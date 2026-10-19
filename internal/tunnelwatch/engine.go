// Package tunnelwatch turns the per-round measurements internal/tunnelmetrics
// collects into history and events: one stored sample per tunnel per round,
// and a small set of events worth showing on a timeline or sending as an alert
// - the HAProxy failover moving traffic to the backup relay (and back),
// sustained packet loss on the path, traffic pinned flat at a ceiling, a
// network card running at its limit, and a tunnel going down or coming back.
//
// The decision logic lives in Engine, which has no clock, database or network
// of its own, so every rule is covered by plain unit tests. Watcher is the
// thin layer that feeds it from the collector and writes the results.
package tunnelwatch

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

// Event kinds.
const (
	KindTunnelDown   = "tunnel_down"
	KindTunnelUp     = "tunnel_up"
	KindFailover     = "failover"
	KindFailback     = "failback"
	KindLossHigh     = "loss_high"
	KindCeiling      = "ceiling"
	KindNICSaturated = "nic_saturated"
)

// Severities.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Event is something that happened to a tunnel.
type Event struct {
	ID              int64     `json:"id,omitempty"`
	TunnelID        int32     `json:"tunnel_id"`
	At              time.Time `json:"at"`
	Kind            string    `json:"kind"`
	Severity        string    `json:"severity"`
	Detail          string    `json:"detail"`
	DurationSeconds *int32    `json:"duration_seconds,omitempty"`
	// Alert is the engine's decision that this event should also be sent to
	// the notification channels (it passed the rate limit). Not stored.
	Alert bool `json:"-"`
}

// PathPoint is the stored form of a ping burst over one address family.
type PathPoint struct {
	// Loss is in percent; RTT and Jitter are milliseconds and nil when every
	// probe was lost.
	Loss   float64
	RTT    *float64
	Jitter *float64
}

// Sample is one round of one tunnel. Every pointer is nil when that thing
// could not be measured in the round (the box was unreachable, there was no
// address of that family, no HAProxy) - so a chart shows a gap, not a zero.
type Sample struct {
	TunnelID    int32
	At          time.Time
	RelayRxBps  *float64
	RelayTxBps  *float64
	NodeRxBps   *float64
	NodeTxBps   *float64
	Connections *int32
	CPU         *float64
	V6, V4      *PathPoint
	HAActive    *string
	HAPrimaryUp *bool
	HABackupUp  *bool
}

// Rule is how often one kind of event may repeat for a tunnel: Record limits
// what is written to the timeline, Alert what is also sent to people. Alert is
// never shorter than Record in effect (an unrecorded event is not alerted).
type Rule struct {
	Record time.Duration
	Alert  time.Duration
}

// Config is the engine's tunables.
type Config struct {
	Plateau PlateauConfig
	// LossPct is the packet-loss percentage that counts as high, and
	// LossRounds how many consecutive rounds must show it.
	LossPct    float64
	LossRounds int
	// NICUtilPct is the utilisation of a NIC with a known speed above which it
	// is saturated, for NICRounds consecutive rounds.
	NICUtilPct float64
	NICRounds  int
	// CeilingClearRounds is how many rounds without a plateau end an episode.
	CeilingClearRounds int
	// BlipTolerance is slack, in seconds, when deciding that a server changed
	// state between two rounds (the HAProxy clock and the collector's differ).
	BlipTolerance int64
	Rules         map[string]Rule
}

// DefaultConfig returns the production settings.
func DefaultConfig() Config {
	return Config{
		Plateau:    DefaultPlateau,
		LossPct:    5,
		LossRounds: 2,
		NICUtilPct: 90,
		NICRounds:  2,

		CeilingClearRounds: 3,
		BlipTolerance:      10,
		Rules: map[string]Rule{
			KindFailover:     {Record: 0, Alert: 5 * time.Minute},
			KindFailback:     {Record: 0, Alert: 5 * time.Minute},
			KindLossHigh:     {Record: 15 * time.Minute, Alert: 30 * time.Minute},
			KindCeiling:      {Record: 30 * time.Minute, Alert: 2 * time.Hour},
			KindNICSaturated: {Record: 30 * time.Minute, Alert: time.Hour},
			KindTunnelDown:   {},
			KindTunnelUp:     {},
		},
	}
}

