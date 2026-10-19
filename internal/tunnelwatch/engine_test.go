package tunnelwatch

import (
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func at(round int) time.Time { return t0.Add(time.Duration(round) * time.Minute) }

type rowOpts struct {
	relayRx, relayTx float64
	nodeRx, nodeTx   float64
	nicSpeed         int
	ha               *tunnelmetrics.HAProxyStatus
	v6, v4           *netprobe.Ping
	err              string
}

func mkRow(o rowOpts) tunnelmetrics.Row {
	m := tunnelmetrics.Metrics{
		NIC: "eth0", NICSpeedMbps: o.nicSpeed, NICRxBps: o.relayRx, NICTxBps: o.relayTx, Connections: 12, CPUPercent: 4,
		Node: &tunnelmetrics.NodeMetrics{NIC: "eth0", RxBps: o.nodeRx, TxBps: o.nodeTx, HAProxy: o.ha},
	}
	if o.v6 != nil || o.v4 != nil {
		m.Node.Path = &tunnelmetrics.PathMetrics{V6: o.v6, V4: o.v4}
	}
	return tunnelmetrics.Row{TunnelID: 1, Name: "node2", Metrics: m, Error: o.err}
}

func haStatus(primarySessions, backupSessions int64, primary, backup string, primarySince, backupSince int64) *tunnelmetrics.HAProxyStatus {
	ha := &tunnelmetrics.HAProxyStatus{Present: true, Servers: []tunnelmetrics.HAServerStatus{
		{Backend: "frp_relay", Name: "relay_v6", Addr: "[2001:db8:3::fe4]:7000", Status: primary, Sessions: primarySessions, SinceSeconds: primarySince},
		{Backend: "frp_relay", Name: "relay_v4", Addr: "203.0.113.82:7000", Status: backup, Backup: true, Sessions: backupSessions, SinceSeconds: backupSince},
	}}
	switch {
	case primarySessions > backupSessions:
		ha.Active = "relay_v6"
	case backupSessions > 0:
		ha.Active = "relay_v4"
	}
	return ha
}

func kinds(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func TestSampleCarriesEveryMeasurement(t *testing.T) {
	e := NewEngine(Config{})
	row := mkRow(rowOpts{
		relayRx: 40e6, relayTx: 90e6, nodeRx: 41e6, nodeTx: 89e6,
		ha: haStatus(14, 0, "UP", "UP", 3600, 3600),
		v6: &netprobe.Ping{Sent: 10, Received: 10, AvgMs: 90.1, JitterMs: 0.2},
		v4: &netprobe.Ping{Sent: 10, Received: 0, LossPct: 100},
	})
	s, evs := e.Observe(row, at(0))
	if len(evs) != 0 {
		t.Fatalf("a first healthy round gives no events: %v", kinds(evs))
	}
	if *s.RelayRxBps != 40e6 || *s.NodeTxBps != 89e6 || *s.Connections != 12 || *s.CPU != 4 {
		t.Errorf("sample = %+v", s)
	}
	if s.V6 == nil || s.V6.RTT == nil || *s.V6.RTT != 90.1 || s.V6.Loss != 0 {
		t.Errorf("v6 = %+v", s.V6)
	}
	if s.V4 == nil || s.V4.RTT != nil || s.V4.Loss != 100 {
		t.Errorf("a family with no answers keeps its loss but has no rtt: %+v", s.V4)
	}
	if s.HAActive == nil || *s.HAActive != "relay_v6" || !*s.HAPrimaryUp || !*s.HABackupUp {
		t.Errorf("ha = %v %v %v", s.HAActive, s.HAPrimaryUp, s.HABackupUp)
	}
}

func TestUnreachableRelayGivesAnEmptySampleAndKeepsState(t *testing.T) {
	e := NewEngine(Config{})
	s, evs := e.Observe(mkRow(rowOpts{err: "dial tcp: i/o timeout"}), at(0))
	if len(evs) != 0 || s.RelayRxBps != nil || s.Connections != nil || s.HAActive != nil {
		t.Fatalf("sample = %+v events = %v", s, evs)
	}
}

func TestFailoverAndFailbackAreDetectedFromWhereTheSessionsAre(t *testing.T) {
	e := NewEngine(Config{})
	rounds := []*tunnelmetrics.HAProxyStatus{
		haStatus(14, 0, "UP", "UP", 5000, 5000),
		haStatus(14, 0, "UP", "UP", 5060, 5060),
		haStatus(0, 14, "DOWN", "UP", 12, 5120), // primary dropped, sessions on the backup
		haStatus(0, 14, "DOWN", "UP", 72, 5180), // still failed over: nothing new
		haStatus(14, 0, "UP", "UP", 20, 5240),   // primary back and carrying again
		haStatus(14, 0, "UP", "UP", 80, 5300),
	}
	var got [][]string
	var alerts []bool
	for i, ha := range rounds {
		_, evs := e.Observe(mkRow(rowOpts{ha: ha}), at(i))
		got = append(got, kinds(evs))
		for _, ev := range evs {
			alerts = append(alerts, ev.Alert)
		}
	}
	want := [][]string{nil, nil, {KindFailover}, nil, {KindFailback}, nil}
	for i := range want {
		if strings.Join(got[i], ",") != strings.Join(want[i], ",") {
			t.Errorf("round %d: events %v, want %v", i, got[i], want[i])
		}
	}
	if len(alerts) != 2 || !alerts[0] || !alerts[1] {
		t.Errorf("both transitions should alert: %v", alerts)
	}
}

func TestFailoverMentionsTheDataItWasBuiltFrom(t *testing.T) {
	e := NewEngine(Config{})
	e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 100, 100)}), at(0))
	_, evs := e.Observe(mkRow(rowOpts{ha: haStatus(0, 9, "DOWN", "UP", 30, 130)}), at(1))
	if len(evs) != 1 || evs[0].Severity != SeverityWarning {
		t.Fatalf("events = %+v", evs)
	}
	for _, want := range []string{"backup", "relay_v4", "203.0.113.82", "relay_v6 DOWN"} {
		if !strings.Contains(evs[0].Detail, want) {
			t.Errorf("detail %q lacks %q", evs[0].Detail, want)
		}
	}
}

