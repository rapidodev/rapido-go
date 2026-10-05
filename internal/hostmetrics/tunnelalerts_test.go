package hostmetrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

type fakeTunnelAlertQuerier struct {
	nodes   []generated.Node
	metrics []generated.HostMetric
}

func (f *fakeTunnelAlertQuerier) ListNodes(ctx context.Context) ([]generated.Node, error) {
	return f.nodes, nil
}
func (f *fakeTunnelAlertQuerier) GetLatestHostMetricPerNode(ctx context.Context) ([]generated.HostMetric, error) {
	return f.metrics, nil
}

type tunnelJSON struct {
	Name   string     `json:"name"`
	Up     bool       `json:"up"`
	Since  *time.Time `json:"since,omitempty"`
	Domain string     `json:"domain,omitempty"`
}

func tunnelPayload(t *testing.T, tunnels map[string]bool) pgtype.Text {
	t.Helper()
	return tunnelPayloadDetailed(t, tunnels, nil)
}

// tunnelPayloadDetailed is tunnelPayload plus a per-tunnel Since override
// (defaults to time.Now() when absent) - what the downtime-duration tests
// need to control.
func tunnelPayloadDetailed(t *testing.T, tunnels map[string]bool, since map[string]time.Time) pgtype.Text {
	t.Helper()
	p := struct {
		Tunnels []tunnelJSON `json:"tunnels"`
	}{}
	for name, up := range tunnels {
		s := time.Now()
		if v, ok := since[name]; ok {
			s = v
		}
		domain := ""
		if !up {
			domain = "exit"
		}
		p.Tunnels = append(p.Tunnels, tunnelJSON{Name: name, Up: up, Since: &s, Domain: domain})
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return pgtype.Text{String: string(raw), Valid: true}
}

func testAlertLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestTunnelAlertTickerStaysQuietOnFirstSightingEvenIfDown(t *testing.T) {
	var calls []string
	q := &fakeTunnelAlertQuerier{
		nodes: []generated.Node{{ID: 1, Name: "node2"}},
		metrics: []generated.HostMetric{{
			NodeID: pgtype.Int4{Int32: 1, Valid: true}, Payload: tunnelPayload(t, map[string]bool{"germany": false}),
		}},
	}
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, up bool, domain string, downFor time.Duration) {
		calls = append(calls, nodeName+"/"+tunnelName)
	}, testAlertLogger())

	w.Tick(context.Background())

	if len(calls) != 0 {
		t.Errorf("alerts after first sighting = %v, want none (no prior state to transition from)", calls)
	}
}

func TestTunnelAlertTickerFiresOnADownTransitionAndAUpTransition(t *testing.T) {
	var calls []string
	up := true
	q := &fakeTunnelAlertQuerier{nodes: []generated.Node{{ID: 1, Name: "node2"}}}
	refreshMetrics := func() {
		q.metrics = []generated.HostMetric{{
			NodeID: pgtype.Int4{Int32: 1, Valid: true}, Payload: tunnelPayload(t, map[string]bool{"germany": up}),
		}}
	}
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, gotUp bool, domain string, downFor time.Duration) {
		state := "down"
		if gotUp {
			state = "up"
		}
		calls = append(calls, nodeName+"/"+tunnelName+":"+state)
	}, testAlertLogger())

	refreshMetrics()
	w.Tick(context.Background()) // first sighting, up - quiet
	if len(calls) != 0 {
		t.Fatalf("alerts after first (up) sighting = %v, want none", calls)
	}

	up = false
	refreshMetrics()
	w.Tick(context.Background()) // transition to down
	if len(calls) != 1 || calls[0] != "node2/germany:down" {
		t.Fatalf("alerts after going down = %v, want [node2/germany:down]", calls)
	}

	w.Tick(context.Background()) // unchanged (still down) - no new alert
	if len(calls) != 1 {
		t.Fatalf("alerts after an unchanged tick = %v, want still just the one", calls)
	}

	up = true
	refreshMetrics()
	w.Tick(context.Background()) // transition back to up
	if len(calls) != 2 || calls[1] != "node2/germany:up" {
		t.Fatalf("alerts after recovering = %v, want [node2/germany:down node2/germany:up]", calls)
	}
}

// TestTunnelAlertTickerReportsExactDowntimeAndDomain is the real reason
// Since/Domain were added to the payload: an admin needs to know not just
// that a tunnel came back, but how long it was actually down and, while it
// was down, whether the fault looked local (tunnel) or the tunnel's own
// remote exit (e.g. Mullvad) - see tunnelhealth.DialProbe's own doc
// comment for how the node tells those apart.
func TestTunnelAlertTickerReportsExactDowntimeAndDomain(t *testing.T) {
	type call struct {
		up      bool
		domain  string
		downFor time.Duration
	}
	var calls []call
	q := &fakeTunnelAlertQuerier{nodes: []generated.Node{{ID: 1, Name: "node2"}}}
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, up bool, domain string, downFor time.Duration) {
		calls = append(calls, call{up, domain, downFor})
	}, testAlertLogger())

	downSince := time.Now()
	q.metrics = []generated.HostMetric{{
		NodeID: pgtype.Int4{Int32: 1, Valid: true},
		Payload: tunnelPayloadDetailed(t, map[string]bool{"uae": true}, nil),
	}}
	w.Tick(context.Background()) // first sighting, up - quiet

	q.metrics = []generated.HostMetric{{
		NodeID: pgtype.Int4{Int32: 1, Valid: true},
		Payload: tunnelPayloadDetailed(t, map[string]bool{"uae": false}, map[string]time.Time{"uae": downSince}),
	}}
	w.Tick(context.Background()) // down - Since pinned to downSince

	// Two more rounds still down: a real node keeps reporting the SAME
	// Since while down (tunnelhealth's own invariant) - simulated here by
	// passing the same downSince again, exactly like a real payload would.
	w.Tick(context.Background())

	recoveredAt := downSince.Add(76 * time.Second) // 1m16s, matching the real-world example this feature was built for
	q.metrics = []generated.HostMetric{{
		NodeID: pgtype.Int4{Int32: 1, Valid: true},
		Payload: tunnelPayloadDetailed(t, map[string]bool{"uae": true}, map[string]time.Time{"uae": recoveredAt}),
	}}
	w.Tick(context.Background()) // recovered

	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want exactly 2 (down, then up)", calls)
	}
	if calls[0].up || calls[0].domain != "exit" {
		t.Errorf("down call = %+v, want up=false domain=exit", calls[0])
	}
	if !calls[1].up {
		t.Fatalf("recovery call = %+v, want up=true", calls[1])
	}
	if calls[1].downFor != 76*time.Second {
		t.Errorf("downFor = %v, want exactly 1m16s", calls[1].downFor)
	}
}

func TestTunnelAlertTickerSkipsNodesWithoutAPayload(t *testing.T) {
	var calls []string
	q := &fakeTunnelAlertQuerier{
		nodes: []generated.Node{{ID: 1, Name: "node2"}},
		metrics: []generated.HostMetric{{
			NodeID: pgtype.Int4{Int32: 1, Valid: true}, Payload: pgtype.Text{Valid: false},
		}},
	}
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, up bool, domain string, downFor time.Duration) {
		calls = append(calls, nodeName+"/"+tunnelName)
	}, testAlertLogger())

	w.Tick(context.Background())

	if len(calls) != 0 {
		t.Errorf("alerts = %v, want none (no payload to read)", calls)
	}
}
