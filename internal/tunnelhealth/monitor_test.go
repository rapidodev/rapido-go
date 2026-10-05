package tunnelhealth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// fakeProbe answers per interface from a table the test edits between rounds.
type fakeProbe struct {
	mu      sync.Mutex
	results map[string]error
	rtt     time.Duration
}

func (f *fakeProbe) set(iface string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[iface] = err
}

func (f *fakeProbe) probe(ctx context.Context, iface string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.results[iface]; err != nil {
		return 0, err
	}
	return f.rtt, nil
}

func newTestMonitor(t *testing.T, tunnels ...string) (*Monitor, *fakeProbe) {
	t.Helper()
	fp := &fakeProbe{results: map[string]error{}, rtt: 40 * time.Millisecond}
	m := New(Options{
		Probe:    fp.probe,
		Discover: func() []string { return tunnels },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return m, fp
}

func TestUnprobedTunnelCountsAsUp(t *testing.T) {
	m, _ := newTestMonitor(t, "uk")
	if !m.Up("uk") {
		t.Error("a tunnel that has never been probed must count as up, or a fresh start sends everyone through the fallback")
	}
	if !m.Up("never-heard-of-it") {
		t.Error("an unknown name is also optimistic")
	}
}

func TestFirstObservationIsTakenAtFaceValue(t *testing.T) {
	m, fp := newTestMonitor(t, "uk", "sweden")
	fp.set("sweden", errors.New("i/o timeout"))
	m.Tick(context.Background())

	if !m.Up("uk") {
		t.Error("uk answered its first probe, want up")
	}
	if m.Up("sweden") {
		t.Error("sweden failed its first probe, want down straight away - no debounce on the first look")
	}
}

func TestUpTunnelNeedsConsecutiveFailuresToGoDown(t *testing.T) {
	m, fp := newTestMonitor(t, "uk")
	ctx := context.Background()
	m.Tick(ctx)

	fp.set("uk", errors.New("i/o timeout"))
	m.Tick(ctx)
	if !m.Up("uk") {
		t.Fatal("one failed round must not take a tunnel down")
	}
	m.Tick(ctx)
	if m.Up("uk") {
		t.Fatal("two consecutive failed rounds must take it down")
	}
}

func TestOneGoodRoundBetweenFailuresResetsTheCount(t *testing.T) {
	m, fp := newTestMonitor(t, "uk")
	ctx := context.Background()
	m.Tick(ctx)

	fp.set("uk", errors.New("i/o timeout"))
	m.Tick(ctx)
	fp.set("uk", nil)
	m.Tick(ctx)
	fp.set("uk", errors.New("i/o timeout"))
	m.Tick(ctx)
	if !m.Up("uk") {
		t.Fatal("failures separated by a good round are not consecutive, tunnel must stay up")
	}
}

func TestMissingInterfaceIsDownImmediately(t *testing.T) {
	m, fp := newTestMonitor(t, "italy")
	ctx := context.Background()
	m.Tick(ctx)
	if !m.Up("italy") {
		t.Fatal("setup: italy should start up")
	}

	fp.set("italy", ErrMissing)
	m.Tick(ctx)
	if m.Up("italy") {
		t.Fatal("wg-quick down removes the interface - that has to be noticed in one round, not two")
	}
	st, _ := m.Status("italy")
	if st.Present {
		t.Errorf("Present = true for a missing interface: %+v", st)
	}
}

func TestDownTunnelNeedsSeveralGoodRoundsToRecover(t *testing.T) {
	m, fp := newTestMonitor(t, "uk")
	ctx := context.Background()
	fp.set("uk", errors.New("down"))
	m.Tick(ctx)
	if m.Up("uk") {
		t.Fatal("setup: uk should start down")
	}

	fp.set("uk", nil)
	m.Tick(ctx)
	m.Tick(ctx)
	if m.Up("uk") {
		t.Fatal("two good rounds are not enough - a flapping tunnel must not bounce users")
	}
	m.Tick(ctx)
	if !m.Up("uk") {
		t.Fatal("three good rounds must bring it back")
	}
}

func TestWatchedInterfaceIsProbedButNotReported(t *testing.T) {
	m, fp := newTestMonitor(t, "uk")
	fp.set("germany", ErrMissing)
	m.Watch([]string{"germany", ""})
	m.Tick(context.Background())

	if m.Up("germany") {
		t.Error("a watched interface that does not exist is down - the fallback supervisor needs to know")
	}
	health := m.Health()
	if _, ok := health["germany"]; ok {
		t.Error("germany lives on another server: reporting it here would show a permanent false outage")
	}
	if h, ok := health["uk"]; !ok || !h.Up || !h.Present {
		t.Errorf("uk is this host's own tunnel and must be reported up: %+v", health)
	}
}

func TestHealthCarriesProbeLatencyOnlyWhileUp(t *testing.T) {
	m, fp := newTestMonitor(t, "uk", "sweden")
	fp.set("sweden", errors.New("i/o timeout"))
	m.Tick(context.Background())

	health := m.Health()
	if h := health["uk"]; h.ProbeMs == nil || *h.ProbeMs != 40 {
		t.Errorf("uk probe = %v, want 40ms", h.ProbeMs)
	}
	if h := health["sweden"]; h.ProbeMs != nil || h.Error != "i/o timeout" {
		t.Errorf("sweden = %+v, want no latency and the probe's error", h)
	}
}

func TestTunnelsThatDisappearFromDiscoveryAreForgotten(t *testing.T) {
	names := []string{"uk", "sweden"}
	fp := &fakeProbe{results: map[string]error{}, rtt: time.Millisecond}
	m := New(Options{
		Probe:    fp.probe,
		Discover: func() []string { return names },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	m.Tick(context.Background())
	names = []string{"uk"}
	m.Tick(context.Background())

	if _, ok := m.Status("sweden"); ok {
		t.Error("a tunnel nobody watches or configures any more must be dropped, not linger as stale state")
	}
}

// TestDomainClassification is the real reason ErrExitUnreachable/
// ErrNodeOffline exist: an admin reading an alert needs to know whether a
// down tunnel is this host's own fault, its tunnel's own remote exit
// (e.g. Mullvad), or this host having no internet at all right now -
// three different things to go fix.
func TestDomainClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"missing interface", ErrMissing, "tunnel"},
		{"node offline", ErrNodeOffline, "node"},
		{"exit unreachable", ErrExitUnreachable, "exit"},
		{"unrecognized error defaults to exit", errors.New("boom"), "exit"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, fp := newTestMonitor(t, "uk")
			fp.set("uk", c.err)
			m.Tick(context.Background())
			health := m.Health()
			if got := health["uk"].Domain; got != c.want {
				t.Errorf("domain = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDomainClearsOnRecovery(t *testing.T) {
	m, fp := newTestMonitor(t, "uk")
	fp.set("uk", ErrExitUnreachable)
	m.Tick(context.Background())
	if health := m.Health(); health["uk"].Domain != "exit" {
		t.Fatalf("setup: want domain=exit while down, got %+v", health["uk"])
	}

	fp.set("uk", nil)
	m.Tick(context.Background())
	m.Tick(context.Background())
	m.Tick(context.Background())
	if health := m.Health(); health["uk"].Domain != "" {
		t.Errorf("domain = %q, want empty once recovered", health["uk"].Domain)
	}
}

// TestSinceIsStableWhileDownAndMovesOnlyOnTransition is what lets the
// panel compute an exact downtime duration later (see
// internal/hostmetrics/tunnelalerts.go): Since must stay fixed at the
// original down-start moment across every tick while still down, not
// creep forward, and must jump only on the tick that actually flips state.
func TestSinceIsStableWhileDownAndMovesOnlyOnTransition(t *testing.T) {
	m, fp := newTestMonitor(t, "uk")
	fp.set("uk", ErrExitUnreachable)
	m.Tick(context.Background())
	downSince := *m.Health()["uk"].Since

	time.Sleep(5 * time.Millisecond)
	m.Tick(context.Background())
	if got := *m.Health()["uk"].Since; !got.Equal(downSince) {
		t.Errorf("Since moved across two down ticks: %v -> %v", downSince, got)
	}

	fp.set("uk", nil)
	m.Tick(context.Background())
	m.Tick(context.Background())
	m.Tick(context.Background())
	recoveredSince := *m.Health()["uk"].Since
	if !recoveredSince.After(downSince) {
		t.Errorf("Since did not move on recovery: still %v", recoveredSince)
	}
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	m, _ := newTestMonitor(t, "uk")
	m.opts.Interval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	time.Sleep(50 * time.Millisecond)
	if _, ok := m.Status("uk"); !ok {
		t.Error("Run must probe immediately and on every tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
