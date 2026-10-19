// Package tunnelmetrics periodically SSHes into a tunnel's relay box (reusing
// the same stored credentials internal/tunnelprovision already provisioned
// it with - no new secret, no agent to deploy) and reads CPU/RAM/disk and the
// GRE interface's cumulative byte counters, so the Tunnels dashboard can show
// more than just up/down. Unlike internal/relayhealth (a bare TCP dial,
// works against any box with no login) this requires SSH access, so it only
// ever runs against rows in the tunnels table - a relay added purely for
// monitoring has no credentials to use.
package tunnelmetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/legendary1205/rapido-go/internal/cache"
	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// Metrics is one relay's resource snapshot. CPUPercent is measured over a
// 1-second window on the box itself (two /proc/stat reads a second apart,
// in the same SSH round trip) - a real sample, not a load-average proxy.
// RxBytes/TxBytes are the GRE interface's cumulative counters since the
// box's last reboot (there is no separate "since this tunnel was created"
// counter at the kernel level), which is the simplest real measure of how
// much this tunnel has actually carried without touching frps's own config
// (no webServer/dashboard needs enabling on every relay for this).
//
// RxDropped/TxDropped/RxErrors/TxErrors are the SAME GRE interface's own
// cumulative loss counters (/sys/class/net/<iface>/statistics/*_dropped,
// *_errors) - real packet loss the kernel already tracks for free, not an
// estimate derived from probe timing. Connections is a live count (not
// cumulative - there is no "connections ever" counter to read without
// frps's own dashboard, which this collector deliberately avoids enabling
// fleet-wide just for this), of ESTABLISHED TCP sockets on the relay whose
// local port is one of this tunnel's forwarded ports right now - "how many
// clients are actually using this tunnel at this moment."
type Metrics struct {
	CPUPercent  float64   `json:"cpu_percent"`
	MemUsedMB   int64     `json:"mem_used_mb"`
	MemTotalMB  int64     `json:"mem_total_mb"`
	DiskUsedGB  float64   `json:"disk_used_gb"`
	DiskTotalGB float64   `json:"disk_total_gb"`
	RxBytes     int64     `json:"rx_bytes"`
	TxBytes     int64     `json:"tx_bytes"`
	RxDropped   int64     `json:"rx_dropped"`
	TxDropped   int64     `json:"tx_dropped"`
	RxErrors    int64     `json:"rx_errors"`
	TxErrors    int64     `json:"tx_errors"`
	Connections int       `json:"connections"`
	CheckedAt   time.Time `json:"checked_at"`

	// The box's main network card (its default-route interface): the card the
	// datacenter's port speed and any shaping apply to, which is the real
	// answer to "how much bandwidth is this server using" - the tunnel
	// interface only counts what happens to cross the tunnel, and a tunnel
	// built without a GRE/WireGuard interface (plain frp) has none at all.
	// The rates are measured over the same 1-second window as the CPU sample.
	NIC          string  `json:"nic,omitempty"`
	NICSpeedMbps int     `json:"nic_speed_mbps,omitempty"`
	NICRxBps     float64 `json:"nic_rx_bps"`
	NICTxBps     float64 `json:"nic_tx_bps"`
	NICRxBytes   int64   `json:"nic_rx_bytes"`
	NICTxBytes   int64   `json:"nic_tx_bytes"`

	// Node is the same main-NIC measurement taken on the node end, plus its
	// HAProxy failover when one is in front of frpc. nil when the node was
	// not collected (no node credentials); a node that could not be reached
	// has Node.Error set and the relay numbers above still stand.
	Node *NodeMetrics `json:"node,omitempty"`
}

