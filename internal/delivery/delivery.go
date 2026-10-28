// Package delivery keeps external effects outside API/database transactions.
package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Payload struct {
	Body        json.RawMessage   `json:"body"`
	Headers     map[string]string `json:"headers,omitempty"`
	OrderingKey string            `json:"ordering_key,omitempty"`
	Proxy       string            `json:"proxy,omitempty"`
}
type Enqueue func(context.Context, string, string, Payload) error
type Transport struct {
	Enqueue Enqueue
	Base    http.RoundTripper
}

func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/createForumTopic") {
		base := t.Base
		if base == nil {
			base = http.DefaultTransport
		}
		return base.RoundTrip(r)
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := t.Enqueue(r.Context(), "notification", r.URL.String(), Payload{Body: raw, Proxy: r.Header.Get("X-Rapido-Delivery-Proxy")}); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
}

// Run claims short leases with SKIP LOCKED. A crash leaves a retryable record.
// Gateway destinations retain FIFO order; older failed changes block newer ones.
func Run(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		kind := "notification"
		if i < 4 {
			kind = "gateway"
		}
		wg.Add(1)
		go func(kind string) { defer wg.Done(); worker(ctx, pool, kind, logger) }(kind)
	}
	<-ctx.Done()
	wg.Wait()
}
func worker(ctx context.Context, pool *pgxpool.Pool, kind string, logger *slog.Logger) {
	client := &http.Client{Timeout: 10 * time.Second}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		for i := 0; i < 25; i++ {
			var id int64
			var destination string
			var raw []byte
			var attempts int
			err := pool.QueryRow(ctx, `WITH candidate AS (
 SELECT o.id FROM delivery_outbox o WHERE kind=$1 AND available_at<=now()
 AND ($1!='gateway' OR NOT EXISTS(SELECT 1 FROM delivery_outbox previous WHERE previous.kind=o.kind AND previous.destination=o.destination AND COALESCE(previous.payload->>'ordering_key','')=COALESCE(o.payload->>'ordering_key','') AND previous.id<o.id))
 ORDER BY o.id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE delivery_outbox o SET available_at=now()+interval '2 minutes',attempts=attempts+1 FROM candidate c WHERE o.id=c.id RETURNING o.id,o.destination,o.payload,o.attempts`, kind).Scan(&id, &destination, &raw, &attempts)
			if err != nil {
				break
			}
			var payload Payload
			err = json.Unmarshal(raw, &payload)
			if err == nil {
				var req *http.Request
				req, err = http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(payload.Body))
				if err == nil {
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Idempotency-Key", fmt.Sprintf("rapido-delivery-%d", id))
					for k, v := range payload.Headers {
						req.Header.Set(k, v)
					}
					var resp *http.Response
					sendClient := client
					if payload.Proxy != "" {
						if proxy, parseErr := url.Parse(payload.Proxy); parseErr == nil {
							sendClient = &http.Client{Timeout: client.Timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
						}
					}
					resp, err = sendClient.Do(req)
					if err == nil {
						_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
						resp.Body.Close()
						if resp.StatusCode < 200 || resp.StatusCode >= 300 {
							err = fmt.Errorf("HTTP %d", resp.StatusCode)
						}
					}
				}
			}
			if err == nil {
				_, err = pool.Exec(ctx, "DELETE FROM delivery_outbox WHERE id=$1", id)
			} else {
				delay := time.Duration(1<<min(attempts, 10)) * time.Second
				_, _ = pool.Exec(ctx, "UPDATE delivery_outbox SET available_at=now()+$2::interval WHERE id=$1", id, fmt.Sprintf("%f seconds", delay.Seconds()))
				logger.Warn("outbox delivery deferred", "delivery_id", id, "kind", kind, "attempt", attempts)
			}
		}
	}
}

func (Transport) QueuedDelivery() {}
