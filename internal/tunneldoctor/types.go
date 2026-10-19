// Package tunneldoctor is the Network Doctor: an on-demand, read-only
// diagnosis of one tunnel. It runs a measurement script on both ends over SSH
// (100 pings in each direction over IPv6 and IPv4, TCP connect probes to the
// relay's control port, a path-MTU probe, a hop-by-hop probe, network-card
// drop counters, TCP retransmissions, the HAProxy state), adds statistics from
// the last 24 hours of stored samples and events, and produces a structured
// result plus a Markdown report meant to be handed to the datacenter's
// network team.
//
// The report states only what was measured. Anything that would be a guess
// about a cause is phrased as a question to the network team instead.
package tunneldoctor

import (
	"time"

	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

// Status of a diagnosis job.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Job is the state kept for one tunnel's latest diagnosis.
type Job struct {
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	Result     *Result    `json:"result,omitempty"`
}

// Result is everything one diagnosis found.
type Result struct {
	Tunnel      TunnelInfo  `json:"tunnel"`
	StartedAt   time.Time   `json:"started_at"`
	FinishedAt  time.Time   `json:"finished_at"`
	Environment Environment `json:"environment"`
	// Node and Relay hold what each end measured towards the other.
	Node     EndResult `json:"node"`
	Relay    EndResult `json:"relay"`
	History  History   `json:"history"`
	Findings []Finding `json:"findings"`
}

// TunnelInfo identifies the tunnel.
type TunnelInfo struct {
	ID       int32  `json:"id"`
	Name     string `json:"name"`
	Method   string `json:"method"`
	Protocol string `json:"protocol,omitempty"`
}

// Environment is the table at the top of the report. Nothing secret.
type Environment struct {
	NodeAddress     string  `json:"node_address"`
	RelayAddress    string  `json:"relay_address"`
	NodeV6          string  `json:"node_v6,omitempty"`
	NodeV4          string  `json:"node_v4,omitempty"`
	RelayV6         string  `json:"relay_v6,omitempty"`
	RelayV4         string  `json:"relay_v4,omitempty"`
	ControlPort     int     `json:"control_port,omitempty"`
	NodeKernel      string  `json:"node_kernel,omitempty"`
	RelayKernel     string  `json:"relay_kernel,omitempty"`
	NodeNIC         string  `json:"node_nic,omitempty"`
	NodeNICSpeed    int     `json:"node_nic_speed_mbps,omitempty"`
	RelayNIC        string  `json:"relay_nic,omitempty"`
	RelayNICSpeed   int     `json:"relay_nic_speed_mbps,omitempty"`
	PingCount       int     `json:"ping_count"`
	PingIntervalSec float64 `json:"ping_interval_seconds"`
}

// PingPair is a ping burst over each address family; a family that was not
// measured is nil.
type PingPair struct {
	V6 *netprobe.Ping `json:"v6,omitempty"`
	V4 *netprobe.Ping `json:"v4,omitempty"`
}

// TCPProbe is a series of TCP connects to one address and port.
type TCPProbe struct {
	Family    string  `json:"family"` // "v6" or "v4"
	Address   string  `json:"address"`
	Port      int     `json:"port"`
	Attempts  int     `json:"attempts"`
	Successes int     `json:"successes"`
	MinMs     float64 `json:"min_ms"`
	AvgMs     float64 `json:"avg_ms"`
	MaxMs     float64 `json:"max_ms"`
}

// MTUProbe is one don't-fragment ping of a payload size.
type MTUProbe struct {
	Payload int  `json:"payload"`
	OK      bool `json:"ok"`
}

// MTUResult is the probe of one family: the sizes tried, largest first, and the
// largest that was answered. PathMTU is that payload plus the IP and ICMP
// headers (28 bytes for IPv4, 48 for IPv6); 0 when nothing was answered.
type MTUResult struct {
	Family    string     `json:"family"`
	Probes    []MTUProbe `json:"probes"`
	LargestOK int        `json:"largest_ok_payload"`
	PathMTU   int        `json:"path_mtu"`
}

// Hop is one step of the hop-limited probe: which address answered a probe with
// that hop limit, and how many of the probes were answered.
type Hop struct {
	TTL     int     `json:"ttl"`
	Addr    string  `json:"addr,omitempty"`
	Replies int     `json:"replies"`
	Sent    int     `json:"sent"`
	Reached bool    `json:"reached"` // the destination itself answered
	AvgMs   float64 `json:"avg_ms,omitempty"`
}

// HopPath is the hops of one family.
type HopPath struct {
	Family string `json:"family"`
	Hops   []Hop  `json:"hops"`
}

// NICStats are counter changes on the main network card during the test
// window, plus the cumulative TCP retransmission figures of the whole box.
type NICStats struct {
	Name         string `json:"name"`
	SpeedMbps    int    `json:"speed_mbps,omitempty"`
	RxPackets    int64  `json:"rx_packets"`
	TxPackets    int64  `json:"tx_packets"`
	RxErrors     int64  `json:"rx_errors"`
	RxDropped    int64  `json:"rx_dropped"`
	RxMissed     int64  `json:"rx_missed"`
	TxErrors     int64  `json:"tx_errors"`
	TxDropped    int64  `json:"tx_dropped"`
	TxCarrier    int64  `json:"tx_carrier_errors"`
	QdiscDropped int64  `json:"qdisc_dropped"`
	// Totals since boot of the same counters, so the report can say that a
	// card that showed no drops during the test has some history.
	TotalQdiscDropped int64 `json:"total_qdisc_dropped"`
	TotalTxPackets    int64 `json:"total_tx_packets"`
}

// TCPStats are the box's TCP segment counters: during the test window and since
// boot.
type TCPStats struct {
	OutSegs         int64   `json:"out_segs"`
	RetransSegs     int64   `json:"retrans_segs"`
	RetransPct      float64 `json:"retrans_pct"`
	TotalOutSegs    int64   `json:"total_out_segs"`
	TotalRetrans    int64   `json:"total_retrans_segs"`
	TotalRetransPct float64 `json:"total_retrans_pct"`
}

// EndResult is what one end measured.
type EndResult struct {
	Role      string `json:"role"` // "node" or "relay"
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
	Kernel    string `json:"kernel,omitempty"`
	// Ping is this end pinging the other end.
	Ping    PingPair                     `json:"ping"`
	TCP     []TCPProbe                   `json:"tcp,omitempty"`
	MTU     []MTUResult                  `json:"mtu,omitempty"`
	Hops    []HopPath                    `json:"hops,omitempty"`
	NIC     *NICStats                    `json:"nic,omitempty"`
	TCPStat *TCPStats                    `json:"tcp_stats,omitempty"`
	HAProxy *tunnelmetrics.HAProxyStatus `json:"haproxy,omitempty"`
}

// Finding is one observation, with its text in both report languages. Detail
// is built only from measured numbers.
type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // info, warning, critical
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	TitleFA  string `json:"title_fa"`
	DetailFA string `json:"detail_fa"`
}

// Finding severities.
const (
	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)