// NodeMetrics is what is read from the node end of a tunnel.
type NodeMetrics struct {
	NIC          string         `json:"nic,omitempty"`
	NICSpeedMbps int            `json:"nic_speed_mbps,omitempty"`
	RxBps        float64        `json:"rx_bps"`
	TxBps        float64        `json:"tx_bps"`
	RxBytes      int64          `json:"rx_bytes"`
	TxBytes      int64          `json:"tx_bytes"`
	HAProxy      *HAProxyStatus `json:"haproxy,omitempty"`
	// Path is the quality of the path from the node to the relay, measured
	// with a short ping burst over each address family the relay has.
	Path  *PathMetrics `json:"path,omitempty"`
	Error string       `json:"error,omitempty"`
}

// PathMetrics is the node-to-relay path quality of one round: a burst of
// pings over IPv6 and over IPv4. A family with no address to ping is nil.
type PathMetrics struct {
	V6 *netprobe.Ping `json:"v6,omitempty"`
	V4 *netprobe.Ping `json:"v4,omitempty"`
}

// HAProxyStatus is the live state of the HAProxy failover in front of frpc:
// which relay address each backend server points at, whether it is up, and
// where the sessions are right now.
type HAProxyStatus struct {
	Present bool             `json:"present"`
	Active  string           `json:"active,omitempty"` // the server carrying the most sessions
	Servers []HAServerStatus `json:"servers,omitempty"`
}

type HAServerStatus struct {
	Backend      string `json:"backend"`
	Name         string `json:"name"`
	Addr         string `json:"addr,omitempty"`
	Status       string `json:"status"` // UP, DOWN, "UP 1/2" (a check just failed), MAINT, ...
	Backup       bool   `json:"backup"`
	Sessions     int64  `json:"sessions"`
	TotalSession int64  `json:"total_sessions"`
	SinceSeconds int64  `json:"since_seconds"` // how long the status has been what it is
}

// Row is what actually goes into Redis - Metrics plus the tunnel id it
// belongs to and an optional collection error, mirroring relayhealth's own
// Status shape (id + verdict + error, one JSON array). Name is carried along
// for whoever consumes a round through Options.OnTick (alerts name the
// tunnel); it is not needed to read the metrics back.
type Row struct {
	TunnelID int32   `json:"tunnel_id"`
	Name     string  `json:"name,omitempty"`
	Metrics  Metrics `json:"metrics"`
	Error    string  `json:"error,omitempty"`
}

const redisKey = "tunnelmetrics:status"

// redisTTL is a little over 2x the collector's own default interval, same
// reasoning as relayhealth's TTL: if the backend singleton dies, the
// dashboard should stop showing numbers rather than serve stale ones forever.
const redisTTL = 3 * time.Minute

func publish(ctx context.Context, c *cache.Client, rows []Row) error {
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return c.Set(ctx, redisKey, string(raw), redisTTL)
}

// ReadMetrics reads back the last-published snapshot, keyed by tunnel id. A
// tunnel with no entry (nothing published yet, a collection error, or the
// key expired) is simply absent - the caller decides what to show for that.
func ReadMetrics(ctx context.Context, c *cache.Client) (map[int32]Metrics, error) {
	raw, err := c.Get(ctx, redisKey)
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []Row
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	out := make(map[int32]Metrics, len(rows))
	for _, r := range rows {
		if r.Error != "" {
			continue
		}
		out[r.TunnelID] = r.Metrics
	}
	return out, nil
}

// Target is one tunnel to collect metrics for - just enough to dial its
// relay, know which interface's counters to read, and know which locally-
// listening ports count as "this tunnel's own connections" for ss below.
type Target struct {
	TunnelID         int32
	Name             string
	RelayHost        string
	RelaySSHPort     int32
	RelaySSHUser     string
	RelaySSHPassword string
	InterfaceName    string
	Ports            []int32

	// RelayNIC/NodeNIC force a network card for the bandwidth reading; empty
	// means the default-route interface.
	RelayNIC string
	NodeNIC  string
	// Node end, for the node's own bandwidth and its HAProxy. Empty NodeHost
	// skips the node.
	NodeHost        string
	NodeSSHPort     int32
	NodeSSHUser     string
	NodeSSHPassword string
	// HAProxySocket is the admin socket of the HAProxy in front of frpc on
	// the node; empty means there is none to read.
	HAProxySocket string
	// PathV6/PathV4 are the relay's addresses the node pings to measure path
	// quality (internal/netprobe.PickAddrs picks them); empty skips that family.
	PathV6, PathV4 string
}

