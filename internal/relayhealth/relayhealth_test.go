package relayhealth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeAlert struct {
	mu      sync.Mutex
	calls   []string
	downFor []time.Duration
}

func (f *fakeAlert) record(ctx context.Context, r Relay, up bool, detail string, downFor time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := "down"
	if up {
		state = "up"
	}
	f.calls = append(f.calls, r.Name+":"+state)
	f.downFor = append(f.downFor, downFor)
}

func (f *fakeAlert) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func relayList(relays []Relay) Lister {
	return func(ctx context.Context) ([]Relay, error) { return relays, nil }
}

// TestADownRelayAlertsOnFirstProbe is the deliberate difference from
// tunnelhealth's "never probed = assume up": an admin needs to hear about a
// relay that is already broken when it is first registered, not have it
// silently treated as healthy until some later round.
func TestADownRelayAlertsOnFirstProbe(t *testing.T) {
	alert := &fakeAlert{}
	relay := Relay{ID: 1, Name: "node1-relay", Host: "x", Port: 1}
	m := New(Options{
		Lister: relayList([]Relay{relay}),
		Prober: func(ctx context.Context, r Relay) (time.Duration, error) { return 0, errors.New("dial refused") },
		Alert:  alert.record,
	})
	m.Tick(context.Background())

	got := alert.snapshot()
	if len(got) != 1 || got[0] != "node1-relay:down" {
		t.Errorf("alerts = %v, want [node1-relay:down]", got)
	}
}

// TestFlappingRelayNeedsConsecutiveRoundsBeforeEachTransition proves the
// hysteresis: a single bad or single good round must not flip the reported
// state, only DownAfter/UpAfter consecutive ones.
func TestFlappingRelayNeedsConsecutiveRoundsBeforeEachTransition(t *testing.T) {
	alert := &fakeAlert{}
	relay := Relay{ID: 1, Name: "r", Host: "x", Port: 1}
	up := true
	m := New(Options{
		Lister: relayList([]Relay{relay}),
		Prober: func(ctx context.Context, r Relay) (time.Duration, error) {
			if up {
				return time.Millisecond, nil
			}
			return 0, errors.New("down")
		},
		Alert:     alert.record,
		DownAfter: 2, UpAfter: 2,
	})

	m.Tick(context.Background()) // round 1: up -> first sighting, up, no alert (nothing to transition from)
	up = false
	m.Tick(context.Background()) // round 2: 1st fail - not enough yet
	if got := alert.snapshot(); len(got) != 0 {
		t.Fatalf("after 1 failed round, alerts = %v, want none yet", got)
	}
	m.Tick(context.Background()) // round 3: 2nd consecutive fail - now down
	got := alert.snapshot()
	if len(got) != 1 || got[0] != "r:down" {
		t.Fatalf("after 2 failed rounds, alerts = %v, want [r:down]", got)
	}

	up = true
	m.Tick(context.Background()) // 1st ok - not enough yet
	if got := alert.snapshot(); len(got) != 1 {
		t.Fatalf("after 1 ok round, alerts = %v, want still just the down one", got)
	}
	m.Tick(context.Background()) // 2nd consecutive ok - now up
	got = alert.snapshot()
	if len(got) != 2 || got[1] != "r:up" {
		t.Fatalf("after 2 ok rounds, alerts = %v, want [r:down r:up]", got)
	}
}