// seriesNames are the throughput series the plateau check examines, in the
// order ties are resolved.
var seriesNames = []string{"relay_tx", "relay_rx", "node_tx", "node_rx"}

var seriesLabel = map[string]string{
	"relay_tx": "relay network card, outbound",
	"relay_rx": "relay network card, inbound",
	"node_tx":  "node network card, outbound",
	"node_rx":  "node network card, inbound",
}

type serverPrev struct {
	up    bool
	since int64
}

type tunnelState struct {
	rounds int
	lastAt time.Time

	lastEvent map[string]time.Time // by kind: when it was last recorded
	lastAlert map[string]time.Time

	haRole    string // "primary", "backup" or "" (not known yet)
	haServers map[string]serverPrev

	lossStreak map[string]int
	lossClear  map[string]int
	lossActive map[string]bool

	series        map[string][]float64
	ceilingActive map[string]bool
	ceilingClear  map[string]int

	nicStreak map[string]int
	nicActive map[string]bool
}

func newTunnelState() *tunnelState {
	return &tunnelState{
		lastEvent: map[string]time.Time{}, lastAlert: map[string]time.Time{},
		haServers:  map[string]serverPrev{},
		lossStreak: map[string]int{}, lossClear: map[string]int{}, lossActive: map[string]bool{},
		series:        map[string][]float64{},
		ceilingActive: map[string]bool{}, ceilingClear: map[string]int{},
		nicStreak: map[string]int{}, nicActive: map[string]bool{},
	}
}

// Engine holds the per-tunnel memory the rules need. Safe for concurrent use.
type Engine struct {
	cfg Config
	mu  sync.Mutex
	st  map[int32]*tunnelState
}

// NewEngine builds an engine; a zero Config means DefaultConfig.
func NewEngine(cfg Config) *Engine {
	if cfg.Rules == nil {
		cfg = DefaultConfig()
	}
	return &Engine{cfg: cfg, st: map[int32]*tunnelState{}}
}

func (e *Engine) state(id int32) *tunnelState {
	s := e.st[id]
	if s == nil {
		s = newTunnelState()
		e.st[id] = s
	}
	return s
}

// Seeded reports whether the engine has any memory of the tunnel yet. The
// watcher uses it to load recent event times from the database once, so a
// restart does not repeat an alert that was just sent.
func (e *Engine) Seeded(tunnelID int32) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.st[tunnelID]
	return ok
}

// Seed records when each kind of event last happened for the tunnel.
func (e *Engine) Seed(tunnelID int32, last map[string]time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.state(tunnelID)
	for kind, at := range last {
		s.lastEvent[kind] = at
		s.lastAlert[kind] = at
	}
}

// Forget drops the memory of a tunnel that no longer exists.
func (e *Engine) Forget(tunnelID int32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.st, tunnelID)
}

// emit applies the per-kind rate limit and returns the event to record, or
// ok=false when the same kind was recorded too recently.
func (e *Engine) emit(s *tunnelState, tunnelID int32, now time.Time, kind, severity, detail string) (Event, bool) {
	rule := e.cfg.Rules[kind]
	if last, ok := s.lastEvent[kind]; ok && rule.Record > 0 && now.Sub(last) < rule.Record {
		return Event{}, false
	}
	s.lastEvent[kind] = now
	ev := Event{TunnelID: tunnelID, At: now, Kind: kind, Severity: severity, Detail: detail}
	if s.rounds > 0 && rule.Alert > 0 {
		if last, ok := s.lastAlert[kind]; !ok || now.Sub(last) >= rule.Alert {
			ev.Alert = true
			s.lastAlert[kind] = now
		}
	}
	return ev, true
}