// Lister returns the tunnels to collect from, called fresh every round -
// mirrors relayhealth.Lister.
type Lister func(ctx context.Context) ([]Target, error)

// script gathers everything in one SSH round trip: a 1-second CPU sample,
// memory, disk, the GRE interface's byte/drop/error counters, and a live
// established-connection count, each on its own line with a fixed prefix
// so parseOutput doesn't need to guess at order. %s is the GRE interface
// name (repeated once per statistics file read) and %s is connCmd - either
// a real `ss` pipeline or a literal "echo 0" when the tunnel forwards no
// ports to count.
const script = `
{{NICSETUP}}
{{PINGSTART}}
nrx1=$(cat /sys/class/net/$NIC/statistics/rx_bytes 2>/dev/null || echo 0); ntx1=$(cat /sys/class/net/$NIC/statistics/tx_bytes 2>/dev/null || echo 0)
read _ a1 b1 c1 i1 _ < /proc/stat
sleep 1
read _ a2 b2 c2 i2 _ < /proc/stat
nrx2=$(cat /sys/class/net/$NIC/statistics/rx_bytes 2>/dev/null || echo 0); ntx2=$(cat /sys/class/net/$NIC/statistics/tx_bytes 2>/dev/null || echo 0)
{{PINGEND}}
dt=$(( (a2+b2+c2+i2) - (a1+b1+c1+i1) ))
di=$(( i2 - i1 ))
if [ "$dt" -gt 0 ]; then echo "CPU:$(( (100*(dt-di))/dt ))"; else echo "CPU:0"; fi
free -m | awk '/^Mem:/ {print "MEM:"$3":"$2}'
df -BG / | awk 'NR==2 {gsub("G","",$2); gsub("G","",$3); print "DISK:"$3":"$2}'
echo "NIC:$NIC"; echo "NICRX:$nrx2"; echo "NICTX:$ntx2"
echo "NICRXBPS:$(( (nrx2-nrx1)*8 ))"; echo "NICTXBPS:$(( (ntx2-ntx1)*8 ))"
echo "NICSPEED:$(cat /sys/class/net/$NIC/speed 2>/dev/null || echo 0)"
echo "RX:$(cat /sys/class/net/{{IFACE}}/statistics/rx_bytes 2>/dev/null || echo 0)"
echo "TX:$(cat /sys/class/net/{{IFACE}}/statistics/tx_bytes 2>/dev/null || echo 0)"
echo "RXDROP:$(cat /sys/class/net/{{IFACE}}/statistics/rx_dropped 2>/dev/null || echo 0)"
echo "TXDROP:$(cat /sys/class/net/{{IFACE}}/statistics/tx_dropped 2>/dev/null || echo 0)"
echo "RXERR:$(cat /sys/class/net/{{IFACE}}/statistics/rx_errors 2>/dev/null || echo 0)"
echo "TXERR:$(cat /sys/class/net/{{IFACE}}/statistics/tx_errors 2>/dev/null || echo 0)"
echo "CONN:$({{CONN}})"
{{HAPROXY}}
`

// connCountCommand builds the shell pipeline that counts, right now, how
// many ESTABLISHED TCP sockets on the relay have one of ports as their
// local (listening) port - i.e. real clients currently using this
// tunnel, not a cumulative "connections ever" figure (there is no such
// kernel counter; frps's own dashboard tracks something close to it, but
// enabling that fleet-wide on every relay just for this figure was not
// worth the extra moving part - see Metrics's own doc comment). A tunnel
// with no forwarded ports yet has nothing to count.
func connCountCommand(ports []int32) string {
	if len(ports) == 0 {
		return "echo 0"
	}
	terms := make([]string, len(ports))
	for i, p := range ports {
		terms[i] = fmt.Sprintf("sport = :%d", p)
	}
	// -H suppresses ss's own header line, which it otherwise always
	// prints regardless of filters - without it, every count here would
	// be off by exactly one.
	return fmt.Sprintf(`ss -tnH state established "( %s )" 2>/dev/null | wc -l`, strings.Join(terms, " or "))
}

