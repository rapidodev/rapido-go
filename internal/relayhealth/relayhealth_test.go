package relayhealth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeAlert struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeAlert) record(ctx context.Context, r Relay, up bool, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := "down"
	if up {
		state = "up"
	}
	f.calls = append(f.calls, r.Name+":"+state)
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
