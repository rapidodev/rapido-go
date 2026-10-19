package tunneldoctor

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }
func ip32(v int32) *int32   { return &v }

// syntheticHistory builds a day of one-minute samples: a quiet night, a busy
// day pinned at 300 Mbit/s on the relay's outbound side with growing loss at
// the top, and two outages.
func syntheticHistory(to time.Time) ([]HistorySample, []HistoryEvent, error) {
	rng := rand.New(rand.NewSource(3))
	var samples []HistorySample
	start := to.Add(-24 * time.Hour)
	for i := 0; i < 24*60; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		hour := at.Hour()
		tx := 20e6 + rng.Float64()*10e6
		loss := 0.0
		if hour >= 10 && hour < 20 {
			tx = 300e6 * (1 + (rng.Float64()-0.5)*0.02)
			if rng.Float64() < 0.3 {
				loss = 10
			}
		}
		samples = append(samples, HistorySample{
			At: at, RelayTx: fp(tx), RelayRx: fp(tx / 10), NodeTx: fp(tx / 10), NodeRx: fp(tx),
			V6Loss: fp(loss), V4Loss: fp(0), V6RTT: fp(90), V4RTT: fp(81),
		})
	}
	events := []HistoryEvent{
		{At: to.Add(-5 * time.Hour), Kind: "tunnel_down", Severity: "critical"},
		{At: to.Add(-5*time.Hour + 2*time.Minute), Kind: "tunnel_up", Severity: "info", DurationSeconds: ip32(120)},
		{At: to.Add(-3 * time.Hour), Kind: "failover", Severity: "warning"},
		{At: to.Add(-3*time.Hour + time.Minute), Kind: "failback", Severity: "info"},
		{At: to.Add(-2 * time.Hour), Kind: "ceiling", Severity: "warning"},
		{At: to.Add(-90 * time.Minute), Kind: "tunnel_up", Severity: "info", DurationSeconds: ip32(45)},
	}
	return samples, events, nil
}

func TestAnalyzeHistoryFindsTheOutagesAndThePlateau(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	samples, events, _ := syntheticHistory(to)
	h := AnalyzeHistory(samples, events, to.Add(-24*time.Hour), to)
	if h.WindowHours != 24 || h.Samples != 1440 {
		t.Errorf("window/samples = %d/%d", h.WindowHours, h.Samples)
	}
	if h.OutageCount != 2 || h.DowntimeSecond != 165 || h.LongestOutage != 120 {
		t.Errorf("outages = %d total=%d longest=%d", h.OutageCount, h.DowntimeSecond, h.LongestOutage)
	}
	if !h.Outages[0].At.Before(h.Outages[1].At) {
		t.Error("outages must be oldest first")
	}
	if h.Failovers != 1 || h.Failbacks != 1 {
		t.Errorf("failovers = %d/%d", h.Failovers, h.Failbacks)
	}
	if !h.Plateau.Detected || h.Plateau.Series != "relay_tx" || h.Plateau.CeilingEvents != 1 {
		t.Fatalf("plateau = %+v", h.Plateau)
	}
	if h.Plateau.LevelMbps < 295 || h.Plateau.LevelMbps > 305 {
		t.Errorf("level = %v", h.Plateau.LevelMbps)
	}
	// ten busy hours = thirty 20-sample windows of flat traffic
	if h.Plateau.Windows < 25 || h.Plateau.WindowsTotal != 72 {
		t.Errorf("windows = %d of %d", h.Plateau.Windows, h.Plateau.WindowsTotal)
	}
	if h.Plateau.NearMaxShare < 0.9 {
		t.Errorf("near max share = %v", h.Plateau.NearMaxShare)
	}
	if len(h.Throughput) != 4 || h.Throughput[0].Max < 299 || h.Throughput[0].Max > 306 {
		t.Errorf("throughput = %+v", h.Throughput)
	}
}

func TestLossByRateComparesQuietAndBusyLoad(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	samples, _, _ := syntheticHistory(to)
	h := AnalyzeHistory(samples, nil, to.Add(-24*time.Hour), to)
	if len(h.LossByRate) < 2 {
		t.Fatalf("bands = %+v", h.LossByRate)
	}
	quiet, busy := h.LossByRate[0], h.LossByRate[len(h.LossByRate)-1]
	if quiet.V6LossPct == nil || *quiet.V6LossPct != 0 || busy.V6LossPct == nil || *busy.V6LossPct < 2 {
		t.Errorf("quiet=%v busy=%v", quiet.V6LossPct, busy.V6LossPct)
	}
	if quiet.ToMbps != 50 || busy.FromMbps != 200 {
		t.Errorf("band edges: %+v / %+v", quiet, busy)
	}
	lo, hi := lossBands(h.LossByRate)
	if lo == nil || hi == nil || hi.loss <= lo.loss {
		t.Fatal("the loss-versus-rate finding should fire for this day")
	}

	flat := make([]HistorySample, 100)
	for i := range flat {
		flat[i] = HistorySample{At: to.Add(time.Duration(-i) * time.Minute), RelayTx: fp(300e6), V6Loss: fp(0.5)}
	}
	hh := AnalyzeHistory(flat, nil, to.Add(-24*time.Hour), to)
	if a, b := lossBands(hh.LossByRate); a != nil || b != nil {
		t.Error("a single band, or equal loss at every load, is not a finding")
	}
}

func TestHistoryFindingsAreWordedFromTheNumbers(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	samples, events, _ := syntheticHistory(to)
	res := &Result{History: AnalyzeHistory(samples, events, to.Add(-24*time.Hour), to), Environment: Environment{RelayNICSpeed: 1000}}
	by := map[string]Finding{}
	for _, f := range historyFindings(res) {
		by[f.Code] = f
	}
	for _, code := range []string{"outages", "failovers", "plateau", "loss_vs_rate", "history_loss"} {
		if _, ok := by[code]; !ok {
			t.Errorf("missing %s", code)
		}
	}
	if d := by["plateau"].Detail; !strings.Contains(d, "relay card, outbound") || !strings.Contains(d, "link speed of 1000 Mbit/s") {
		t.Errorf("plateau detail = %s", d)
	}
	if d := by["outages"].Detail; !strings.Contains(d, "2 outage(s), 165 s in total, the longest 120 s") {
		t.Errorf("outage detail = %s", d)
	}
	if by["outages"].Severity != SevWarning {
		t.Errorf("165 s of downtime is warning-level (critical starts at 300 s), got %s", by["outages"].Severity)
	}
}