var (
	ifaceNameRE  = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,32}$`)
	socketPathRE = regexp.MustCompile(`^/[A-Za-z0-9_./-]{1,120}$`)
)

// nicSetup picks the network card to measure: the one asked for if it is a
// plain interface name, else the default-route interface (IPv4, then IPv6).
func nicSetup(nic string) string {
	if !ifaceNameRE.MatchString(nic) {
		nic = ""
	}
	return `NIC="` + nic + `"
[ -n "$NIC" ] || NIC=$(ip route show default 2>/dev/null | awk '/default/{print $5; exit}')
[ -n "$NIC" ] || NIC=$(ip -6 route show default 2>/dev/null | awk '/default/{print $5; exit}')`
}

// haproxyScript reads the HAProxy admin socket - the only thing every setup
// has, whether the panel or a person configured it - with socat or, failing
// that, a netcat that can speak to a unix socket. HAPSRV rows are
// backend,server,status,scur,stot,bck,lastchg from "show stat"; HAPADDR rows
// are backend,server,address:port from "show servers state".
func haproxyScript(socket string) string {
	if !socketPathRE.MatchString(socket) {
		return ""
	}
	return `SOCK="` + socket + `"
if [ -S "$SOCK" ]; then
  if command -v socat >/dev/null 2>&1; then Q() { echo "$1" | socat stdio "$SOCK"; }
  elif command -v nc >/dev/null 2>&1; then Q() { echo "$1" | nc -U -w2 "$SOCK"; }
  else Q() { :; }; fi
  echo "HAP:1"
  Q "show stat" | awk -F, 'NR>1 && $1 !~ /^#/ && $2!="FRONTEND" && $2!="BACKEND" && $2!="" {print "HAPSRV:"$1","$2","$18","$5","$8","$21","$24}'
  Q "show servers state" | awk 'NR>2 && NF>=19 {print "HAPADDR:"$2","$4","$5":"$19}'
else
  echo "HAP:0"
