package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type observedBody struct {
	*strings.Reader
	closed bool
}

func (b *observedBody) Close() error { b.closed = true; return nil }

func TestTransportQueuesCompleteBodyAndClosesIt(t *testing.T) {
	body := &observedBody{Reader: strings.NewReader(`{"message":"test"}`)}
	req, _ := http.NewRequest(http.MethodPost, "https://notification.example.com/send", body)
	req.Header.Set("X-Rapido-Delivery-Proxy", "http://localhost:3128")
	called := false
	transport := Transport{Enqueue: func(ctx context.Context, kind, destination string, payload Payload) error {
		called = true
		if kind != "notification" || destination != req.URL.String() || string(payload.Body) != `{"message":"test"}` || payload.Proxy != "http://localhost:3128" {
			t.Error("queued notification lost its destination, body, or proxy")
		}
		return nil
	}}
	resp, err := transport.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK || !called || !body.closed {
		t.Fatalf("queue operation failed or leaked its request body: %v", err)
	}
	resp.Body.Close()
}

func TestTransportRejectsTruncatedNotificationsAndQueueFailures(t *testing.T) {
	t.Run("missing queue", func(t *testing.T) {
		body := &observedBody{Reader: strings.NewReader(`{}`)}
		req, _ := http.NewRequest(http.MethodPost, "https://notification.example.com/send", body)
		if _, err := (Transport{}).RoundTrip(req); err == nil || !body.closed {
			t.Fatal("missing queue was acknowledged as success or the request body was left open")
		}
	})
	t.Run("oversized body", func(t *testing.T) {
		called := false
		body := &observedBody{Reader: strings.NewReader(strings.Repeat("x", (1<<20)+1))}
		req, _ := http.NewRequest(http.MethodPost, "https://notification.example.com/send", body)
		transport := Transport{Enqueue: func(context.Context, string, string, Payload) error { called = true; return nil }}
		if _, err := transport.RoundTrip(req); err == nil || called || !body.closed {
			t.Fatal("oversized notification was queued, silently truncated, or left open")
		}
	})
	t.Run("queue failure", func(t *testing.T) {
		want := errors.New("test-only queue failure")
		body := &observedBody{Reader: strings.NewReader(`{}`)}
		req, _ := http.NewRequest(http.MethodPost, "https://notification.example.com/send", body)
		transport := Transport{Enqueue: func(context.Context, string, string, Payload) error { return want }}
		if _, err := transport.RoundTrip(req); !errors.Is(err, want) || !body.closed {
			t.Fatal("queue failure was acknowledged as success or the request body was left open")
		}
	})
	t.Run("empty body", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "https://notification.example.com/send", nil)
		transport := Transport{Enqueue: func(context.Context, string, string, Payload) error { return nil }}
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	})
}

func TestForumTopicCreationUsesSynchronousTransport(t *testing.T) {
	called := false
	transport := Transport{
		Enqueue: func(context.Context, string, string, Payload) error { t.Error("topic creation was queued"); return nil },
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_thread_id":123}}`))}, nil
		}),
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot-test-only/createForumTopic", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil || !called {
		t.Fatal("topic creation did not reach the synchronous transport")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"message_thread_id":123`) {
		t.Fatal("topic creation response was replaced by a queue acknowledgement")
	}
}

func TestQueuedDeliveriesDoNotForwardSecretsOnRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	for _, proxied := range []bool{false, true} {
		payload := Payload{Body: json.RawMessage(`{"username":"alice"}`), Headers: map[string]string{"Authorization": "Bearer test-only"}}
		if proxied {
			payload.Proxy = redirect.URL
		}
		err := sendDelivery(context.Background(), &http.Client{Timeout: time.Second}, "gateway", 12, redirect.URL+"/api/internal/gateway/users/sync", payload)
		if err == nil {
			t.Fatal("redirect was acknowledged as a successful delivery")
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("a direct or proxied delivery forwarded credentials or user data to a redirect target")
	}
}

func TestGatewayDeliveryRejectsUnsafeDestinationsBeforeSending(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("unsafe destination reached the HTTP transport")
		return nil, errors.New("test transport should not be reached")
	})}
	for _, destination := range []string{
		"http://peer.example.com/api/internal/gateway/users/sync",
		"https://peer.example.com/api/internal/gateway/users/sync?",
		"https://user:test-only@peer.example.com/api/internal/gateway/users/sync",
		"https://peer.example.com/other-endpoint",
	} {
		if err := sendDelivery(context.Background(), client, "gateway", 1, destination, Payload{}); err == nil {
			t.Error("unsafe gateway destination accepted")
		}
	}
}

func TestGatewayDeliveryRequiresExplicitAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"accepted", `{"ok":true}`, 200, false},
		{"rejected", `{"ok":false}`, 200, true},
		{"malformed", `not-json`, 200, true},
		{"server failure", `{"ok":true}`, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Idempotency-Key") != "rapido-delivery-42" || r.Header.Get("Authorization") != "Bearer test-only" {
					t.Error("delivery identity or authentication header missing")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			err := sendDelivery(context.Background(), &http.Client{Timeout: time.Second}, "gateway", 42, server.URL+"/api/internal/gateway/users/sync", Payload{Body: json.RawMessage(`{}`), Headers: map[string]string{"Authorization": "Bearer test-only"}})
			if (err != nil) != tc.wantErr {
				t.Fatalf("delivery error = %v, want error = %v", err, tc.wantErr)
			}
		})
	}
}