// Observe digests one collection round of one tunnel: it returns the sample to
// store and the events the round gave rise to. A row with an error (the relay
// could not be reached) yields an all-empty sample and changes no rule state:
// an unreachable box is not evidence of anything but that.
func (e *Engine) Observe(row tunnelmetrics.Row, now time.Time) (Sample, []Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.state(row.TunnelID)
	sample := Sample{TunnelID: row.TunnelID, At: now}
	if row.Error != "" {
		return sample, nil
	}
	m := row.Metrics
	sample.RelayRxBps, sample.RelayTxBps = fptr(m.NICRxBps), fptr(m.NICTxBps)
	sample.Connections = i32ptr(int32(m.Connections))
	sample.CPU = fptr(m.CPUPercent)

	var events []Event
	add := func(ev Event, ok bool) {
		if ok {
			events = append(events, ev)
		}
	}

	values := map[string]float64{"relay_tx": m.NICTxBps, "relay_rx": m.NICRxBps}
	if n := m.Node; n != nil && n.Error == "" {
		sample.NodeRxBps, sample.NodeTxBps = fptr(n.RxBps), fptr(n.TxBps)
		values["node_tx"], values["node_rx"] = n.TxBps, n.RxBps
		if n.Path != nil {
			if n.Path.V6 != nil {
				sample.V6 = pathPoint(n.Path.V6.LossPct, n.Path.V6.Received, n.Path.V6.AvgMs, n.Path.V6.JitterMs)
			}
			if n.Path.V4 != nil {
				sample.V4 = pathPoint(n.Path.V4.LossPct, n.Path.V4.Received, n.Path.V4.AvgMs, n.Path.V4.JitterMs)
			}
		}
		if ha := n.HAProxy; ha != nil && ha.Present && len(ha.Servers) > 0 {
			active, primaryUp, backupUp := haSummary(ha)
			sample.HAActive = &active
			sample.HAPrimaryUp = primaryUp
			sample.HABackupUp = backupUp
			for _, ev := range e.haEvents(s, row.TunnelID, now, ha) {
				add(ev, true)
			}
		}
		for _, ev := range e.lossEvents(s, row.TunnelID, now, n.Path) {
			add(ev, true)
		}
	}
	for _, ev := range e.ceilingEvents(s, row.TunnelID, now, values) {
		add(ev, true)
	}
	for _, ev := range e.nicEvents(s, row.TunnelID, now, m) {
		add(ev, true)
	}

	s.rounds++
	s.lastAt = now
	return sample, events
}

func fptr(v float64) *float64 { return &v }
func i32ptr(v int32) *int32   { return &v }

func pathPoint(loss float64, received int, avg, jitter float64) *PathPoint {
	p := &PathPoint{Loss: loss}
	if received > 0 {
		p.RTT, p.Jitter = fptr(avg), fptr(jitter)
	}
	return p
}

func isUp(status string) bool { return strings.HasPrefix(strings.ToUpper(status), "UP") }

// haSummary names the server carrying the sessions and says whether the
// primary and the backup servers are up (nil when the tunnel has no server of
// that role).
func haSummary(ha *tunnelmetrics.HAProxyStatus) (active string, primaryUp, backupUp *bool) {
	active = ha.Active
	var hasP, hasB, pUp, bUp bool
	for _, sv := range ha.Servers {
		if sv.Backup {
			hasB = true
			bUp = bUp || isUp(sv.Status)
		} else {
			hasP = true
			pUp = pUp || isUp(sv.Status)
		}
	}
	if hasP {
		primaryUp = &pUp
	}
	if hasB {
		backupUp = &bUp
	}
	return active, primaryUp, backupUp
}

// haRole says which role carries the traffic: a role wins outright when the
// other has no sessions, or has at least four times fewer; otherwise (both
// busy, or nothing connected) the answer is unknown and the previous role
// stands.
func haRole(ha *tunnelmetrics.HAProxyStatus) string {
	var p, b int64
	for _, sv := range ha.Servers {
		if sv.Backup {
			b += sv.Sessions
		} else {
			p += sv.Sessions
		}
	}
	switch {
	case p > 0 && b == 0:
		return "primary"
	case b > 0 && p == 0:
		return "backup"
	case p >= 4*b && p > 0:
		return "primary"
	case b >= 4*p && b > 0:
		return "backup"
	}
	return ""
}

