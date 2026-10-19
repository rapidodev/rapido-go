package tunneldoctor

import (
	"sort"
	"time"

	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/tunnelwatch"
)

// HistorySample is one stored round (see tunnelwatch.Sample); bits per second,
// percent and milliseconds, nil when not measured.
type HistorySample struct {
	At                               time.Time
	RelayRx, RelayTx, NodeRx, NodeTx *float64
	V6Loss, V4Loss, V6RTT, V4RTT     *float64
	Connections                      *float64
}

// HistoryEvent is one stored event.
type HistoryEvent struct {
	At              time.Time
	Kind            string
	Severity        string
	Detail          string
	DurationSeconds *int32
}

// Outage is one period during which the relay did not answer.
type Outage struct {
	At      time.Time `json:"at"`
	Seconds int32     `json:"seconds"`
}

// SeriesStats are the throughput percentiles of one series, in Mbit/s.
type SeriesStats struct {
	Series string  `json:"series"`
	Label  string  `json:"label"`
	N      int     `json:"samples"`
	P50    float64 `json:"p50_mbps"`
	P90    float64 `json:"p90_mbps"`
	P99    float64 `json:"p99_mbps"`
	Max    float64 `json:"max_mbps"`
}

// PlateauSummary says whether, and where, traffic sat flat at one level.
type PlateauSummary struct {
	// Detected is true when at least one window of the history was flat (see
	// tunnelwatch.DetectPlateau) or the watcher recorded a ceiling event.
	Detected bool   `json:"detected"`
	Series   string `json:"series,omitempty"`
	Label    string `json:"label,omitempty"`
	// LevelMbps is the mean level of the flat windows of the busiest series.
	LevelMbps float64 `json:"level_mbps,omitempty"`
	// Windows of WindowsTotal consecutive 20-sample windows were flat.
	Windows      int `json:"windows"`
	WindowsTotal int `json:"windows_total"`
	// FlatMinutes is the time those windows cover.
	FlatMinutes int `json:"flat_minutes"`
	// NearMaxShare is, of the samples above half the maximum, the share that
	// lies within 5% of the maximum.
	NearMaxShare  float64 `json:"near_max_share"`
	CeilingEvents int     `json:"ceiling_events"`
}

// RateBand groups samples by how much traffic the relay's card carried and
// shows the path quality seen at that load.
type RateBand struct {
	FromMbps  float64  `json:"from_mbps"`
	ToMbps    float64  `json:"to_mbps"` // 0 = no upper bound
	Samples   int      `json:"samples"`
	V6LossPct *float64 `json:"v6_loss_pct"`
	V4LossPct *float64 `json:"v4_loss_pct"`
	V6RTTMs   *float64 `json:"v6_rtt_ms"`
	V4RTTMs   *float64 `json:"v4_rtt_ms"`
}

