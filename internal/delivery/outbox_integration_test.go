package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func outboxTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; disposable PostgreSQL is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	database := cfg.ConnConfig.Database
	if database != "test" && !strings.HasPrefix(database, "test_") && !strings.HasSuffix(database, "_test") {
		t.Fatal("outbox integration test requires an explicitly named disposable test database")
	}
	admin, err := pgxpool.NewWithConfig(context.Background(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := pgx.Identifier{"delivery_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Reuse the migrated schema in an isolated namespace. The test never
	// clears or processes delivery records belonging to another test.
	if _, err := pool.Exec(context.Background(), "CREATE TABLE delivery_outbox (LIKE public.delivery_outbox INCLUDING ALL)"); err != nil {
		t.Fatal(err)
	}
	return pool
}

func waitOutbox(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("outbox did not reach the expected state before the deadline")
}

func TestOutboxRetriesPreserveUserOrderAndDeliveryIdentity(t *testing.T) {
	pool := outboxTestPool(t)
	ctx := context.Background()
	var acknowledge atomic.Bool
	var mu sync.Mutex
	type call struct {
		Sequence int
		Key      string
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Sequence int }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("delivery body was corrupted")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, call{body.Sequence, r.Header.Get("Idempotency-Key")})
		mu.Unlock()
		if body.Sequence == 1 && !acknowledge.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	destination := server.URL + "/api/internal/gateway/users/sync"
	var firstID int64
	for sequence := 1; sequence <= 2; sequence++ {
		raw, err := json.Marshal(Payload{Body: json.RawMessage(fmt.Sprintf(`{"Sequence":%d}`, sequence)), OrderingKey: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := pool.QueryRow(ctx, "INSERT INTO delivery_outbox (kind,destination,payload) VALUES ('gateway',$1,$2) RETURNING id", destination, raw).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if sequence == 1 {
			firstID = id
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(runCtx, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("delivery workers did not stop after cancellation")
		}
	})
	waitOutbox(t, func() bool {
		var deferred bool
		return pool.QueryRow(ctx, "SELECT attempts=1 AND available_at<now()+interval '10 seconds' FROM delivery_outbox WHERE id=$1", firstID).Scan(&deferred) == nil && deferred
	})
	var secondAttempts int
	if err := pool.QueryRow(ctx, "SELECT attempts FROM delivery_outbox WHERE id<>$1", firstID).Scan(&secondAttempts); err != nil || secondAttempts != 0 {
		t.Fatal("a newer change was claimed while the older change was waiting for retry")
	}
	acknowledge.Store(true)
	if _, err := pool.Exec(ctx, "UPDATE delivery_outbox SET available_at=now() WHERE id=$1", firstID); err != nil {
		t.Fatal(err)
	}
	waitOutbox(t, func() bool {
		var remaining int
		return pool.QueryRow(ctx, "SELECT count(*) FROM delivery_outbox").Scan(&remaining) == nil && remaining == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 || calls[0].Sequence != 1 || calls[1].Sequence != 1 || calls[2].Sequence != 2 {
		t.Fatalf("concurrent workers violated retry order or delivered duplicates: %+v", calls)
	}
	if calls[0].Key == "" || calls[0].Key != calls[1].Key || calls[1].Key == calls[2].Key {
		t.Fatal("retries changed delivery identity or reused another delivery's identity")
	}
}