func TestMixedSessionsDuringAFailoverAreNotAFlap(t *testing.T) {
	e := NewEngine(Config{})
	e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 100, 100)}), at(0))
	// half of the pool reconnected: neither role clearly carries the traffic
	_, evs := e.Observe(mkRow(rowOpts{ha: haStatus(7, 7, "DOWN", "UP", 5, 105)}), at(1))
	if len(evs) != 0 {
		t.Fatalf("mixed state must not decide anything, got %v", kinds(evs))
	}
	_, evs = e.Observe(mkRow(rowOpts{ha: haStatus(0, 14, "DOWN", "UP", 65, 165)}), at(2))
	if len(evs) != 1 || evs[0].Kind != KindFailover {
		t.Fatalf("settled on the backup: %v", kinds(evs))
	}
}

func TestABlipBetweenTwoRoundsIsDetectedFromTheServersStatusAge(t *testing.T) {
	e := NewEngine(Config{})
	e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 4000, 4000)}), at(0))
	// One minute later the primary is UP and the sessions are on it, exactly as
	// before - but it has only been UP for 20 s, so it was down in between.
	_, evs := e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 20, 4060)}), at(1))
	if strings.Join(kinds(evs), ",") != "failover,failback" {
		t.Fatalf("events = %v", kinds(evs))
	}
	if !evs[0].At.Before(evs[1].At) || !evs[0].At.Equal(at(1).Add(-20*time.Second)) {
		t.Errorf("the blip is dated by the status age: %v / %v", evs[0].At, evs[1].At)
	}
	if !strings.Contains(evs[0].Detail, "between two checks") {
		t.Errorf("detail must say the moment is not exact: %s", evs[0].Detail)
	}
	// An ordinary round afterwards says nothing.
	if _, evs := e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 80, 4120)}), at(2)); len(evs) != 0 {
		t.Errorf("no repeat: %v", kinds(evs))
	}
}