func (e *Engine) haEvents(s *tunnelState, id int32, now time.Time, ha *tunnelmetrics.HAProxyStatus) []Event {
	var out []Event
	moved := false
	if role := haRole(ha); role != "" {
		if s.haRole != "" && role != s.haRole && s.rounds > 0 {
			moved = true
			if role == "backup" {
				if ev, ok := e.emit(s, id, now, KindFailover, SeverityWarning, "Traffic moved from the primary relay path to the backup ("+serverNames(ha, true)+"); the primary server is "+roleStatus(ha, false)+"."); ok {
					out = append(out, ev)
				}
			} else {
				if ev, ok := e.emit(s, id, now, KindFailback, SeverityInfo, "Traffic moved back from the backup relay path to the primary ("+serverNames(ha, false)+")."); ok {
					out = append(out, ev)
				}
			}
		}
		s.haRole = role
	}

	// A primary that dropped and came back between two rounds leaves no trace
	// in the sessions (they may be back before the next look) but its status
	// age betrays it: same state as last round, yet changed after that round.
	elapsed := int64(now.Sub(s.lastAt) / time.Second)
	for _, sv := range ha.Servers {
		key := sv.Backend + "/" + sv.Name
		cur := serverPrev{up: isUp(sv.Status), since: sv.SinceSeconds}
		prev, known := s.haServers[key]
		s.haServers[key] = cur
		if !known || s.rounds == 0 || s.lastAt.IsZero() || sv.Backup || moved {
			continue
		}
		if prev.up == cur.up && cur.since+e.cfg.BlipTolerance < elapsed {
			at := now.Add(-time.Duration(cur.since) * time.Second)
			state := "up"
			if !cur.up {
				state = "down"
			}
			detail := fmt.Sprintf("Primary relay server %s changed state and returned to %s %d s ago, between two checks (%d s apart); the exact moment is not known.", sv.Name, state, cur.since, elapsed)
			if cur.up {
				// It went down and came back: traffic may have been moved to
				// the backup for a moment.
				if ev, ok := e.emit(s, id, at, KindFailover, SeverityWarning, "Brief failover: "+detail); ok {
					out = append(out, ev)
				}
				if ev, ok := e.emit(s, id, now, KindFailback, SeverityInfo, "Traffic is on the primary again after a brief failover: "+detail); ok {
					out = append(out, ev)
				}
			}
		}
	}
	return out
}

func serverNames(ha *tunnelmetrics.HAProxyStatus, backup bool) string {
	var names []string
	for _, sv := range ha.Servers {
		if sv.Backup == backup && sv.Sessions > 0 {
			label := sv.Name
			if sv.Addr != "" {
				label += " " + sv.Addr
			}
			names = append(names, label)
		}
	}
	if len(names) == 0 {
		return "no sessions"
	}
	return strings.Join(names, ", ")
}

func roleStatus(ha *tunnelmetrics.HAProxyStatus, backup bool) string {
	var parts []string
	for _, sv := range ha.Servers {
		if sv.Backup == backup {
			parts = append(parts, sv.Name+" "+sv.Status)
		}
	}
	return strings.Join(parts, ", ")
}

func (e *Engine) lossEvents(s *tunnelState, id int32, now time.Time, path *tunnelmetrics.PathMetrics) []Event {
	if path == nil {
		return nil
	}
	var out []Event
	check := func(key, label string, p *netprobe.Ping) {
		if p.Sent == 0 {
			return
		}
		loss, rtt, received, sent := p.LossPct, p.AvgMs, p.Received, p.Sent
		if loss >= e.cfg.LossPct {
			s.lossStreak[key]++
			s.lossClear[key] = 0
		} else {
			s.lossStreak[key] = 0
			s.lossClear[key]++
			if s.lossClear[key] >= e.cfg.LossRounds {
				s.lossActive[key] = false
			}
		}
		if s.lossStreak[key] >= e.cfg.LossRounds && !s.lossActive[key] {
			detail := fmt.Sprintf("%s path from the node to the relay lost %.0f%% of its probes (%d of %d answered) in each of the last %d checks", label, loss, received, sent, s.lossStreak[key])
			if received > 0 {
				detail += fmt.Sprintf("; average round trip %.1f ms", rtt)
			}
			detail += "."
			if ev, emitted := e.emit(s, id, now, KindLossHigh, SeverityWarning, detail); emitted {
				s.lossActive[key] = true
				out = append(out, ev)
			}
		}
	}
	if path.V6 != nil {
		check("v6", "IPv6", path.V6)
	}
	if path.V4 != nil {
		check("v4", "IPv4", path.V4)
	}
	return out
}

