package tunnelmetrics

import (
	"strings"

	"github.com/legendary1205/rapido-go/internal/netprobe"
)

// Path-quality burst: how many pings, how far apart. Ten at 0.2 s is two
// seconds, which overlaps the one-second CPU/NIC sample, and the bursts of the
// two families run side by side, so it adds about a second to a round.
const (
	pathPingCount    = 10
	pathPingInterval = 0.2
)

// pingStart/pingEnd bracket the CPU/NIC sample: the bursts start before it
// (so they overlap its sleep) and are collected after it. Each family writes
// to its own file; the statistics line and the round-trip line are echoed
// with a prefix so parsePath can rebuild the ping output. Both are empty when
// no target address is given.
func pingStart(v6, v4 string) string {
	cmd6 := netprobe.PingCommand(6, v6, pathPingCount, pathPingInterval, 1, "")
	cmd4 := netprobe.PingCommand(4, v4, pathPingCount, pathPingInterval, 1, "")
	if cmd6 == "" && cmd4 == "" {
		return ""
	}
	var lines []string
	lines = append(lines, `PT=$(mktemp -d 2>/dev/null); [ -n "$PT" ] || PT=/tmp/.tmetrics$$; mkdir -p "$PT"`)
	if cmd6 != "" {
		lines = append(lines, `( `+cmd6+` > "$PT/p6" 2>&1 ) &`)
	}
	if cmd4 != "" {
		lines = append(lines, `( `+cmd4+` > "$PT/p4" 2>&1 ) &`)
	}
	return strings.Join(lines, "\n")
}

func pingEnd(v6, v4 string) string {
	if pingStart(v6, v4) == "" {
		return ""
	}
	lines := []string{"wait"}
	for _, fam := range []struct{ tag, file string }{{"6", "p6"}, {"4", "p4"}} {
		path := `"$PT/` + fam.file + `"`
		lines = append(lines,
			`if [ -f `+path+` ]; then`,
			`  echo "PING`+fam.tag+`:$(grep -E 'packets transmitted' `+path+` | head -1)"`,
			`  echo "PING`+fam.tag+`RTT:$(grep -E '^(rtt|round-trip)' `+path+` | head -1)"`,
			`fi`,
		)
	}
	lines = append(lines, `rm -rf "$PT"`)
	return strings.Join(lines, "\n")
}

// parsePath rebuilds the path quality from the PING6/PING4 lines. A family
// whose ping printed no statistics (it could not even route) comes back as a
// result with 100% loss rather than nil, since "tried and failed" is
// information; a family that was never asked for is nil.
func parsePath(out, v6, v4 string) *PathMetrics {
	var pm PathMetrics
	read := func(tag, addr string) *netprobe.Ping {
		var stat, rtt string
		asked := false
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "PING"+tag+":"):
				asked = true
				stat = strings.TrimPrefix(line, "PING"+tag+":")
			case strings.HasPrefix(line, "PING"+tag+"RTT:"):
				rtt = strings.TrimPrefix(line, "PING"+tag+"RTT:")
			}
		}
		if !asked {
			return nil
		}
		p, ok := netprobe.ParsePing(stat + "\n" + rtt)
		if !ok {
			p = netprobe.Ping{Sent: pathPingCount, LossPct: 100}
		}
		p.Target = addr
		return &p
	}
	if v6 != "" {
		pm.V6 = read("6", v6)
	}
	if v4 != "" {
		pm.V4 = read("4", v4)
	}
	if pm.V6 == nil && pm.V4 == nil {
		return nil
	}
	return &pm
}

// NICSetupScript is the shell snippet that sets $NIC to the card to measure
// (the one asked for if it is a plain interface name, else the default-route
// interface), for other collectors that read the same card.
func NICSetupScript(nic string) string { return nicSetup(nic) }

// HAProxyScript is the shell snippet that prints the HAP* lines for the HAProxy
// admin socket (empty for an unsafe path), and ParseHAProxy turns that output
// into the failover status.
func HAProxyScript(socket string) string { return haproxyScript(socket) }

// ParseHAProxy builds the failover status from HAP* lines, or nil if the
// script did not look at HAProxy at all.
func ParseHAProxy(out string) *HAProxyStatus { return parseHAProxy(out) }
