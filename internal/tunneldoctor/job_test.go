package tunneldoctor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemKV() *memKV { return &memKV{m: map[string]string{}} }
func (k *memKV) Get(_ context.Context, key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	if !ok {
		return "", redis.Nil
	}
	return v, nil
}
func (k *memKV) Set(_ context.Context, key, value string, _ time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}
func (k *memKV) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.m[key]; ok {
		return false, nil
	}
	k.m[key] = value
	return true, nil
}
func (k *memKV) Del(_ context.Context, keys ...string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, key := range keys {
		delete(k.m, key)
	}
	return nil
}

func TestJobLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	jobs := Jobs{KV: newMemKV(), Now: func() time.Time { return now }}

	if _, err := jobs.Load(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no job yet: %v", err)
	}
	started, job, err := jobs.Start(ctx, 1)
	if err != nil || !started || job.Status != StatusRunning {
		t.Fatalf("start: %v %v %+v", started, err, job)
	}
	started, cur, _ := jobs.Start(ctx, 1)
	if started || cur == nil || cur.Status != StatusRunning {
		t.Fatalf("a second start while running must be refused: %v %+v", started, cur)
	}
	if other, _, _ := jobs.Start(ctx, 2); !other {
		t.Error("tunnels are independent")
	}

	now = now.Add(30 * time.Second)
	if err := jobs.Finish(ctx, 1, job.StartedAt, &Result{Tunnel: TunnelInfo{ID: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := jobs.Load(ctx, 1)
	if err != nil || got.Status != StatusDone || got.Result == nil || got.FinishedAt == nil {
		t.Fatalf("done: %v %+v", err, got)
	}
	if again, _, _ := jobs.Start(ctx, 1); !again {
		t.Error("a finished diagnosis releases the tunnel")
	}
	if err := jobs.Finish(ctx, 1, now, nil, errors.New("neither end reachable")); err != nil {
		t.Fatal(err)
	}
	got, _ = jobs.Load(ctx, 1)
	if got.Status != StatusFailed || got.Error == "" || got.Result != nil {
		t.Fatalf("failed: %+v", got)
	}
}

func TestAnInterruptedRunningJobReadsAsFailed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	jobs := Jobs{KV: newMemKV(), Now: func() time.Time { return now }}
	jobs.Start(ctx, 7)
	now = now.Add(10 * time.Minute)
	got, err := jobs.Load(ctx, 7)
	if err != nil || got.Status != StatusFailed || got.Error == "" {
		t.Fatalf("got %+v err %v", got, err)
	}
}
