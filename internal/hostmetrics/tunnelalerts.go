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
	} `json:"tunnels"`
}

// TunnelAlertQuerier is the subset of *generated.Queries this watcher
// needs, narrowed so it's trivially fakeable in a test.
type TunnelAlertQuerier interface {
	ListNodes(ctx context.Context) ([]generated.Node, error)
	GetLatestHostMetricPerNode(ctx context.Context) ([]generated.HostMetric, error)
}

// TunnelAlerter is told about one tunnel's transition - up is the new
// state.
type TunnelAlerter func(ctx context.Context, nodeName, tunnelName string, up bool)

type tunnelKey struct {
	nodeID int32
	tunnel string
}

// TunnelAlertTicker holds the cross-round "last known state" a transition
// needs to be detected against. Separated from RunTunnelAlerts so a test
// can call Tick repeatedly without waiting on a real timer.
type TunnelAlertTicker struct {
	q      TunnelAlertQuerier
	alert  TunnelAlerter
	logger *slog.Logger
	known  map[tunnelKey]bool
}

func NewTunnelAlertTicker(q TunnelAlertQuerier, alert TunnelAlerter, logger *slog.Logger) *TunnelAlertTicker {
	return &TunnelAlertTicker{q: q, alert: alert, logger: logger, known: make(map[tunnelKey]bool)}
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
			wasUp, seen := w.known[key]
			w.known[key] = t.Up
			if !seen || wasUp == t.Up {
				continue
			}
			if t.Up {
				w.logger.Info("wireguard tunnel recovered", "node", nodeName, "tunnel", t.Name)
			} else {
				w.logger.Warn("wireguard tunnel is down", "node", nodeName, "tunnel", t.Name)
			}
			if w.alert != nil {
				w.alert(ctx, nodeName, t.Name, t.Up)
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
