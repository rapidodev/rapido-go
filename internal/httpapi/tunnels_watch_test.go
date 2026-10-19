package httpapi

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/tunneldiscover"
)

func TestBucketSecondsKeepsChartsUnderThreeHundredPoints(t *testing.T) {
	cases := map[int]int{1: 60, 5: 60, 6: 72, 24: 288, 168: 2016, 336: 4032}
	for hours, want := range cases {
		got := bucketSeconds(hours)
		if got != want {
			t.Errorf("hours=%d bucket=%d want %d", hours, got, want)
		}
		if points := hours * 3600 / got; points > maxChartPoints {
			t.Errorf("hours=%d gives %d points", hours, points)
		}
	}
}

func TestParseSinceAcceptsRFC3339AndUnixSeconds(t *testing.T) {
	if got, ok := parseSince("2026-10-19T12:00:00Z"); !ok || !got.Equal(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("rfc3339: %v %v", got, ok)
	}
	if got, ok := parseSince("1760875200"); !ok || got.Unix() != 1760875200 {
		t.Errorf("unix: %v %v", got, ok)
	}
	for _, bad := range []string{"", "yesterday", "-5"} {
		if _, ok := parseSince(bad); ok {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestClampInt(t *testing.T) {
	if clampInt("", 24, 1, 336) != 24 || clampInt("0", 24, 1, 336) != 1 || clampInt("9999", 24, 1, 336) != 336 || clampInt("x", 24, 1, 336) != 24 {
		t.Error("clampInt")
	}
}

func TestSamplePointMapsSentinelsToNull(t *testing.T) {
	p := toSamplePoint(generated.ListTunnelSamplesBinnedRow{
		Bucket:     pgtype.Timestamptz{Time: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), Valid: true},
		RelayRxBps: 5e6, RelayTxBps: -1, NodeRxBps: -1, NodeTxBps: -1,
		RelayRxBpsMax: 9e6, RelayTxBpsMax: -1, NodeRxBpsMax: -1, NodeTxBpsMax: -1,
		Connections: 12, Cpu: 3, V6RttMs: 90.5, V6Loss: 0, V6LossMax: 10, V6JitterMs: 0.2,
		V4RttMs: -1, V4Loss: -1, V4LossMax: -1, V4JitterMs: -1,
		HaActive: "relay_v6", HaPrimaryUp: 1, HaBackupUp: -1, Samples: 5,
	})
	if p.RelayRxBps == nil || *p.RelayRxBps != 5e6 || p.RelayTxBps != nil || p.V4Loss != nil {
		t.Errorf("point = %+v", p)
	}
	if p.V6Loss == nil || *p.V6Loss != 0 {
		t.Error("a measured zero loss is 0, not null")
	}
	if p.HAPrimaryUp == nil || !*p.HAPrimaryUp || p.HABackupUp != nil || p.HAActive == nil || *p.HAActive != "relay_v6" {
		t.Errorf("ha = %v %v %v", p.HAActive, p.HAPrimaryUp, p.HABackupUp)
	}
	if down := triState(0); down == nil || *down {
		t.Error("0 means false")
	}
}

func TestControlPortOfPrefersWhatThePanelAllocatedThenDetection(t *testing.T) {
	built := generated.Tunnel{Managed: true, FrpControlPort: 27005}
	if got := controlPortOf(built, tunneldiscover.Details{FRP: &tunneldiscover.FRPInfo{ControlPort: 7000}}); got != 27005 {
		t.Errorf("built tunnel: %d", got)
	}
	found := generated.Tunnel{Managed: false}
	if got := controlPortOf(found, tunneldiscover.Details{FRP: &tunneldiscover.FRPInfo{ControlPort: 7000}}); got != 7000 {
		t.Errorf("frp config: %d", got)
	}
	ha := tunneldiscover.Details{HAProxy: &tunneldiscover.HAInfo{Servers: []tunneldiscover.HAServer{{Name: "a", Addr: "[2001:db8::1]:7100"}}}}
	if got := controlPortOf(found, ha); got != 7100 {
		t.Errorf("haproxy server: %d", got)
	}
	if got := controlPortOf(found, tunneldiscover.Details{}); got != 0 {
		t.Errorf("nothing known: %d", got)
	}
}

func TestReportFilenameIsSafe(t *testing.T) {
	at := time.Date(2026, 10, 19, 12, 30, 0, 0, time.UTC)
	if got := reportFilename(`node 2"; rm -rf /`, "fa", at); got != "network-report-node-2-rm--rf--20261019-1230.fa.md" && got != "network-report-node-2-rm-rf--20261019-1230.fa.md" {
		t.Errorf("got %q", got)
	}
	if got := reportFilename("", "en", at); got != "network-report-tunnel-20261019-1230.en.md" {
		t.Errorf("got %q", got)
	}
}

func TestHistorySampleFromRowKeepsNullsAsNil(t *testing.T) {
	s := historySampleFromRow(generated.TunnelSample{
		At:         pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true},
		RelayTxBps: pgtype.Float8{Float64: 5e6, Valid: true}, V6Loss: pgtype.Float4{Float32: 2.5, Valid: true},
		Connections: pgtype.Int4{Int32: 9, Valid: true},
	})
	if s.RelayTx == nil || *s.RelayTx != 5e6 || s.RelayRx != nil || s.V4Loss != nil || s.V6Loss == nil || *s.V6Loss != 2.5 || *s.Connections != 9 {
		t.Errorf("sample = %+v", s)
	}
}
