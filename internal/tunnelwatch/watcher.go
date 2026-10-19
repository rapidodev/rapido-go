package tunnelwatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

// Retention of what the watcher stores: samples feed charts and the Network
// Doctor's 24 hour statistics, events are the long-lived timeline.
const (
	SampleRetention = 14 * 24 * time.Hour
	EventRetention  = 90 * 24 * time.Hour
)

// Store is where samples and events live.
type Store interface {
	InsertSample(ctx context.Context, s Sample) error
	InsertEvent(ctx context.Context, e Event) error
	// LastEventTimes returns when each kind of event last happened for the
	// tunnel since the given moment.
	LastEventTimes(ctx context.Context, tunnelID int32, since time.Time) (map[string]time.Time, error)
	// Prune deletes samples older than samplesBefore and events older than
	// eventsBefore.
	Prune(ctx context.Context, samplesBefore, eventsBefore time.Time) (samples, events int64, err error)
}

// Notify sends an event to the notification channels. It is only called for
// events the engine marked Alert.
type Notify func(ctx context.Context, tunnelName string, ev Event)

// Watcher feeds an Engine from the metrics collector and persists the result.
type Watcher struct {
	engine *Engine
	store  Store
	notify Notify
	log    *slog.Logger

	mu    sync.Mutex
	names map[int32]string
}

// NewWatcher builds a watcher; notify may be nil.
func NewWatcher(store Store, notify Notify, log *slog.Logger) *Watcher {
	if log == nil {
		log = slog.Default()
	}
	return &Watcher{engine: NewEngine(Config{}), store: store, notify: notify, log: log, names: map[int32]string{}}
}

// Handle processes one collection round (the OnTick of tunnelmetrics.Run).
func (w *Watcher) Handle(ctx context.Context, rows []tunnelmetrics.Row) {
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		w.remember(row.TunnelID, row.Name)
		w.seed(ctx, row.TunnelID)
		now := row.Metrics.CheckedAt
		if row.Error != "" || now.IsZero() {
			now = time.Now()
		}
		sample, events := w.engine.Observe(row, now)
		if err := w.store.InsertSample(ctx, sample); err != nil {
			w.log.Warn("tunnelwatch: could not store a sample", "tunnel_id", row.TunnelID, "error", err)
		}
		w.persist(ctx, row.Name, events)
	}
}

// RelayTransition records a relay-health up/down change on the tunnel's
// timeline. The relay-health monitor already sends its own alert for the same
// change, so nothing is notified here.
func (w *Watcher) RelayTransition(ctx context.Context, tunnelID int32, up bool, detail string, downFor time.Duration) {
	w.seed(ctx, tunnelID)
	if ev, ok := w.engine.Transition(tunnelID, up, detail, downFor, time.Now()); ok {
		w.persist(ctx, w.name(tunnelID), []Event{ev})
	}
}

func (w *Watcher) persist(ctx context.Context, name string, events []Event) {
	for _, ev := range events {
		if err := w.store.InsertEvent(ctx, ev); err != nil {
			w.log.Warn("tunnelwatch: could not store an event", "tunnel_id", ev.TunnelID, "kind", ev.Kind, "error", err)
		}
		w.log.Info("tunnelwatch: event", "tunnel_id", ev.TunnelID, "kind", ev.Kind, "severity", ev.Severity, "alert", ev.Alert)
		if ev.Alert && w.notify != nil {
			w.notify(ctx, name, ev)
		}
	}
}

// seed loads the tunnel's recent event times into the engine the first time it
// is seen, so a restarted panel keeps honouring the rate limits.
func (w *Watcher) seed(ctx context.Context, tunnelID int32) {
	if w.engine.Seeded(tunnelID) {
		return
	}
	last, err := w.store.LastEventTimes(ctx, tunnelID, time.Now().Add(-24*time.Hour))
	if err != nil {
		w.log.Warn("tunnelwatch: could not read recent events", "tunnel_id", tunnelID, "error", err)
	}
	w.engine.Seed(tunnelID, last)
}

func (w *Watcher) remember(id int32, name string) {
	if name == "" {
		return
	}
	w.mu.Lock()
	w.names[id] = name
	w.mu.Unlock()
}

func (w *Watcher) name(id int32) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.names[id]
}

// SetName tells the watcher what to call a tunnel in alerts raised outside a
// collection round (a relay-health transition).
func (w *Watcher) SetName(id int32, name string) { w.remember(id, name) }

// PruneLoop deletes samples and events past their retention every interval
// until ctx is done.
func (w *Watcher) PruneLoop(ctx context.Context, interval time.Duration) {
	run := func() {
		now := time.Now()
		s, e, err := w.store.Prune(ctx, now.Add(-SampleRetention), now.Add(-EventRetention))
		if err != nil {
			if ctx.Err() == nil {
				w.log.Warn("tunnelwatch: prune failed", "error", err)
			}
			return
		}
		if s > 0 || e > 0 {
			w.log.Info("tunnelwatch: pruned history", "samples", s, "events", e)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