func TestNoBlipWhenTheStatusAgeIsSimplyOld(t *testing.T) {
	e := NewEngine(Config{})
	e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 462, 2166)}), at(0))
	_, evs := e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 522, 2226)}), at(1))
	if len(evs) != 0 {
		t.Fatalf("events = %v", kinds(evs))
	}
	// a check hiccup (status text) is still "up"
	_, evs = e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP 1/2", "UP", 582, 2286)}), at(2))
	if len(evs) != 0 {
		t.Fatalf("UP 1/2 is still up: %v", kinds(evs))
	}
}

func TestFirstSightingAfterARestartNeverFiresATransition(t *testing.T) {
	e := NewEngine(Config{})
	// The very first round already shows the backup carrying traffic: that is
	// the state we start from, not a failover.
	_, evs := e.Observe(mkRow(rowOpts{ha: haStatus(0, 14, "DOWN", "UP", 900, 900)}), at(0))
	if len(evs) != 0 {
		t.Fatalf("events = %v", kinds(evs))
	}
}

func TestFailoverAlertsAreRateLimitedButEveryEventIsRecorded(t *testing.T) {
	e := NewEngine(Config{})
	up := haStatus(14, 0, "UP", "UP", 100, 100)
	down := haStatus(0, 14, "DOWN", "UP", 10, 100)
	e.Observe(mkRow(rowOpts{ha: up}), at(0))
	var recorded, alerted int
	for i := 1; i <= 6; i++ { // flapping every minute
		ha := down
		if i%2 == 0 {
			ha = up
		}
		_, evs := e.Observe(mkRow(rowOpts{ha: ha}), at(i))
		for _, ev := range evs {
			recorded++
			if ev.Alert {
				alerted++
			}
		}
	}
	if recorded != 6 {
		t.Errorf("recorded = %d, want 6", recorded)
	}
	if alerted != 2 {
		t.Errorf("alerted = %d, want one failover and one failback inside the 5 minute limit", alerted)
	}
}

func TestSeedingStopsARestartFromRepeatingARecentAlert(t *testing.T) {
	e := NewEngine(Config{})
	if e.Seeded(1) {
		t.Fatal("unknown tunnel")
	}
	e.Seed(1, map[string]time.Time{KindFailover: at(0)})
	if !e.Seeded(1) {
		t.Fatal("seed must register the tunnel")
	}
	e.Observe(mkRow(rowOpts{ha: haStatus(14, 0, "UP", "UP", 100, 100)}), at(1))
	_, evs := e.Observe(mkRow(rowOpts{ha: haStatus(0, 14, "DOWN", "UP", 10, 170)}), at(2))
	if len(evs) != 1 || evs[0].Alert {
		t.Fatalf("recorded but not re-alerted: %+v", evs)
	}
}

func ping(sent, got int, loss, avg float64) *netprobe.Ping {
	return &netprobe.Ping{Sent: sent, Received: got, LossPct: loss, AvgMs: avg}
}

