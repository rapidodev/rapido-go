// Package relayhealth probes external TCP-forwarding boxes (GRE+FRP relays
// and similar) that sit in front of a node but live entirely outside this
// fleet - rapido-go has no other way to know they exist or that one has
// gone dark. A relay is registered in the tunnel_relays table as a
// name/host/port; reaching that port end-to-end (relay -> its tunnel to the
// node -> frps -> the node's own inbound) is a meaningful proxy for "this
// relay is still forwarding traffic" without needing SSH access or a
// provider-specific bandwidth-quota API, and it catches a quota-exhausted
// box the same way it catches a crashed process or a severed tunnel: the
// port simply stops answering.
package relayhealth

import (
	"context"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Relay is one endpoint to probe.
type Relay struct {
	ID   int32
	Name string
	Host string
	Port int32
}

// Prober dials one relay and returns how long it took. Defaults to
// DialProbe.
type Prober func(ctx context.Context, r Relay) (time.Duration, error)

// DialProbe opens a real TCP connection - the same check a client's own
// handshake attempt would make - and closes it immediately.
func DialProbe(ctx context.Context, r Relay) (time.Duration, error) {
	start := time.Now()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(r.Host, strconv.Itoa(int(r.Port))))
	if err != nil {
		return 0, err
	}
	conn.Close()
	return time.Since(start), nil
}

// Lister returns the current relays to watch, called fresh every round so
// an admin's add/remove via the API takes effect on the next tick.
type Lister func(ctx context.Context) ([]Relay, error)

// Alerter is told about a transition - up means the new state (true = just
// came up, false = just went down).
type Alerter func(ctx context.Context, r Relay, up bool, detail string)

type Options struct {
	Prober   Prober
	Lister   Lister
	Alert    Alerter
	Interval time.Duration
	// DownAfter/UpAfter are consecutive-round thresholds before a
	// transition fires, so one dropped probe never pages anyone - mirrors
	// internal/tunnelhealth's own reasoning.
	DownAfter    int
	UpAfter      int
	ProbeTimeout time.Duration
	Logger       *slog.Logger
}

type entry struct {
	up         bool
	known      bool
	okStreak   int
	failStreak int
}

type Monitor struct {
	opts    Options
	mu      sync.Mutex
	entries map[int32]*entry
}

func New(o Options) *Monitor {
	if o.Prober == nil {
		o.Prober = DialProbe
	}
	if o.Interval <= 0 {
		o.Interval = 30 * time.Second
	}
	if o.DownAfter <= 0 {
		o.DownAfter = 2
	}
	if o.UpAfter <= 0 {
		o.UpAfter = 2
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = 5 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Monitor{opts: o, entries: make(map[int32]*entry)}
}

// Run probes every interval until ctx is done. The first round runs
// immediately.
func (m *Monitor) Run(ctx context.Context) {
	m.Tick(ctx)
	t := time.NewTicker(m.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick(ctx)
		}
	}
}

func (m *Monitor) Tick(ctx context.Context) {
	relays, err := m.opts.Lister(ctx)
	if err != nil {
		m.opts.Logger.Warn("relayhealth: could not list relays", "error", err)
		return
	}
	sort.Slice(relays, func(i, j int) bool { return relays[i].ID < relays[j].ID })

	var wg sync.WaitGroup
	for _, r := range relays {
		wg.Add(1)
		go func(r Relay) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, m.opts.ProbeTimeout)
			defer cancel()
			_, err := m.opts.Prober(pctx, r)
			m.record(ctx, r, err)
		}(r)
	}
	wg.Wait()

	// Drop entries for relays the admin removed, so a re-added relay with
	// the same id starts its streaks fresh rather than resuming a stale one.
	keep := make(map[int32]bool, len(relays))
	for _, r := range relays {
		keep[r.ID] = true
	}
	m.mu.Lock()
	for id := range m.entries {
		if !keep[id] {
			delete(m.entries, id)
		}
	}
	m.mu.Unlock()
}

func (m *Monitor) record(ctx context.Context, r Relay, err error) {
	m.mu.Lock()
	e := m.entries[r.ID]
	if e == nil {
		e = &entry{}
		m.entries[r.ID] = e
	}
	wasKnown, wasUp := e.known, e.up

	if err == nil {
		e.okStreak++
		e.failStreak = 0
		if !wasKnown || (!e.up && e.okStreak >= m.opts.UpAfter) {
			e.up = true
		}
	} else {
		e.failStreak++
		e.okStreak = 0
		if !wasKnown || (e.up && e.failStreak >= m.opts.DownAfter) {
			e.up = false
		}
	}
	e.known = true
	nowUp := e.up
	m.mu.Unlock()

	switch {
	case !wasKnown && nowUp:
		// First sighting and healthy - nothing has "recovered", stay quiet.
		return
	case wasKnown && nowUp == wasUp:
		return
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	if nowUp {
		m.opts.Logger.Info("relay recovered", "relay", r.Name, "host", r.Host, "port", r.Port)
	} else {
		m.opts.Logger.Warn("relay is down", "relay", r.Name, "host", r.Host, "port", r.Port, "error", detail)
	}
	// The very first round for a relay that happens to be down should still
	// alert - unlike tunnelhealth's "never seen = assume up" (which exists
	// so a fresh node doesn't fail everyone over before its first probe
	// lands), a relay with no history and a failing probe right now is
	// exactly the case an admin needs to hear about, not one to wait out.
	if m.opts.Alert != nil {
		m.opts.Alert(ctx, r, nowUp, detail)
	}
}
