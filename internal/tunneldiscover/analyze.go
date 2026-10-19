package tunneldiscover

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/legendary1205/rapido-go/internal/tunnelprovision"
)

// MethodExternal marks a tunnel that exists but is not one of the designs the
// panel itself builds (for example frp running through a GRE tunnel over IPv6).
const MethodExternal = "external"

// ConfigFile is a file read from a box.
type ConfigFile struct {
	Path    string
	Content string
}

// NodeFacts is everything read from the node's side.
type NodeFacts struct {
	Route4, Route6 string // `ip route show default`
	Addrs          string // `ip -o -4 addr show`
	IPTunnel       string // `ip tunnel show`
	WGDump         string // `wg show all dump`
	FRPC           []ConfigFile
	HAProxy        *ConfigFile
}

// RelayFacts is everything read from the relay's side.
type RelayFacts struct {
	Route4, Route6 string
	IPTunnel       string
	WGDump         string
	Listen         string // `ss -ltnpH`
	DNAT           string // `iptables -t nat -S`
	FRPS           []ConfigFile
}

// FRPInfo records where the frp configs are and how the link is set up.
type FRPInfo struct {
	ClientConfig string `json:"client_config,omitempty"`
	ServerConfig string `json:"server_config,omitempty"`
	ControlPort  int    `json:"control_port,omitempty"`
	Transport    string `json:"transport,omitempty"`
	PoolCount    int    `json:"pool_count,omitempty"`
}

// HAInfo records an HAProxy failover sitting between frpc and the relay.
type HAInfo struct {
	Socket   string     `json:"socket,omitempty"`
	Config   string     `json:"config,omitempty"`
	Frontend string     `json:"frontend,omitempty"`
	Servers  []HAServer `json:"servers,omitempty"`
}