fi`
}

func buildScript(ifaceName, nic string, ports []int32, haproxySocket, pathV6, pathV4 string) string {
	if !ifaceNameRE.MatchString(ifaceName) {
		ifaceName = "none"
	}
	return strings.NewReplacer(
		"{{PINGSTART}}", pingStart(pathV6, pathV4),
		"{{PINGEND}}", pingEnd(pathV6, pathV4),
		"{{NICSETUP}}", nicSetup(nic),
		"{{IFACE}}", ifaceName,
		"{{CONN}}", connCountCommand(ports),
		"{{HAPROXY}}", haproxyScript(haproxySocket),
	).Replace(script)
}

func collect(ctx context.Context, relay *sshexec.Client, ifaceName, nic string, ports []int32) (Metrics, error) {
	out, err := relay.Run(ctx, buildScript(ifaceName, nic, ports, "", "", ""))
	if err != nil {
		return Metrics{}, err
	}
	return parseOutput(out)
}

// collectNode takes the node's main-NIC rate and, if socket is set, its
// HAProxy status. No connection count is asked for on the node.
func collectNode(ctx context.Context, node *sshexec.Client, nic, socket, pathV6, pathV4 string) NodeMetrics {
	out, err := node.Run(ctx, buildScript("none", nic, nil, socket, pathV6, pathV4))
	if err != nil {
		return NodeMetrics{Error: err.Error()}
	}
	m, _ := parseOutput(out)
	return NodeMetrics{
		NIC: m.NIC, NICSpeedMbps: m.NICSpeedMbps, RxBps: m.NICRxBps, TxBps: m.NICTxBps,
		RxBytes: m.NICRxBytes, TxBytes: m.NICTxBytes, HAProxy: parseHAProxy(out),
		Path: parsePath(out, pathV6, pathV4),
	}
}

// parseHAProxy builds the failover status from HAP* lines, or nil if the
// script did not look at HAProxy at all (no HAP: line).
func parseHAProxy(out string) *HAProxyStatus {
	var st *HAProxyStatus
	addr := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "HAP:"):
			st = &HAProxyStatus{Present: strings.TrimPrefix(line, "HAP:") == "1"}
		case strings.HasPrefix(line, "HAPADDR:"):
			if p := strings.SplitN(strings.TrimPrefix(line, "HAPADDR:"), ",", 3); len(p) == 3 {
				addr[p[0]+"/"+p[1]] = p[2]
			}
		}
	}
	if st == nil {
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "HAPSRV:") {
			continue
		}
		p := strings.Split(strings.TrimPrefix(line, "HAPSRV:"), ",")
		if len(p) < 7 {
			continue
		}
		sess, _ := strconv.ParseInt(p[3], 10, 64)
		total, _ := strconv.ParseInt(p[4], 10, 64)
		since, _ := strconv.ParseInt(p[6], 10, 64)
		st.Servers = append(st.Servers, HAServerStatus{
			Backend: p[0], Name: p[1], Addr: addr[p[0]+"/"+p[1]], Status: p[2],
			Backup: p[5] == "1", Sessions: sess, TotalSession: total, SinceSeconds: since,
		})
	}
	var best int64 = -1
	for _, sv := range st.Servers {
		if sv.Sessions > best {
			best, st.Active = sv.Sessions, sv.Name
		}
	}
	if best <= 0 {
		st.Active = ""
	}
	return st
}

func parseOutput(out string) (Metrics, error) {
	var m Metrics
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "CPU:"):
			m.CPUPercent, _ = strconv.ParseFloat(strings.TrimPrefix(line, "CPU:"), 64)
		case strings.HasPrefix(line, "MEM:"):
			parts := strings.SplitN(strings.TrimPrefix(line, "MEM:"), ":", 2)
			if len(parts) == 2 {
				m.MemUsedMB, _ = strconv.ParseInt(parts[0], 10, 64)
				m.MemTotalMB, _ = strconv.ParseInt(parts[1], 10, 64)
			}
		case strings.HasPrefix(line, "DISK:"):
			parts := strings.SplitN(strings.TrimPrefix(line, "DISK:"), ":", 2)
			if len(parts) == 2 {
				m.DiskUsedGB, _ = strconv.ParseFloat(parts[0], 64)
				m.DiskTotalGB, _ = strconv.ParseFloat(parts[1], 64)
			}
		case strings.HasPrefix(line, "RX:"):
			m.RxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "RX:"), 10, 64)
		case strings.HasPrefix(line, "TX:"):
			m.TxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "TX:"), 10, 64)
		case strings.HasPrefix(line, "RXDROP:"):
			m.RxDropped, _ = strconv.ParseInt(strings.TrimPrefix(line, "RXDROP:"), 10, 64)
		case strings.HasPrefix(line, "TXDROP:"):
			m.TxDropped, _ = strconv.ParseInt(strings.TrimPrefix(line, "TXDROP:"), 10, 64)
		case strings.HasPrefix(line, "RXERR:"):
			m.RxErrors, _ = strconv.ParseInt(strings.TrimPrefix(line, "RXERR:"), 10, 64)
		case strings.HasPrefix(line, "TXERR:"):
			m.TxErrors, _ = strconv.ParseInt(strings.TrimPrefix(line, "TXERR:"), 10, 64)
		case strings.HasPrefix(line, "NIC:"):
			m.NIC = strings.TrimPrefix(line, "NIC:")
		case strings.HasPrefix(line, "NICRX:"):
			m.NICRxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "NICRX:"), 10, 64)
		case strings.HasPrefix(line, "NICTX:"):
			m.NICTxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "NICTX:"), 10, 64)
		case strings.HasPrefix(line, "NICRXBPS:"):
			m.NICRxBps, _ = strconv.ParseFloat(strings.TrimPrefix(line, "NICRXBPS:"), 64)
		case strings.HasPrefix(line, "NICTXBPS:"):
			m.NICTxBps, _ = strconv.ParseFloat(strings.TrimPrefix(line, "NICTXBPS:"), 64)
		case strings.HasPrefix(line, "NICSPEED:"):
			// virtio and some VMs report -1 or nothing; only a positive speed is real.
			if v, err := strconv.Atoi(strings.TrimPrefix(line, "NICSPEED:")); err == nil && v > 0 {
				m.NICSpeedMbps = v
			}
		case strings.HasPrefix(line, "CONN:"):
			conn, _ := strconv.Atoi(strings.TrimPrefix(line, "CONN:"))
			m.Connections = conn
		}
	}
	m.CheckedAt = time.Now()
	return m, nil
}

type Options struct {
	Lister      Lister
	Interval    time.Duration
	DialTimeout time.Duration
	Logger      *slog.Logger
	OnTick      func(rows []Row)
}

// Run collects every interval until ctx is done, concurrently across
// targets (one SSH dial + ~1s CPU sample each, so sequential would scale
// badly with fleet size). The first round runs immediately.
func Run(ctx context.Context, c *cache.Client, o Options) {
	if o.Interval <= 0 {
		o.Interval = 60 * time.Second
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}

	tick := func() {
		targets, err := o.Lister(ctx)
		if err != nil {
			o.Logger.Warn("tunnelmetrics: could not list targets", "error", err)
			return
		}
		rows := make([]Row, len(targets))
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func(i int, t Target) {
				defer wg.Done()
				rows[i] = Row{TunnelID: t.TunnelID, Name: t.Name}
				dctx, cancel := context.WithTimeout(ctx, o.DialTimeout+2*time.Second)
				defer cancel()
				client, err := sshexec.Dial(dctx, sshexec.Config{
					Host: t.RelayHost, Port: t.RelaySSHPort, User: t.RelaySSHUser, Password: t.RelaySSHPassword,
				})
				if err != nil {
					rows[i].Error = err.Error()
					return
				}
				defer client.Close()
				// The login and the reading each get their own budget: a slow
				// SSH handshake must not eat the time the script itself needs.
				rctx, rcancel := context.WithTimeout(ctx, runTimeout)
				defer rcancel()
				m, err := collect(rctx, client, t.InterfaceName, t.RelayNIC, t.Ports)
				if err != nil {
					rows[i].Error = err.Error()
					return
				}
				if t.NodeHost != "" {
					nm := collectNodeFor(ctx, t, o.DialTimeout)
					m.Node = &nm
				}
				rows[i].Metrics = m
			}(i, t)
		}
		wg.Wait()

		if err := publish(ctx, c, rows); err != nil {
			o.Logger.Warn("tunnelmetrics: could not publish to redis", "error", err)
		}
		if o.OnTick != nil {
			o.OnTick(rows)
		}
	}

	tick()
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// collectNodeFor dials the node and reads it; a node that cannot be reached
// is reported in the result, it does not fail the relay's numbers.
func collectNodeFor(ctx context.Context, t Target, dialTimeout time.Duration) NodeMetrics {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout+4*time.Second)
	defer cancel()
	client, err := sshexec.Dial(dctx, sshexec.Config{Host: t.NodeHost, Port: t.NodeSSHPort, User: t.NodeSSHUser, Password: t.NodeSSHPassword})
	if err != nil {
		return NodeMetrics{Error: err.Error()}
	}
	defer client.Close()
	rctx, rcancel := context.WithTimeout(ctx, runTimeout)
	defer rcancel()
	return collectNode(rctx, client, t.NodeNIC, t.HAProxySocket, t.PathV6, t.PathV4)
}

// runTimeout bounds one reading (the script on the relay or on the node),
// separate from the SSH login's own limit.
const runTimeout = 30 * time.Second
