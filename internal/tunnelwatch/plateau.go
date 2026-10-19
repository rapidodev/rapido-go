package tunnelwatch

import (
	"math"
	"sort"
)

// PlateauConfig tunes the "traffic is pinned flat at a ceiling" heuristic.
type PlateauConfig struct {
	// MinBps is the smallest peak that counts: a flat 2 Mbit/s trickle is not
	// a bandwidth ceiling, just a quiet link.
	MinBps float64
	// Window is how many of the latest samples are examined; MinSamples is how
	// many must exist before any verdict is given.
	Window     int
	MinSamples int
	// Tolerance is how close to the peak (as a fraction of it) a sample must
	// be to count as "on the plateau"; Share is the fraction of the window
	// that must be on it.
	Tolerance float64
	Share     float64
}

// DefaultPlateau is what the watcher uses: at least 100 Mbit/s, and at least
// 60% of the last 20 one-minute samples within 5% of the peak. Samples are
// one minute apart, so that is twelve minutes of traffic stuck at one level -
// real demand from many clients does not hold still within 5% for that long,
// a shaper or a saturated port does.
var DefaultPlateau = PlateauConfig{
	MinBps:     100e6,
	Window:     20,
	MinSamples: 15,
	Tolerance:  0.05,
	Share:      0.60,
}

// Plateau is the verdict of DetectPlateau.
type Plateau struct {
	// Peak is the robust peak (95th percentile) of the window, Level the mean
	// of the samples that sit on the plateau.
	Peak  float64
	Level float64
	// OnPlateau of Window samples are within Tolerance of Peak.
	OnPlateau int
	Window    int
}

// DetectPlateau looks at the latest samples (oldest first, bits per second;
// negative values are "not measured" and skipped) and reports whether traffic
// is pinned at a ceiling.
//
// The peak used is the 95th percentile of the window rather than the maximum,
// so one measurement spike above a flat top does not hide the plateau under
// it.
func DetectPlateau(values []float64, cfg PlateauConfig) (Plateau, bool) {
	if cfg.Window <= 0 {
		cfg = DefaultPlateau
	}
	if len(values) > cfg.Window {
		values = values[len(values)-cfg.Window:]
	}
	clean := make([]float64, 0, len(values))
	for _, v := range values {
		if v >= 0 && !math.IsNaN(v) {
			clean = append(clean, v)
		}
	}
	if len(clean) < cfg.MinSamples || len(clean) == 0 {
		return Plateau{}, false
	}
	sorted := append([]float64(nil), clean...)
	sort.Float64s(sorted)
	rank := 0.95 * float64(len(sorted)-1)
	lo, hi := int(math.Floor(rank)), int(math.Ceil(rank))
	peak := sorted[lo] + (sorted[hi]-sorted[lo])*(rank-float64(lo))
	if peak < cfg.MinBps {
		return Plateau{Peak: peak, Window: len(clean)}, false
	}
	floor := peak * (1 - cfg.Tolerance)
	ceil := peak * (1 + cfg.Tolerance)
	var on int
	var sum float64
	for _, v := range clean {
		if v >= floor && v <= ceil {
			on++
			sum += v
		}
	}
	p := Plateau{Peak: peak, OnPlateau: on, Window: len(clean)}
	if on > 0 {
		p.Level = sum / float64(on)
	}
	need := int(math.Ceil(cfg.Share * float64(len(clean))))
	return p, on >= need
}
