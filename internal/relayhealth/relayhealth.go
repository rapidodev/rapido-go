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
	"encoding/json"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/legendary1205/rapido-go/internal/cache"
)

// redisKey is where PublishStatuses/ReadStatuses keep the live snapshot -
// shared between the backend singleton (the only process that actually
// runs a Monitor) and the api role's GET /api/tunnel-relays handler, which
// has no Monitor of its own to read from directly.
const redisKey = "relayhealth:status"

// redisTTL bounds how long a snapshot is trusted once written: if the
// backend singleton dies, GET /api/tunnel-relays should stop claiming
// relays are up/down from stale data rather than serving it forever.
const redisTTL = 2 * time.Minute

// PublishStatuses writes a Snapshot to Redis - call from OnTick.
func PublishStatuses(ctx context.Context, c *cache.Client, statuses []Status) error {
	raw, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	return c.Set(ctx, redisKey, string(raw), redisTTL)
}

// ReadStatuses reads back the last-published snapshot, keyed by relay id.
// A relay with no entry (nothing published yet, or the key expired) is
// simply absent from the map - the caller decides what "unknown" means for
// its own response shape.
func ReadStatuses(ctx context.Context, c *cache.Client) (map[int32]Status, error) {
	raw, err := c.Get(ctx, redisKey)
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var statuses []Status
	if err := json.Unmarshal([]byte(raw), &statuses); err != nil {
		return nil, err
	}
	out := make(map[int32]Status, len(statuses))
	for _, s := range statuses {
		out[s.ID] = s
	}
	return out, nil
}

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
// came up, false = just went down). downFor is the exact time the relay
// was down, only meaningful when up is true (zero on a relay's first-ever
// sighting already down, where there is no real down period to report -
// see record's own doc comment).
type Alerter func(ctx context.Context, r Relay, up bool, detail string, downFor time.Duration)

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
	// OnTick, if set, is called after every round with the full current
	// Snapshot - the hook cmd/panel/main.go uses to publish live status to
	// Redis so the api role (a separate process from the backend singleton
	// that actually runs this Monitor) can serve it from GET
	// /api/tunnel-relays without needing its own copy of this Monitor.
	OnTick func(snapshot []Status)
}

type entry struct {
	up         bool
	known      bool
	okStreak   int
	failStreak int
	lastError  string
	checkedAt  time.Time
	// since is when up last changed - stays fixed across every round the
	// relay keeps the same state, so a later recovery can compute an
	// exact downtime duration (newChecked - since) rather than only
	// "within one poll interval" - same invariant as
	// internal/tunnelhealth's own Since field.
	since time.Time
}

// Status is one relay's current verdict, as Snapshot reports it.
type Status struct {
	ID        int32     `json:"id"`
	Up        bool      `json:"up"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	// Since is when Up last changed - lets the dashboard show "down for
	// Xm" on a relay that is down right now, without waiting for an
	// alert transition.
	Since time.Time `json:"since"`
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

	if m.opts.OnTick != nil {
		m.opts.OnTick(m.Snapshot())
	}
}

// Snapshot returns every currently-tracked relay's verdict, sorted by id.
func (m *Monitor) Snapshot() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.entries))
	for id, e := range m.entries {
		if !e.known {
			continue
		}
		out = append(out, Status{ID: id, Up: e.up, Error: e.lastError, CheckedAt: e.checkedAt, Since: e.since})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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
		e.lastError = ""
		if !wasKnown || (!e.up && e.okStreak >= m.opts.UpAfter) {
			e.up = true
		}
	} else {
		e.failStreak++
		e.okStreak = 0
		e.lastError = err.Error()
		if !wasKnown || (e.up && e.failStreak >= m.opts.DownAfter) {
			e.up = false
		}
	}
	e.known = true
	e.checkedAt = time.Now()
	nowUp := e.up
	downSince := e.since // only meaningful while down; read before since is overwritten below
	if nowUp != wasUp || !wasKnown {
		e.since = e.checkedAt
	}
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
	var downFor time.Duration
	if nowUp && wasKnown {
		downFor = e.checkedAt.Sub(downSince)
	}
	if nowUp {
		m.opts.Logger.Info("relay recovered", "relay", r.Name, "host", r.Host, "port", r.Port, "down_for", downFor.Round(time.Second).String())
	} else {
		m.opts.Logger.Warn("relay is down", "relay", r.Name, "host", r.Host, "port", r.Port, "error", detail)
	}
	// The very first round for a relay that happens to be down should still
	// alert - unlike tunnelhealth's "never seen = assume up" (which exists
	// so a fresh node doesn't fail everyone over before its first probe
	// lands), a relay with no history and a failing probe right now is
	// exactly the case an admin needs to hear about, not one to wait out.
	if m.opts.Alert != nil {
		m.opts.Alert(ctx, r, nowUp, detail, downFor)
	}
}
