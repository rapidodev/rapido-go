package hostmetrics

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

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

func tunnelPayload(t *testing.T, tunnels map[string]bool) pgtype.Text {
	t.Helper()
	type tunnelJSON struct {
		Name string `json:"name"`
		Up   bool   `json:"up"`
	}
	p := struct {
		Tunnels []tunnelJSON `json:"tunnels"`
	}{}
	for name, up := range tunnels {
		p.Tunnels = append(p.Tunnels, tunnelJSON{Name: name, Up: up})
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
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, up bool) {
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
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, gotUp bool) {
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

func TestTunnelAlertTickerSkipsNodesWithoutAPayload(t *testing.T) {
	var calls []string
	q := &fakeTunnelAlertQuerier{
		nodes: []generated.Node{{ID: 1, Name: "node2"}},
		metrics: []generated.HostMetric{{
			NodeID: pgtype.Int4{Int32: 1, Valid: true}, Payload: pgtype.Text{Valid: false},
		}},
	}
	w := NewTunnelAlertTicker(q, func(ctx context.Context, nodeName, tunnelName string, up bool) {
		calls = append(calls, nodeName+"/"+tunnelName)
	}, testAlertLogger())

	w.Tick(context.Background())

	if len(calls) != 0 {
		t.Errorf("alerts = %v, want none (no payload to read)", calls)
	}
}