// Details is stored in tunnels.details and read back by monitoring.
type Details struct {
	Discovered bool     `json:"discovered"`
	Protocol   string   `json:"protocol,omitempty"` // human description of what was found
	NodeNIC    string   `json:"node_nic,omitempty"`
	RelayNIC   string   `json:"relay_nic,omitempty"`
	RelayAddrs []string `json:"relay_addrs,omitempty"`
	Interface  string   `json:"interface,omitempty"`
	FRP        *FRPInfo `json:"frp,omitempty"`
	HAProxy    *HAInfo  `json:"haproxy,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

// Result is the verdict.
type Result struct {
	Found       bool
	Method      string
	ControlPort int32
	Ports       []int32
	Interface   string
	// RelayHost is the address best suited for SSH-ing to the relay (IPv4 when
	// the node knows one), empty if the node revealed none.
	RelayHost string
	Details   Details
}

const defaultHASocket = "/run/haproxy/admin.sock"

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsLoopback()
}

func isPrivate(host string) bool {
	a, err := netip.ParseAddr(host)
	return err == nil && a.IsPrivate()
}

func family(host string) string {
	a, err := netip.ParseAddr(host)
	switch {
	case err != nil:
		return "host"
	case a.Is6():
		return "IPv6"
	default:
		return "IPv4"
	}
}

// ParseAddrs maps interface -> prefix from `ip -o -4 addr show`.
func ParseAddrs(out string) map[string]netip.Prefix {
	res := map[string]netip.Prefix{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "inet" && len(f) > 1 {
				if p, err := netip.ParsePrefix(f[i+1]); err == nil {
					res[strings.TrimSuffix(f[1], ":")] = p
				}
			}
		}
	}
	return res
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// pickSSHHost prefers an IPv4 address, which is what an admin can usually SSH to.
func pickSSHHost(addrs []string) string {
	for _, a := range addrs {
		if p, err := netip.ParseAddr(a); err == nil && p.Is4() {
			return a
		}
	}
	if len(addrs) > 0 {
		return addrs[0]
	}
	return ""
}

func pathLabel(addrs []string, haveBackup bool) string {
	if len(addrs) == 0 {
		return ""
	}
	label := family(addrs[0])
	if haveBackup && len(addrs) > 1 {
		label += " with " + family(addrs[1]) + " backup"
	}
	return label
}

// Analyze turns what was read from the node (and relay, when known) into a
// verdict: is there a tunnel, what design is it, which relay does it reach
// and which public ports does it carry.
func Analyze(n NodeFacts, r *RelayFacts) Result {
	res := Result{Details: Details{Discovered: true}}
	res.Details.NodeNIC = firstNonEmpty(DefaultRouteDev(n.Route4), DefaultRouteDev(n.Route6))
	if r != nil {
		res.Details.RelayNIC = firstNonEmpty(DefaultRouteDev(r.Route4), DefaultRouteDev(r.Route6))
	}

	tunnels := ParseIPTunnel(n.IPTunnel)
	addrs := ParseAddrs(n.Addrs)
	var wgTunnels []WGPeer
	for _, p := range ParseWGDump(n.WGDump) {
		if p.IsTunnelPeer() {
			wgTunnels = append(wgTunnels, p)
		}
	}

	if len(n.FRPC) > 0 {
		analyzeFRP(&res, n, tunnels, addrs)
	} else {
		analyzeNoFRP(&res, tunnels, wgTunnels)
	}
	if !res.Found {
		return res
	}
	// Details.FRP.ControlPort is the port on the relay; with HAProxy in front,
	// frpc itself dials a local port (kept in Details.HAProxy.Frontend).
	if res.Details.FRP != nil && res.ControlPort > 0 {
		res.Details.FRP.ControlPort = int(res.ControlPort)
	}

	// Ports: the frpc config names the public ports; for a forwarding design
	// they are read off the relay's DNAT rules.
	if len(res.Ports) == 0 && r != nil {
		seen := map[int32]bool{}
		for _, rule := range ParseDNAT(r.DNAT) {
			if !seen[rule.Port] {
				seen[rule.Port] = true
				res.Ports = append(res.Ports, rule.Port)
			}
		}
		sort.Slice(res.Ports, func(i, j int) bool { return res.Ports[i] < res.Ports[j] })
	}
	if r != nil && len(r.FRPS) > 0 && res.Details.FRP != nil {
		res.Details.FRP.ServerConfig = r.FRPS[0].Path
		if srv := ParseFRPServer(r.FRPS[0].Content); srv.QUICBindPort > 0 && res.Details.FRP.Transport == "tcp" {
			res.Details.Notes = append(res.Details.Notes, "the relay also listens for frp over QUIC")
		}
	}
	res.RelayHost = pickSSHHost(res.Details.RelayAddrs)
	return res
}

func analyzeFRP(res *Result, n NodeFacts, tunnels []IPTunnel, addrs map[string]netip.Prefix) {
	cfgFile := n.FRPC[0]
	for _, f := range n.FRPC {
		if c := ParseFRPClient(f.Content); len(c.RemotePorts) > 0 {
			cfgFile = f
			break
		}
	}
	c := ParseFRPClient(cfgFile.Content)
	res.Found = true
	res.Ports = c.RemotePorts
	res.ControlPort = int32(c.ServerPort)
	res.Details.FRP = &FRPInfo{ClientConfig: cfgFile.Path, ControlPort: c.ServerPort, Transport: c.Protocol, PoolCount: c.PoolCount}

	host := c.ServerAddr
	transport := "frp over TCP"
	if c.Protocol == "quic" {
		transport = "frp over QUIC"
	}

	switch {
	case isLoopback(host):
		// frpc talks to a local HAProxy, which owns the real path to the relay.
		if n.HAProxy == nil {
			res.Details.Notes = append(res.Details.Notes, "frpc connects to localhost but no HAProxy configuration was found")
			res.Method = MethodExternal
			res.Details.Protocol = transport + " via a local proxy"
			return
		}
		ha := ParseHAProxy(n.HAProxy.Content)
		backend := firstNonEmpty(
			ha.Frontend[fmt.Sprintf("127.0.0.1:%d", c.ServerPort)],
			ha.Frontend[fmt.Sprintf("*:%d", c.ServerPort)],
			ha.Frontend[fmt.Sprintf("localhost:%d", c.ServerPort)],
		)
		info := &HAInfo{Socket: firstNonEmpty(ha.Socket, defaultHASocket), Config: n.HAProxy.Path, Frontend: fmt.Sprintf("127.0.0.1:%d", c.ServerPort)}
		var primary, backup []string
		for _, s := range ha.Backends[backend] {
			info.Servers = append(info.Servers, s)
			h, p, ok := splitHostPort(s.Addr)
			if !ok {
				continue
			}
			if s.Backup {
				backup = append(backup, h)
			} else {
				primary = append(primary, h)
				if res.ControlPort == int32(c.ServerPort) {
					res.ControlPort = int32(p)
				}
			}
		}
		res.Details.HAProxy = info
		if backend == "" {
			res.Details.Notes = append(res.Details.Notes, "HAProxy has no frontend on the port frpc connects to")
		}
		res.Details.RelayAddrs = append(append([]string{}, primary...), backup...)
		res.Details.Protocol = transport + " via HAProxy (" + pathLabel(res.Details.RelayAddrs, len(backup) > 0) + ")"
		res.Method = frpMethod(c.Protocol)
		// A primary that is a tunnel-internal address means frp rides a GRE
		// tunnel, with the other addresses as the fallback path.
		if len(primary) > 0 && isPrivate(primary[0]) && c.Protocol != "quic" {
			if gre := greOwning(tunnels, addrs, primary[0]); gre != nil && gre.Kind == "gre" {
				res.Details.Interface = gre.Name
				res.Interface = gre.Name
				res.Details.RelayAddrs = append([]string{gre.Remote}, backup...)
				res.Details.Protocol = "GRE over " + family(gre.Remote) + " + " + transport + " via HAProxy (" + pathLabel(res.Details.RelayAddrs, len(backup) > 0) + ")"
				res.Method = tunnelprovision.MethodGREFRP
			}
		}
	case isPrivate(host):
		// frpc dials a tunnel-internal address: it rides a GRE tunnel.
		gre := greOwning(tunnels, addrs, host)
		if gre == nil {
			res.Method = MethodExternal
			res.Details.Protocol = transport + " through a private tunnel address"
			res.Details.Notes = append(res.Details.Notes, "frpc dials "+host+" but no GRE interface owning that address was found")
			return
		}
		res.Details.Interface = gre.Name
		res.Interface = gre.Name
		res.Details.RelayAddrs = []string{gre.Remote}
		if gre.Kind == "gre" && c.Protocol != "quic" {
			res.Method = tunnelprovision.MethodGREFRP
			res.Details.Protocol = "GRE over IPv4 + frp over TCP"
		} else {
			res.Method = MethodExternal
			res.Details.Protocol = fmt.Sprintf("%s over %s + %s", strings.ToUpper(gre.Kind), family(gre.Remote), transport)
		}
	default:
		res.Details.RelayAddrs = []string{host}
		res.Details.Protocol = transport + " (" + family(host) + ")"
		res.Method = frpMethod(c.Protocol)
	}
}

// greOwning returns the tunnel interface whose own subnet contains host.
func greOwning(tunnels []IPTunnel, addrs map[string]netip.Prefix, host string) *IPTunnel {
	for i := range tunnels {
		if p, ok := addrs[tunnels[i].Name]; ok && p.Contains(mustAddr(host)) {
			return &tunnels[i]
		}
	}
	return nil
}

func frpMethod(protocol string) string {
	if protocol == "quic" {
		return tunnelprovision.MethodFRPQUIC
	}
	return tunnelprovision.MethodFRPDirect
}

func mustAddr(s string) netip.Addr {
	a, _ := netip.ParseAddr(s)
	return a
}

func analyzeNoFRP(res *Result, tunnels []IPTunnel, wg []WGPeer) {
	switch {
	case len(tunnels) > 0:
		t := tunnels[0]
		res.Found = true
		res.Interface = t.Name
		res.Details.Interface = t.Name
		res.Details.RelayAddrs = []string{t.Remote}
		if t.Kind == "ip6gre" {
			res.Method = tunnelprovision.MethodIP6GREDNAT
			res.Details.Protocol = "GRE over IPv6 + kernel port forwarding"
		} else {
			res.Method = tunnelprovision.MethodGREDNAT
			res.Details.Protocol = "GRE over IPv4 + kernel port forwarding"
		}
	case len(wg) > 0:
		p := wg[0]
		res.Found = true
		res.Interface = p.Iface
		res.Details.Interface = p.Iface
		if h, _, ok := splitHostPort(p.Endpoint); ok {
			res.Details.RelayAddrs = []string{h}
		}
		res.Method = tunnelprovision.MethodWGDNAT
		res.Details.Protocol = "WireGuard + kernel port forwarding"
	}
}
