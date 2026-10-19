// Package tunneldiscover works out what tunnel, if any, already runs between
// a node and a relay - however it was built. The panel provisions tunnels of
// its own (internal/tunnelprovision), but admins also set them up by hand,
// and the panel should be able to monitor those too without being told what
// they are: it reads the node (and, when it has the login, the relay) over SSH
// and recognises the design from what is actually running - frp configs, the
// GRE/WireGuard interfaces, an HAProxy failover in front of frpc, the relay's
// forwarding rules.
//
// Nothing here writes to either box. This file is the pure part (parsers and
// the analysis that turns their output into a verdict); gather.go does the
// SSH.
package tunneldiscover

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FRPClient is what matters in an frpc.toml.
type FRPClient struct {
	ServerAddr  string
	ServerPort  int
	Protocol    string // "tcp" (the default), "quic", ...
	PoolCount   int
	RemotePorts []int32
}

var (
	reKV         = regexp.MustCompile(`^\s*([A-Za-z0-9_.]+)\s*=\s*(.+?)\s*$`)
	reRangePair  = regexp.MustCompile(`parseNumberRangePair\s+"([0-9,\- ]+)"\s+"([0-9,\- ]+)"`)
	reRemotePort = regexp.MustCompile(`^\s*remotePort\s*=\s*(\d+)`)
)

func unquote(s string) string { return strings.Trim(strings.TrimSpace(s), `"'`) }

// ParseFRPClient reads an frpc.toml. frp configs written by the common
// install scripts list their ports in one template line
// (`parseNumberRangePair "20001,20005" "20001,20005"` - local list, then
// remote list), so that is understood as well as plain `remotePort = N` lines.
func ParseFRPClient(content string) FRPClient {
	c := FRPClient{Protocol: "tcp"}
	seen := map[int32]bool{}
	add := func(p int32) {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			c.RemotePorts = append(c.RemotePorts, p)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if m := reRangePair.FindStringSubmatch(line); m != nil {
			for _, p := range expandPortList(m[2]) {
				add(p)
			}
			continue
		}
		if m := reRemotePort.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			add(int32(n))
			continue
		}
		kv := reKV.FindStringSubmatch(line)
		if kv == nil {
			continue
		}
		switch kv[1] {
		case "serverAddr", "server_addr":
			c.ServerAddr = unquote(kv[2])
		case "serverPort", "server_port":
			c.ServerPort, _ = strconv.Atoi(unquote(kv[2]))
		case "transport.protocol", "protocol":
			if v := unquote(kv[2]); v != "" {
				c.Protocol = v
			}
		case "transport.poolCount", "pool_count":
			c.PoolCount, _ = strconv.Atoi(unquote(kv[2]))
		}
	}
	sort.Slice(c.RemotePorts, func(i, j int) bool { return c.RemotePorts[i] < c.RemotePorts[j] })
	return c
}

// expandPortList turns "20001,20005,20110-20112" into individual ports.
func expandPortList(s string) []int32 {
	var out []int32
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, e1 := strconv.Atoi(strings.TrimSpace(lo))
			b, e2 := strconv.Atoi(strings.TrimSpace(hi))
			if e1 != nil || e2 != nil || a > b || b-a > 5000 {
				continue
			}
			for p := a; p <= b; p++ {
				out = append(out, int32(p))
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil {
			out = append(out, int32(n))
		}
	}
	return out
}

// FRPServer is what matters in an frps.toml.
type FRPServer struct {
	BindPort     int
	QUICBindPort int
}

func ParseFRPServer(content string) FRPServer {
	var s FRPServer
	for _, line := range strings.Split(content, "\n") {
		kv := reKV.FindStringSubmatch(line)
		if kv == nil {
			continue
		}
		switch kv[1] {
		case "bindPort", "bind_port":
			s.BindPort, _ = strconv.Atoi(unquote(kv[2]))
		case "quicBindPort", "kcp_bind_port":
			s.QUICBindPort, _ = strconv.Atoi(unquote(kv[2]))
		}
	}
	return s
}