func TestLossHighNeedsTwoRoundsAndFiresOncePerEpisode(t *testing.T) {
	e := NewEngine(Config{})
	lossy := ping(10, 8, 20, 91)
	good := ping(10, 10, 0, 90)
	var fired []int
	seq := []*netprobe.Ping{good, lossy, good, lossy, lossy, lossy, lossy}
	for i, p := range seq {
		_, evs := e.Observe(mkRow(rowOpts{v6: p}), at(i))
		for _, ev := range evs {
			if ev.Kind == KindLossHigh {
				fired = append(fired, i)
				if !strings.Contains(ev.Detail, "IPv6") || !strings.Contains(ev.Detail, "20%") || !strings.Contains(ev.Detail, "8 of 10") {
					t.Errorf("detail = %s", ev.Detail)
				}
			}
		}
	}
	if len(fired) != 1 || fired[0] != 4 {
		t.Fatalf("fired in rounds %v, want only round 4 (second consecutive lossy round after the isolated one)", fired)
	}
}

func TestLossHighIsRateLimitedAcrossEpisodes(t *testing.T) {
	e := NewEngine(Config{})
	lossy, good := ping(10, 8, 20, 91), ping(10, 10, 0, 90)
	count := 0
	seq := []*netprobe.Ping{lossy, lossy, good, good, lossy, lossy, good, good}
	for i, p := range seq {
		_, evs := e.Observe(mkRow(rowOpts{v4: p}), at(i))
		for _, ev := range evs {
			if ev.Kind == KindLossHigh {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("a second episode inside the record window must not be re-recorded, got %d", count)
	}
}

func TestLossBelowThresholdNeverFires(t *testing.T) {
	e := NewEngine(Config{})
	for i := 0; i < 10; i++ {
		_, evs := e.Observe(mkRow(rowOpts{v6: ping(100, 99, 1, 90)}), at(i))
		if len(evs) != 0 {
			t.Fatalf("1%% loss is not high: %v", kinds(evs))
		}
	}
}

func TestNICSaturation(t *testing.T) {
	e := NewEngine(Config{})
	var got []Event
	for i := 0; i < 4; i++ {
		_, evs := e.Observe(mkRow(rowOpts{nicSpeed: 1000, relayTx: 950e6, relayRx: 20e6}), at(i))
		got = append(got, evs...)
	}
	if len(got) != 1 || got[0].Kind != KindNICSaturated || !strings.Contains(got[0].Detail, "95%") || !strings.Contains(got[0].Detail, "outbound") {
		t.Fatalf("events = %+v", got)
	}
	// An unknown speed can never be saturated.
	e2 := NewEngine(Config{})
	for i := 0; i < 4; i++ {
		if _, evs := e2.Observe(mkRow(rowOpts{nicSpeed: 0, relayTx: 5e9}), at(i)); len(evs) != 0 {
			t.Fatalf("no speed, no verdict: %v", kinds(evs))
		}
	}
}

func TestCeilingFiresOnAFlatTopAndIsQuietOtherwise(t *testing.T) {
	e := NewEngine(Config{})
	rng := rand.New(rand.NewSource(7))
	var fired []int
	for i := 0; i < 40; i++ {
		tx := 300e6 * (1 + (rng.Float64()-0.5)*0.02) // pinned at ~300 Mbit/s, 1% noise
		if i < 6 {
			tx = (50 + float64(i)*40) * 1e6 // ramping up first
		}
		_, evs := e.Observe(mkRow(rowOpts{relayTx: tx, relayRx: 30e6, nodeTx: 20e6, nodeRx: 20e6}), at(i))
		for _, ev := range evs {
			if ev.Kind == KindCeiling {
				fired = append(fired, i)
				if !strings.Contains(ev.Detail, "relay network card, outbound") || !strings.Contains(ev.Detail, "stays flat near") {
					t.Errorf("detail = %s", ev.Detail)
				}
			}
		}
	}
	if len(fired) != 1 {
		t.Fatalf("ceiling fired in rounds %v, want exactly once", fired)
	}
	if fired[0] < 14 || fired[0] > 30 {
		t.Errorf("fired at round %d, expected after enough plateau samples accumulated", fired[0])
	}
}

func TestPlateauHeuristic(t *testing.T) {
	cfg := DefaultPlateau
	rng := rand.New(rand.NewSource(1))
	gen := func(n int, f func(i int) float64) []float64 {
		v := make([]float64, n)
		for i := range v {
			v[i] = f(i)
		}
		return v
	}
	cases := []struct {
		name string
		v    []float64
		want bool
	}{
		{"flat at 300M with 1% noise", gen(20, func(i int) float64 { return 300e6 * (1 + (rng.Float64()-0.5)*0.02) }), true},
		{"flat plateau with a spike above it", gen(20, func(i int) float64 {
			if i == 7 {
				return 340e6
			}
			return 300e6
		}), true},
		{"flat plateau with three dips", gen(20, func(i int) float64 {
			if i%7 == 0 {
				return 120e6
			}
			return 500e6
		}), true},
		{"ordinary noisy traffic at 200M (+-25%)", gen(20, func(i int) float64 { return 200e6 * (1 + (rng.Float64()-0.5)*0.5) }), false},
		{"steady climb", gen(20, func(i int) float64 { return (100 + float64(i)*20) * 1e6 }), false},
		{"flat but only 5 Mbit/s", gen(20, func(i int) float64 { return 5e6 }), false},
		{"flat at 99 Mbit/s is below the floor", gen(20, func(i int) float64 { return 99e6 }), false},
		{"only 10 samples so far", gen(10, func(i int) float64 { return 300e6 }), false},
		{"half plateau half idle", gen(20, func(i int) float64 {
			if i < 10 {
				return 300e6
			}
			return 10e6
		}), false},
		{"unmeasured samples are skipped", append(gen(5, func(i int) float64 { return -1 }), gen(15, func(i int) float64 { return 300e6 })...), true},
	}
	for _, c := range cases {
		p, got := DetectPlateau(c.v, cfg)
		if got != c.want {
			t.Errorf("%s: got %v want %v (%+v)", c.name, got, c.want, p)
		}
	}
	p, ok := DetectPlateau(gen(20, func(i int) float64 { return 250e6 }), cfg)
	if !ok || math.Abs(p.Level-250e6) > 1 || p.OnPlateau != 20 || p.Window != 20 {
		t.Errorf("plateau stats = %+v", p)
	}
}

func TestTransitionDeduplicatesAndRecordsTheDuration(t *testing.T) {
	e := NewEngine(Config{})
	ev, ok := e.Transition(1, false, "dial tcp: i/o timeout", 0, at(0))
	if !ok || ev.Kind != KindTunnelDown || ev.Severity != SeverityCritical || !strings.Contains(ev.Detail, "i/o timeout") || ev.Alert {
		t.Fatalf("down = %+v ok=%v", ev, ok)
	}
	if _, ok := e.Transition(1, false, "again", 0, at(1)); ok {
		t.Error("a second down in a row is not an event")
	}
	ev, ok = e.Transition(1, true, "", 5*time.Minute, at(5))
	if !ok || ev.Kind != KindTunnelUp || ev.DurationSeconds == nil || *ev.DurationSeconds != 300 {
		t.Fatalf("up = %+v ok=%v", ev, ok)
	}
	if _, ok := e.Transition(1, true, "", 0, at(6)); ok {
		t.Error("a second up in a row is not an event")
	}
	if _, ok := e.Transition(1, false, "", 0, at(9)); !ok {
		t.Error("a new outage after recovery is an event")
	}
}

func TestTransitionAfterARestartMidOutageIsNotDuplicated(t *testing.T) {
	e := NewEngine(Config{})
	e.Seed(1, map[string]time.Time{KindTunnelDown: at(0)})
	if _, ok := e.Transition(1, false, "", 0, at(10)); ok {
		t.Fatal("the outage is already on the timeline")
	}
	if ev, ok := e.Transition(1, true, "", 12*time.Minute, at(12)); !ok || ev.Kind != KindTunnelUp {
		t.Fatalf("recovery still recorded: %+v %v", ev, ok)
	}
}
