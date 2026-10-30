// Package delivery keeps external effects outside API/database transactions.
package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/legendary1205/rapido-go/internal/controlurl"
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
	if r.Body != nil {
		defer r.Body.Close()
	}
	if t.Enqueue == nil {
		return nil, fmt.Errorf("delivery queue is not configured")
	}
	var raw []byte
	var err error
	if r.Body != nil {
		raw, err = io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 1<<20 {
		return nil, fmt.Errorf("notification body exceeds 1 MiB")
	}
	if err := t.Enqueue(r.Context(), "notification", r.URL.String(), Payload{Body: raw, Proxy: r.Header.Get("X-Rapido-Delivery-Proxy")}); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
}

func sendDelivery(ctx context.Context, client *http.Client, kind string, id int64, destination string, payload Payload) error {
	if kind == "gateway" {
		u, err := url.Parse(destination)
		if err != nil || u.Path != "/api/internal/gateway/users/sync" {
			return fmt.Errorf("invalid gateway delivery destination")
		}
		origin := *u
		origin.Path, origin.RawPath = "", ""
		if _, err := controlurl.Normalize(origin.String()); err != nil {
			return fmt.Errorf("unsafe gateway delivery destination")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(payload.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("rapido-delivery-%d", id))
	for key, value := range payload.Headers {
		req.Header.Set(key, value)
	}
	sendClient := *client
	sendClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if payload.Proxy != "" {
		proxy, err := url.Parse(payload.Proxy)
		if err != nil || proxy.Hostname() == "" || !proxy.IsAbs() {
			return fmt.Errorf("invalid delivery proxy URL")
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		defer transport.CloseIdleConnections()
		sendClient.Transport = transport
	}
	resp, err := sendClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return deliveryRefused{status: resp.StatusCode}
	}
	if kind == "gateway" {
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
			return fmt.Errorf("invalid gateway delivery acknowledgement: %w", err)
		}
		if !result.OK {
			return fmt.Errorf("gateway did not acknowledge the delivery")
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	}
	return nil
}

// refuseInternalAddress is a net.Dialer Control: it sees the address actually
// being connected to, after DNS, so a name resolving to one of these gains nothing.
func refuseInternalAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("refusing to deliver to internal address %s", ip)
	}
	return nil
}

// deliveryRefused is the other end answering, with something other than success.
type deliveryRefused struct{ status int }

func (e deliveryRefused) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

// permanent is an answer that retrying won't change: the request itself is what's
// being refused. An auth failure isn't one, since someone can fix the secret and
// the change then still arrives, in order.
func (e deliveryRefused) permanent() bool {
	switch e.status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return e.status >= 400 && e.status < 500
}

// maxRefusedAttempts is how many times a permanently refused delivery is tried:
// with the backoff below that's hours, enough for a passing misconfiguration. In a
// gateway's first-in-first-out order, keeping it forever would block every later
// change to that user on that peer.
const maxRefusedAttempts = 20

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
	if kind == "notification" {
		// A webhook goes wherever an admin typed. It can't be allowed to reach the
		// panel itself or a cloud provider's metadata service. Private ranges stay
		// open: a bot on the same network is an ordinary setup.
		client.Transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: refuseInternalAddress}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			IdleConnTimeout:     90 * time.Second,
			MaxIdleConns:        16,
		}
	}
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
				err = sendDelivery(ctx, client, kind, id, destination, payload)
			}
			if err == nil {
				_, err = pool.Exec(ctx, "DELETE FROM delivery_outbox WHERE id=$1", id)
			} else if refused := (deliveryRefused{}); errors.As(err, &refused) && refused.permanent() && attempts >= maxRefusedAttempts {
				_, _ = pool.Exec(ctx, "DELETE FROM delivery_outbox WHERE id=$1", id)
				logger.Error("outbox delivery dropped: refused every time", "delivery_id", id, "kind", kind, "status", refused.status, "attempts", attempts)
			} else {
				delay := time.Duration(1<<min(attempts, 10)) * time.Second
				_, _ = pool.Exec(ctx, "UPDATE delivery_outbox SET available_at=now()+$2::interval WHERE id=$1", id, fmt.Sprintf("%f seconds", delay.Seconds()))
				logger.Warn("outbox delivery deferred", "delivery_id", id, "kind", kind, "attempt", attempts, "error", controlurl.RedactError(err))
			}
		}
	}
}

func (Transport) QueuedDelivery() {}