func (e *Engine) ceilingEvents(s *tunnelState, id int32, now time.Time, values map[string]float64) []Event {
	window := e.cfg.Plateau.Window
	for name, v := range values {
		hist := append(s.series[name], v)
		if len(hist) > window {
			hist = hist[len(hist)-window:]
		}
		s.series[name] = hist
	}
	var best string
	var bestP Plateau
	for _, name := range seriesNames {
		if _, ok := values[name]; !ok {
			continue
		}
		p, hit := DetectPlateau(s.series[name], e.cfg.Plateau)
		if !hit {
			s.ceilingClear[name]++
			if s.ceilingClear[name] >= e.cfg.CeilingClearRounds {
				s.ceilingActive[name] = false
			}
			continue
		}
		s.ceilingClear[name] = 0
		if s.ceilingActive[name] {
			continue
		}
		if best == "" || p.Peak > bestP.Peak {
			best, bestP = name, p
		}
	}
	if best == "" {
		return nil
	}
	detail := fmt.Sprintf("Traffic on the %s stays flat near %s: %d of the last %d checks are within %.0f%% of the %s peak.",
		seriesLabel[best], mbit(bestP.Level), bestP.OnPlateau, bestP.Window, e.cfg.Plateau.Tolerance*100, mbit(bestP.Peak))
	ev, ok := e.emit(s, id, now, KindCeiling, SeverityWarning, detail)
	if !ok {
		return nil
	}
	s.ceilingActive[best] = true
	return []Event{ev}
}

func mbit(bps float64) string { return fmt.Sprintf("%.0f Mbit/s", bps/1e6) }

func (e *Engine) nicEvents(s *tunnelState, id int32, now time.Time, m tunnelmetrics.Metrics) []Event {
	type nic struct {
		key, label, name string
		speed            int
		rx, tx           float64
		ok               bool
	}
	nics := []nic{{"relay", "relay", m.NIC, m.NICSpeedMbps, m.NICRxBps, m.NICTxBps, true}}
	if n := m.Node; n != nil && n.Error == "" {
		nics = append(nics, nic{"node", "node", n.NIC, n.NICSpeedMbps, n.RxBps, n.TxBps, true})
	}
	var out []Event
	for _, c := range nics {
		if c.speed <= 0 {
			continue
		}
		linkBps := float64(c.speed) * 1e6
		util := 100 * maxF(c.rx, c.tx) / linkBps
		if util > e.cfg.NICUtilPct {
			s.nicStreak[c.key]++
		} else {
			s.nicStreak[c.key] = 0
			s.nicActive[c.key] = false
		}
		if s.nicStreak[c.key] >= e.cfg.NICRounds && !s.nicActive[c.key] {
			dir, rate := "inbound", c.rx
			if c.tx > c.rx {
				dir, rate = "outbound", c.tx
			}
			detail := fmt.Sprintf("The %s network card %s is at %.0f%% of its %d Mbit/s link (%s %s).", c.label, c.name, util, c.speed, dir, mbit(rate))
			if ev, ok := e.emit(s, id, now, KindNICSaturated, SeverityWarning, detail); ok {
				s.nicActive[c.key] = true
				out = append(out, ev)
			}
		}
	}
	return out
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// Transition turns a relay-health up/down change into an event. It records
// nothing twice in a row (a down while a down is already the latest, an up
// right after an up), which is what a backend restart in the middle of an
// outage would otherwise do. The returned event is never marked Alert: the
// relay-health monitor already sends its own message for the same change.
func (e *Engine) Transition(tunnelID int32, up bool, detail string, downFor time.Duration, now time.Time) (Event, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.state(tunnelID)
	lastDown, hasDown := s.lastEvent[KindTunnelDown]
	lastUp, hasUp := s.lastEvent[KindTunnelUp]
	if up {
		if hasUp && (!hasDown || lastUp.After(lastDown)) {
			return Event{}, false
		}
		s.lastEvent[KindTunnelUp] = now
		ev := Event{TunnelID: tunnelID, At: now, Kind: KindTunnelUp, Severity: SeverityInfo, Detail: "The relay answers again."}
		if downFor > 0 {
			secs := int32(downFor.Round(time.Second) / time.Second)
			ev.DurationSeconds = &secs
			ev.Detail = fmt.Sprintf("The relay answers again after %s down.", downFor.Round(time.Second))
		}
		return ev, true
	}
	if hasDown && (!hasUp || lastDown.After(lastUp)) {
		return Event{}, false
	}
	s.lastEvent[KindTunnelDown] = now
	msg := "The relay stopped answering."
	if detail != "" {
		msg = "The relay stopped answering: " + detail
	}
	return Event{TunnelID: tunnelID, At: now, Kind: KindTunnelDown, Severity: SeverityCritical, Detail: msg}, true
}
