package hostmetrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// tunnelAlertPayload is the handful of fields this watcher needs out of a
// host_metrics row's JSON payload - a local, minimal mirror of the same
// shape internal/httpapi/monitoring.go parses for its own purposes
// (duplicated rather than shared to avoid that package depending back on
// this one).
type tunnelAlertPayload struct {
	Tunnels []struct {
		Name string `json:"name"`
		Up   bool   `json:"up"`
		// Since is when Up last changed, at the node's own probe-cycle
		// precision (tunnelhealth.Monitor's own Interval) - NOT this
		// payload's own push interval. It stays fixed at the down-start
		// moment across every report while still down (see
		// tunnelhealth's own TestSinceIsStableWhileDownAndMovesOnlyOnTransition),
		// which is what lets Tick compute an exact downtime duration on
		// recovery below, bounded only by the node's probe interval
		// (seconds), not by this watcher's own 30s poll interval.
		Since *time.Time `json:"since,omitempty"`
		// Domain narrows down where a down tunnel's fault most likely
		// sits - "tunnel"/"exit"/"node", see tunnelhealth.DialProbe's own
		// doc comment. Empty while up.
		Domain string `json:"domain,omitempty"`
	} `json:"tunnels"`
}

// TunnelAlertQuerier is the subset of *generated.Queries this watcher
// needs, narrowed so it's trivially fakeable in a test.
type TunnelAlertQuerier interface {
	ListNodes(ctx context.Context) ([]generated.Node, error)
	GetLatestHostMetricPerNode(ctx context.Context) ([]generated.HostMetric, error)
}

// TunnelAlerter is told about one tunnel's transition - up is the new
// state. domain is the fault domain ("tunnel"/"exit"/"node") while going
// down, empty while recovering. downFor is the exact time the tunnel was
// down, only meaningful when up is true (a recovery) - zero when this is
// the tunnel's first-ever sighting already up, since there is no down
// period to report.
type TunnelAlerter func(ctx context.Context, nodeName, tunnelName string, up bool, domain string, downFor time.Duration)

type tunnelKey struct {
	nodeID int32
	tunnel string
}

// tunnelKnownState is what the ticker remembers about one tunnel between
// rounds - not just its last state, but the Since of that observation, so
// a later recovery can compute exactly how long it was down (newSince -
// downSince) rather than only "within one poll interval" (see Tick).
type tunnelKnownState struct {
	up    bool
	since time.Time
}

// TunnelAlertTicker holds the cross-round "last known state" a transition
// needs to be detected against. Separated from RunTunnelAlerts so a test
// can call Tick repeatedly without waiting on a real timer.
type TunnelAlertTicker struct {
	q      TunnelAlertQuerier
	alert  TunnelAlerter
	logger *slog.Logger
	known  map[tunnelKey]tunnelKnownState
}

func NewTunnelAlertTicker(q TunnelAlertQuerier, alert TunnelAlerter, logger *slog.Logger) *TunnelAlertTicker {
	return &TunnelAlertTicker{q: q, alert: alert, logger: logger, known: make(map[tunnelKey]tunnelKnownState)}
}

// Tick reads every node's latest tunnel detail and calls alert on every
// up<->down transition since the previous Tick. Every node's tunnels start
// "unknown" on a fresh ticker, so the first sighting of a tunnel never
// fires an alert on its own - only a change from a previously-known state
// does, matching tunnelhealth's own reasoning for why a restart must not
// immediately page everyone about tunnels it has simply never looked at
// yet.
func (w *TunnelAlertTicker) Tick(ctx context.Context) {
	nodes, err := w.q.ListNodes(ctx)
	if err != nil {
		w.logger.Warn("tunnelalerts: could not list nodes", "error", err)
		return
	}
	names := make(map[int32]string, len(nodes))
	for _, n := range nodes {
		names[n.ID] = n.Name
	}

	metrics, err := w.q.GetLatestHostMetricPerNode(ctx)
	if err != nil {
		w.logger.Warn("tunnelalerts: could not read host metrics", "error", err)
		return
	}
	for _, m := range metrics {
		if !m.NodeID.Valid || !m.Payload.Valid || m.Payload.String == "" {
			continue
		}
		var p tunnelAlertPayload
		if err := json.Unmarshal([]byte(m.Payload.String), &p); err != nil {
			continue
		}
		nodeName := names[m.NodeID.Int32]
		for _, t := range p.Tunnels {
			key := tunnelKey{nodeID: m.NodeID.Int32, tunnel: t.Name}
			prev, seen := w.known[key]
			since := prev.since
			if t.Since != nil {
				since = *t.Since
			}
			w.known[key] = tunnelKnownState{up: t.Up, since: since}
			if !seen || prev.up == t.Up {
				continue
			}
			var downFor time.Duration
			if t.Up && !prev.since.IsZero() {
				// prev.since is this same tunnel's down-start moment,
				// carried forward unchanged across every round it stayed
				// down (tunnelhealth's own Since only moves on a real
				// transition) - and since is this round's own, freshly
				// set to the recovery moment. Both at the node's probe-
				// cycle precision, not this ticker's 30s poll interval.
				downFor = since.Sub(prev.since)
			}
			if t.Up {
				w.logger.Info("wireguard tunnel recovered", "node", nodeName, "tunnel", t.Name, "down_for", downFor.Round(time.Second).String())
			} else {
				w.logger.Warn("wireguard tunnel is down", "node", nodeName, "tunnel", t.Name, "domain", t.Domain)
			}
			if w.alert != nil {
				w.alert(ctx, nodeName, t.Name, t.Up, t.Domain, downFor)
			}
		}
	}
}

// RunTunnelAlerts polls the per-node tunnel detail every interval (the same
// data the Monitoring page already reads - see monitoring.go) and calls
// alert on every up<->down transition it sees, so a dropped WireGuard
// tunnel reaches an admin without anyone having to open the dashboard.
func RunTunnelAlerts(ctx context.Context, q TunnelAlertQuerier, alert TunnelAlerter, logger *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	w := NewTunnelAlertTicker(q, alert, logger)
	w.Tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Tick(ctx)
		}
	}
}