// HAServer is one `server` line of an HAProxy backend.
type HAServer struct {
	Name   string `json:"name"`
	Addr   string `json:"addr"`
	Backup bool   `json:"backup"`
}

// HAProxyConfig is the part of haproxy.cfg a tunnel cares about: which local
// bind port feeds which backend, the backend's servers, and where the admin
// socket is (so the panel can read live status without a stats page).
type HAProxyConfig struct {
	Socket   string
	Frontend map[string]string // "127.0.0.1:7001" -> backend name
	Backends map[string][]HAServer
}

var (
	reBindAddr = regexp.MustCompile(`^\s*bind\s+(\S+)`)
	reServer   = regexp.MustCompile(`^\s*server\s+(\S+)\s+(\S+)(.*)$`)
	reSocket   = regexp.MustCompile(`^\s*stats\s+socket\s+(\S+)`)
)

func ParseHAProxy(content string) HAProxyConfig {
	cfg := HAProxyConfig{Frontend: map[string]string{}, Backends: map[string][]HAServer{}}
	var section, name string
	var binds []string
	flush := func(defaultBackend string) {
		if section == "frontend" || section == "listen" {
			for _, b := range binds {
				if defaultBackend != "" {
					cfg.Frontend[b] = defaultBackend
				} else if section == "listen" {
					cfg.Frontend[b] = name
				}
			}
		}
		binds = nil
	}
	defaultBackend := ""
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "global", "defaults", "frontend", "backend", "listen", "resolvers", "userlist", "peers":
			flush(defaultBackend)
			defaultBackend = ""
			section = fields[0]
			name = ""
			if len(fields) > 1 {
				name = fields[1]
			}
			continue
		}
		if m := reSocket.FindStringSubmatch(raw); m != nil && section == "global" {
			cfg.Socket = m[1]
			continue
		}
		if section == "frontend" || section == "listen" {
			if m := reBindAddr.FindStringSubmatch(raw); m != nil {
				binds = append(binds, normalizeBind(m[1]))
			}
			if fields[0] == "default_backend" && len(fields) > 1 {
				defaultBackend = fields[1]
			}
		}
		if section == "backend" || section == "listen" {
			if m := reServer.FindStringSubmatch(raw); m != nil {
				srv := HAServer{Name: m[1], Addr: strings.Trim(m[2], ""), Backup: hasWord(m[3], "backup")}
				cfg.Backends[name] = append(cfg.Backends[name], srv)
			}
		}
	}
	flush(defaultBackend)
	return cfg
}

func hasWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}

// normalizeBind turns "*:7001" / ":7001" / "127.0.0.1:7001" into host:port.
func normalizeBind(b string) string {
	if strings.HasPrefix(b, ":") {
		return "*" + b
	}
	return b
}

// splitHostPort splits "1.2.3.4:7000" and "[2a05::1]:7000".
func splitHostPort(s string) (host string, port int, ok bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 || len(s) <= end+2 || s[end+1] != ':' {
			return "", 0, false
		}
		p, err := strconv.Atoi(s[end+2:])
		return s[1:end], p, err == nil
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	p, err := strconv.Atoi(s[i+1:])
	return s[:i], p, err == nil
}

// IPTunnel is one GRE-family interface from `ip tunnel show`.
type IPTunnel struct {
	Name   string
	Kind   string // "gre" or "ip6gre"
	Remote string
	Local  string
}

var reTunnel = regexp.MustCompile(`^(\S+?):\s+(\S+)\s+remote\s+(\S+)\s+local\s+(\S+)`)

