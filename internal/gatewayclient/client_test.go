package gatewayclient

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
)

func TestPeerURLRejectsUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{
		"", "/peer", "http://remote.example.com", "http://localhost.example.com",
		"https://user:test-only@peer.example.com", "https://peer.example.com/prefix",
		"https://peer.example.com/?route=other", "https://peer.example.com/?",
		"https://peer.example.com/#fragment", "https://peer.example.com:0",
		"https://peer.example.com:65536", "ftp://peer.example.com",
	} {
		if _, err := NormalizeBaseURL(raw); err == nil {
			t.Errorf("unsafe or ambiguous peer URL was accepted: %q", raw)
		}
	}
	for _, raw := range []string{"https://peer.example.com/", "http://127.0.0.1:8000/", "http://[::1]:8000/"} {
		got, err := NormalizeBaseURL(raw)
		if err != nil || got != strings.TrimRight(raw, "/") {
			t.Errorf("valid peer URL rejected or changed: %q, %v", got, err)
		}
	}
}

func TestPeerRequestsNeverFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer target.Close()
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-only-peer-secret" {
				t.Error("initial peer request did not carry the expected authentication")
			}
			http.Redirect(w, r, target.URL, status)
		}))
		for _, call := range []struct {
			name string
			run  func(context.Context, string) error
		}{
			{"ping", func(ctx context.Context, base string) error {
				_, err := Ping(ctx, base, "test-only-peer-secret")
				return err
			}},
			{"status", func(ctx context.Context, base string) error {
				_, err := Status(ctx, base, "test-only-peer-secret")
				return err
			}},
			{"sync", func(ctx context.Context, base string) error {
				_, err := SyncUser(ctx, base, "test-only-peer-secret", UserSyncPayload{Username: "alice"})
				return err
			}},
		} {
			t.Run(call.name+"/"+http.StatusText(status), func(t *testing.T) {
				if err := call.run(context.Background(), source.URL); err == nil {
					t.Fatal("redirect was reported as a successful peer operation")
				}
			})
		}
		source.Close()
	}
	if got := redirected.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests; peer credentials or user data could leak", got)
	}
}

func TestPeerOperationsUseExpectedRoutesAndDecodeResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only-peer-secret" {
			t.Error("peer authentication header missing")
		}
		switch r.URL.Path {
		case "/api/internal/gateway/ping":
			_, _ = io.WriteString(w, `{"panel_name":"peer-one"}`)
		case "/api/internal/gateway/status":
			_, _ = io.WriteString(w, `{"crowdedness":23,"hosts":[]}`)
		case "/api/internal/gateway/users/sync":
			var payload UserSyncPayload
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Error("user sync used an incorrect request method or content type")
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Username != "alice" || !payload.Deleted {
				t.Error("user sync did not preserve the requested user operation")
			}
			_, _ = io.WriteString(w, `{"ok":true,"detail":"synced"}`)
		default:
			t.Error("unexpected peer route")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	if got, err := Ping(ctx, server.URL, "test-only-peer-secret"); err != nil || got.PanelName != "peer-one" {
		t.Fatalf("ping failed: %+v, %v", got, err)
	}
	if got, err := Status(ctx, server.URL, "test-only-peer-secret"); err != nil || got.Crowdedness != 23 {
		t.Fatalf("status failed: %+v, %v", got, err)
	}
	if got, err := SyncUser(ctx, server.URL, "test-only-peer-secret", UserSyncPayload{Username: "alice", Deleted: true}); err != nil || !got.OK {
		t.Fatalf("sync failed: %+v, %v", got, err)
	}
}

func TestUserSyncRequiresExplicitAcknowledgement(t *testing.T) {
	for _, body := range []string{"not-json", `{"ok":false}`, `{}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		if _, err := SyncUser(context.Background(), server.URL, "test-only-peer-secret", UserSyncPayload{Username: "alice"}); err == nil {
			t.Error("invalid response was acknowledged as a successful sync")
		}
		server.Close()
	}
}

func TestPeerCallsHonorCancellation(t *testing.T) {
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, pingErr := Ping(ctx, server.URL, "test-only-peer-secret")
	_, statusErr := Status(ctx, server.URL, "test-only-peer-secret")
	_, syncErr := SyncUser(ctx, server.URL, "test-only-peer-secret", UserSyncPayload{Username: "alice"})
	for _, err := range []error{pingErr, statusErr, syncErr} {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancellation was not propagated: %v", err)
		}
	}
	if sent.Load() != 0 {
		t.Fatal("peer operations continued sending after cancellation")
	}
}
