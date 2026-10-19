package tunnelwatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

type fakeStore struct {
	samples []Sample
	events  []Event
	last    map[string]time.Time
	lastErr error
	pruned  atomic.Int32
}

func (f *fakeStore) InsertSample(_ context.Context, s Sample) error {
	f.samples = append(f.samples, s)
	return nil
}
func (f *fakeStore) InsertEvent(_ context.Context, e Event) error {
	f.events = append(f.events, e)
	return nil
}
func (f *fakeStore) LastEventTimes(_ context.Context, _ int32, _ time.Time) (map[string]time.Time, error) {
	return f.last, f.lastErr
}
func (f *fakeStore) Prune(_ context.Context, _, _ time.Time) (int64, int64, error) {
	f.pruned.Add(1)
	return 1, 2, nil
}

func roundRow(ha *tunnelmetrics.HAProxyStatus, at time.Time) tunnelmetrics.Row {
	r := mkRow(rowOpts{ha: ha, relayTx: 10e6})
	r.Metrics.CheckedAt = at
	return r
}

func TestWatcherStoresEverySampleAndNotifiesOnlyAlertedEvents(t *testing.T) {
	st := &fakeStore{}
	var notified []Event
	var names []string
	w := NewWatcher(st, func(_ context.Context, name string, ev Event) {
		notified = append(notified, ev)
		names = append(names, name)
	}, nil)

	ctx := context.Background()
	w.Handle(ctx, []tunnelmetrics.Row{roundRow(haStatus(14, 0, "UP", "UP", 100, 100), at(0))})
	w.Handle(ctx, []tunnelmetrics.Row{roundRow(haStatus(0, 14, "DOWN", "UP", 10, 160), at(1))})
	w.Handle(ctx, []tunnelmetrics.Row{{TunnelID: 1, Name: "node2", Error: "dial timeout"}})

	if len(st.samples) != 3 {
		t.Fatalf("samples = %d, want one per round", len(st.samples))
	}
	if st.samples[2].RelayTxBps != nil {
		t.Error("an unreachable round stores empty measurements")
	}
	if len(st.events) != 1 || st.events[0].Kind != KindFailover {
		t.Fatalf("events = %+v", st.events)
	}
	if len(notified) != 1 || names[0] != "node2" {
		t.Fatalf("notified = %+v names = %v", notified, names)
	}
}

func TestWatcherSeedsFromTheDatabaseOnceAndSurvivesASeedError(t *testing.T) {
	st := &fakeStore{last: map[string]time.Time{KindFailover: at(0)}}
	var notified int
	w := NewWatcher(st, func(context.Context, string, Event) { notified++ }, nil)
	ctx := context.Background()
	w.Handle(ctx, []tunnelmetrics.Row{roundRow(haStatus(14, 0, "UP", "UP", 100, 100), at(1))})
	w.Handle(ctx, []tunnelmetrics.Row{roundRow(haStatus(0, 14, "DOWN", "UP", 10, 160), at(2))})
	if len(st.events) != 1 || notified != 0 {
		t.Fatalf("the failover is recorded but a recently sent kind is not alerted again: events=%d notified=%d", len(st.events), notified)
	}

	bad := &fakeStore{lastErr: errors.New("db down")}
	w2 := NewWatcher(bad, nil, nil)
	w2.Handle(ctx, []tunnelmetrics.Row{roundRow(nil, at(0))})
	if len(bad.samples) != 1 {
		t.Fatal("a failing seed lookup must not stop sampling")
	}
}

func TestWatcherRecordsRelayTransitionsWithoutNotifying(t *testing.T) {
	st := &fakeStore{}
	notified := 0
	w := NewWatcher(st, func(context.Context, string, Event) { notified++ }, nil)
	ctx := context.Background()
	w.SetName(7, "node4")
	w.RelayTransition(ctx, 7, false, "timeout", 0)
	w.RelayTransition(ctx, 7, false, "timeout", 0)
	w.RelayTransition(ctx, 7, true, "", 90*time.Second)
	if len(st.events) != 2 || st.events[0].Kind != KindTunnelDown || st.events[1].Kind != KindTunnelUp {
		t.Fatalf("events = %+v", st.events)
	}
	if *st.events[1].DurationSeconds != 90 {
		t.Errorf("duration = %d", *st.events[1].DurationSeconds)
	}
	if notified != 0 {
		t.Error("relay health already sends these alerts")
	}
}

func TestPruneLoopRunsImmediatelyAndStopsWithTheContext(t *testing.T) {
	st := &fakeStore{}
	w := NewWatcher(st, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.PruneLoop(ctx, time.Hour); close(done) }()
	deadline := time.After(2 * time.Second)
	for st.pruned.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("prune did not run")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop")
	}
}