// ParseIPTunnel reads `ip tunnel show`, keeping real point-to-point GRE and
// GRE-over-IPv6 tunnels. The kernel's placeholder devices (gre0, gretap0,
// remote "any") and other kinds (vti, ipip, sit) are not tunnels to a relay.
func ParseIPTunnel(out string) []IPTunnel {
	var res []IPTunnel
	for _, line := range strings.Split(out, "\n") {
		m := reTunnel.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || m[3] == "any" {
			continue
		}
		var kind string
		switch {
		case strings.HasPrefix(m[2], "ip6gre"):
			kind = "ip6gre"
		case strings.HasPrefix(m[2], "gre"):
			kind = "gre"
		default:
			continue
		}
		res = append(res, IPTunnel{Name: m[1], Kind: kind, Remote: m[3], Local: m[4]})
	}
	return res
}

// WGPeer is one peer of a WireGuard interface.
type WGPeer struct {
	Iface      string
	Endpoint   string
	AllowedIPs []string
}

// ParseWGDump reads `wg show all dump`. Only interfaces whose peer is allowed
// a private /32 count as a tunnel to a relay: a VPN exit (a commercial
// interface, allowed 0.0.0.0/0) is where traffic leaves, not a tunnel.
func ParseWGDump(out string) []WGPeer {
	var res []WGPeer
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		// peer rows: iface, pubkey, psk, endpoint, allowed-ips, handshake, rx, tx, keepalive
		if len(f) < 9 {
			continue
		}
		var allowed []string
		for _, a := range strings.Split(f[4], ",") {
			if a = strings.TrimSpace(a); a != "" && a != "(none)" {
				allowed = append(allowed, a)
			}
		}
		res = append(res, WGPeer{Iface: f[0], Endpoint: f[3], AllowedIPs: allowed})
	}
	return res
}

// IsTunnelPeer reports whether a WG peer looks like a node<->relay tunnel:
// every allowed network is a single private address.
func (p WGPeer) IsTunnelPeer() bool {
	if len(p.AllowedIPs) == 0 {
		return false
	}
	for _, a := range p.AllowedIPs {
		pfx, err := netip.ParsePrefix(a)
		if err != nil || !pfx.Addr().IsPrivate() || pfx.Bits() != pfx.Addr().BitLen() {
			return false
		}
	}
	return true
}

// DefaultRouteDev returns the interface of the first default route in
// `ip route show default` output ("default via 1.2.3.1 dev eth0 proto ...").
func DefaultRouteDev(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "dev" && strings.HasPrefix(strings.TrimSpace(line), "default") {
				return f[i+1]
			}
		}
	}
	return ""
}

// DNATRule is a port forward on a relay.
type DNATRule struct {
	Port   int32
	Target string // host part of --to-destination
}

var reDNAT = regexp.MustCompile(`--dport\s+(\d+).*-j\s+DNAT\s+--to-destination\s+(\S+)`)

// ParseDNAT reads `iptables -t nat -S` and returns the port forwards that
// point at a private address (the far end of a tunnel); a DNAT to a public
// address is somebody else's business.
func ParseDNAT(out string) []DNATRule {
	var res []DNATRule
	for _, line := range strings.Split(out, "\n") {
		m := reDNAT.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		port, _ := strconv.Atoi(m[1])
		host, _, ok := splitHostPort(m[2])
		if !ok {
			host = m[2]
		}
		if a, err := netip.ParseAddr(host); err == nil && a.IsPrivate() {
			res = append(res, DNATRule{Port: int32(port), Target: host})
		}
	}
	return res
}

var reSSListen = regexp.MustCompile(`(?:\*|\[::\]|0\.0\.0\.0|\[::ffff:[0-9.]+\]):(\d+)\s.*users:\(\("([^"]+)"`)

// ParseSSListeners returns the ports a given process listens on, from
// `ss -ltnpH` output.
func ParseSSListeners(out, process string) []int32 {
	seen := map[int32]bool{}
	var res []int32
	for _, line := range strings.Split(out, "\n") {
		m := reSSListen.FindStringSubmatch(line)
		if m == nil || m[2] != process {
			continue
		}
		p, _ := strconv.Atoi(m[1])
		if !seen[int32(p)] {
			seen[int32(p)] = true
			res = append(res, int32(p))
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
	return res
}

func joinInts(ports []int32) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, ",")
}