// History is the analysis of the stored samples and events of a window.
type History struct {
	WindowHours int       `json:"window_hours"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Samples     int       `json:"samples"`

	Outages        []Outage `json:"outages"`
	OutageCount    int      `json:"outage_count"`
	DowntimeSecond int32    `json:"downtime_seconds"`
	LongestOutage  int32    `json:"longest_outage_seconds"`
	Failovers      int      `json:"failovers"`
	Failbacks      int      `json:"failbacks"`
	LossEvents     int      `json:"loss_events"`
	NICSaturated   int      `json:"nic_saturated_events"`

	Throughput []SeriesStats  `json:"throughput"`
	Plateau    PlateauSummary `json:"plateau"`
	LossByRate []RateBand     `json:"loss_by_rate"`
	// V6LossPct/V4LossPct are the mean loss over the window, and
	// V6LossyShare/V4LossyShare the share of samples with any loss.
	V6LossPct    *float64 `json:"v6_loss_pct"`
	V4LossPct    *float64 `json:"v4_loss_pct"`
	V6LossyShare *float64 `json:"v6_lossy_share"`
	V4LossyShare *float64 `json:"v4_lossy_share"`
}

var seriesDefs = []struct {
	key, label string
	get        func(HistorySample) *float64
}{
	{"relay_tx", "relay card, outbound", func(s HistorySample) *float64 { return s.RelayTx }},
	{"relay_rx", "relay card, inbound", func(s HistorySample) *float64 { return s.RelayRx }},
	{"node_tx", "node card, outbound", func(s HistorySample) *float64 { return s.NodeTx }},
	{"node_rx", "node card, inbound", func(s HistorySample) *float64 { return s.NodeRx }},
}

// rateBandEdges are the upper edges (Mbit/s) of the load bands used to compare
// loss at different rates.
var rateBandEdges = []float64{10, 50, 100, 200, 400}

// AnalyzeHistory computes the History of the given samples (oldest first) and
// events over the window ending at to.
func AnalyzeHistory(samples []HistorySample, events []HistoryEvent, from, to time.Time) History {
	h := History{WindowHours: int(to.Sub(from).Round(time.Hour) / time.Hour), From: from.UTC(), To: to.UTC(), Samples: len(samples), Outages: []Outage{}}
	sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })

	ceilingEvents := 0
	for _, e := range events {
		switch e.Kind {
		case tunnelwatch.KindTunnelUp:
			if e.DurationSeconds != nil {
				h.Outages = append(h.Outages, Outage{At: e.At.Add(-time.Duration(*e.DurationSeconds) * time.Second).UTC(), Seconds: *e.DurationSeconds})
				h.DowntimeSecond += *e.DurationSeconds
				if *e.DurationSeconds > h.LongestOutage {
					h.LongestOutage = *e.DurationSeconds
				}
			}
		case tunnelwatch.KindFailover:
			h.Failovers++
		case tunnelwatch.KindFailback:
			h.Failbacks++
		case tunnelwatch.KindLossHigh:
			h.LossEvents++
		case tunnelwatch.KindCeiling:
			ceilingEvents++
		case tunnelwatch.KindNICSaturated:
			h.NICSaturated++
		}
	}
	h.OutageCount = len(h.Outages)
	sort.Slice(h.Outages, func(i, j int) bool { return h.Outages[i].At.Before(h.Outages[j].At) })

	// Throughput percentiles and the plateau search, per series.
	var busiest string
	var busiestLabel string
	var busiestMax float64
	var busiestVals []float64
	for i, def := range seriesDefs {
		var vals []float64
		for _, s := range samples {
			if v := def.get(s); v != nil && *v >= 0 {
				vals = append(vals, *v)
			}
		}
		if len(vals) == 0 {
			continue
		}
		st := netprobe.Summarize(vals)
		h.Throughput = append(h.Throughput, SeriesStats{
			Series: def.key, Label: def.label, N: st.N,
			P50: st.P50 / 1e6, P90: st.P90 / 1e6, P99: st.P99 / 1e6, Max: st.Max / 1e6,
		})
		if st.Max > busiestMax || i == 0 {
			busiestMax, busiest, busiestLabel, busiestVals = st.Max, def.key, def.label, vals
		}
	}
	if busiest != "" {
		h.Plateau = analysePlateau(busiest, busiestLabel, busiestVals)
	}
	h.Plateau.CeilingEvents = ceilingEvents
	if ceilingEvents > 0 {
		h.Plateau.Detected = true
	}

	h.LossByRate = lossByRate(samples)
	h.V6LossPct, h.V6LossyShare = lossSummary(samples, func(s HistorySample) *float64 { return s.V6Loss })
	h.V4LossPct, h.V4LossyShare = lossSummary(samples, func(s HistorySample) *float64 { return s.V4Loss })
	return h
}

func analysePlateau(key, label string, vals []float64) PlateauSummary {
	p := PlateauSummary{Series: key, Label: label}
	cfg := tunnelwatch.DefaultPlateau
	var levels []float64
	for start := 0; start+cfg.Window <= len(vals); start += cfg.Window {
		p.WindowsTotal++
		if pl, ok := tunnelwatch.DetectPlateau(vals[start:start+cfg.Window], cfg); ok {
			p.Windows++
			levels = append(levels, pl.Level)
		}
	}
	if p.Windows > 0 {
		var sum float64
		for _, l := range levels {
			sum += l
		}
		p.LevelMbps = sum / float64(len(levels)) / 1e6
		p.FlatMinutes = p.Windows * cfg.Window
		p.Detected = true
	}
	st := netprobe.Summarize(vals)
	var busy, near int
	for _, v := range vals {
		if v >= st.Max/2 {
			busy++
			if v >= st.Max*(1-cfg.Tolerance) {
				near++
			}
		}
	}
	if busy > 0 {
		p.NearMaxShare = float64(near) / float64(busy)
	}
	return p
}

func lossSummary(samples []HistorySample, get func(HistorySample) *float64) (mean, lossyShare *float64) {
	var sum float64
	var n, lossy int
	for _, s := range samples {
		if v := get(s); v != nil {
			sum += *v
			n++
			if *v > 0 {
				lossy++
			}
		}
	}
	if n == 0 {
		return nil, nil
	}
	m, share := sum/float64(n), float64(lossy)/float64(n)
	return &m, &share
}

func lossByRate(samples []HistorySample) []RateBand {
	type acc struct {
		n                  int
		l6, l4, r6, r4     float64
		nl6, nl4, nr6, nr4 int
	}
	accs := make([]acc, len(rateBandEdges)+1)
	for _, s := range samples {
		var load float64
		switch {
		case s.RelayTx != nil && s.RelayRx != nil:
			load = max(*s.RelayTx, *s.RelayRx)
		case s.RelayTx != nil:
			load = *s.RelayTx
		case s.RelayRx != nil:
			load = *s.RelayRx
		default:
			continue
		}
		mbps := load / 1e6
		i := sort.SearchFloat64s(rateBandEdges, mbps)
		if i < len(rateBandEdges) && mbps == rateBandEdges[i] {
			i++
		}
		a := &accs[i]
		a.n++
		if s.V6Loss != nil {
			a.l6 += *s.V6Loss
			a.nl6++
		}
		if s.V4Loss != nil {
			a.l4 += *s.V4Loss
			a.nl4++
		}
		if s.V6RTT != nil {
			a.r6 += *s.V6RTT
			a.nr6++
		}
		if s.V4RTT != nil {
			a.r4 += *s.V4RTT
			a.nr4++
		}
	}
	mean := func(sum float64, n int) *float64 {
		if n == 0 {
			return nil
		}
		m := sum / float64(n)
		return &m
	}
	var out []RateBand
	for i, a := range accs {
		if a.n == 0 {
			continue
		}
		b := RateBand{Samples: a.n}
		if i > 0 {
			b.FromMbps = rateBandEdges[i-1]
		}
		if i < len(rateBandEdges) {
			b.ToMbps = rateBandEdges[i]
		}
		b.V6LossPct, b.V4LossPct = mean(a.l6, a.nl6), mean(a.l4, a.nl4)
		b.V6RTTMs, b.V4RTTMs = mean(a.r6, a.nr6), mean(a.r4, a.nr4)
		out = append(out, b)
	}
	return out
}