// TestRecoveryReportsExactDowntime proves Alert's downFor is the real
// elapsed time the relay was down (DownAfter/UpAfter rounds included),
// not just "however long Tick happens to take" - computed from each
// entry's own since, which record pins at the moment of the transition
// and leaves untouched on every round the relay stays in the same state.
func TestRecoveryReportsExactDowntime(t *testing.T) {
	alert := &fakeAlert{}
	relay := Relay{ID: 1, Name: "r", Host: "x", Port: 1}
	up := true
	m := New(Options{
		Lister: relayList([]Relay{relay}),
		Prober: func(ctx context.Context, r Relay) (time.Duration, error) {
			if up {
				return time.Millisecond, nil
			}
			return 0, errors.New("down")
		},
		Alert:     alert.record,
		DownAfter: 1, UpAfter: 1,
	})

	m.Tick(context.Background()) // first sighting, up - quiet
	up = false
	m.Tick(context.Background()) // down transition

	time.Sleep(20 * time.Millisecond)
	up = true
	m.Tick(context.Background()) // up transition

	calls, downFor := alert.snapshot(), alert.downFor
	if len(calls) != 2 || calls[1] != "r:up" {
		t.Fatalf("calls = %v, want [r:down r:up]", calls)
	}
	if downFor[0] != 0 {
		t.Errorf("downFor on the DOWN call = %v, want 0 (nothing to report yet)", downFor[0])
	}
	if downFor[1] < 15*time.Millisecond {
		t.Errorf("downFor on recovery = %v, want at least ~20ms", downFor[1])
	}
}

// TestSnapshotAndOnTickReportEveryTrackedRelay covers the data Phase 1's
// dashboard reads: Snapshot (polled by the GET /api/tunnel-relays handler
// indirectly, via the Redis blob OnTick publishes) must carry every
// relay's current up/error/checked-at, and OnTick must fire with exactly
// that same data after each round - not just on a transition, unlike
// Alert.
func TestSnapshotAndOnTickReportEveryTrackedRelay(t *testing.T) {
	var onTickCalls [][]Status
	healthy := Relay{ID: 1, Name: "ok", Host: "x", Port: 1}
	broken := Relay{ID: 2, Name: "bad", Host: "x", Port: 2}
	m := New(Options{
		Lister: relayList([]Relay{healthy, broken}),
		Prober: func(ctx context.Context, r Relay) (time.Duration, error) {
			if r.ID == healthy.ID {
				return time.Millisecond, nil
			}
			return 0, errors.New("refused")
		},
		OnTick: func(s []Status) { onTickCalls = append(onTickCalls, s) },
	})
	m.Tick(context.Background())

	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot = %v, want 2 entries", snap)
	}
	if !snap[0].Up || snap[0].Error != "" {
		t.Errorf("healthy relay status = %+v, want Up=true Error=\"\"", snap[0])
	}
	if snap[1].Up || snap[1].Error != "refused" {
		t.Errorf("broken relay status = %+v, want Up=false Error=\"refused\"", snap[1])
	}
	if snap[0].CheckedAt.IsZero() || snap[1].CheckedAt.IsZero() {
		t.Error("CheckedAt was never set")
	}

	if len(onTickCalls) != 1 || len(onTickCalls[0]) != 2 {
		t.Fatalf("OnTick calls = %v, want exactly 1 call with 2 statuses", onTickCalls)
	}
}

// TestRemovedRelayStopsBeingTracked proves a relay deleted via the API
// (next Lister call simply omits it) does not leave a stale entry that
// would resume its old streak if the same id were ever reused.
func TestRemovedRelayStopsBeingTracked(t *testing.T) {
	alert := &fakeAlert{}
	relay := Relay{ID: 1, Name: "r", Host: "x", Port: 1}
	relays := []Relay{relay}
	m := New(Options{
		Lister: func(ctx context.Context) ([]Relay, error) { return relays, nil },
		Prober: func(ctx context.Context, r Relay) (time.Duration, error) { return 0, errors.New("down") },
		Alert:  alert.record,
	})
	m.Tick(context.Background())
	if len(m.entries) != 1 {
		t.Fatalf("entries = %d, want 1 before removal", len(m.entries))
	}
	relays = nil
	m.Tick(context.Background())
	if len(m.entries) != 0 {
		t.Errorf("entries = %d, want 0 after the relay was removed", len(m.entries))
	}
}
