package tunnelprovision

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// The tunnel designs this package can build between a node and a relay.
//
// Every design ends the same way - a user connects to a port on the relay and
// the bytes arrive at the same port on the node - they differ in how the
// bytes get from the relay to the node, which is what decides how they behave
// on a bad path:
//
//	gre_frp      GRE over IPv4 + frp inside it (the original design)
//	frp_direct   frp straight over the relay's address, IPv6 or IPv4, no GRE
//	frp_quic     the same, with frp's QUIC (UDP) transport instead of TCP
//	gre_dnat     GRE over IPv4 + kernel DNAT on the relay (no userspace proxy)
//	ip6gre_dnat  GRE over IPv6 + kernel DNAT (needs IPv6 on both boxes)
//	wg_dnat      WireGuard + kernel DNAT (encrypted, plain UDP, passes where
//	             GRE - IP protocol 47 - is filtered)
//
// The frp designs reach IPv4 and IPv6 users alike (frps listens on ::). The
// DNAT designs forward IPv4 only.
const (
	MethodGREFRP     = "gre_frp"
	MethodFRPDirect  = "frp_direct"
	MethodFRPQUIC    = "frp_quic"
	MethodGREDNAT    = "gre_dnat"
	MethodIP6GREDNAT = "ip6gre_dnat"
	MethodWGDNAT     = "wg_dnat"
)

// Methods lists every supported design, in the order the dashboard offers
// them (best first).
var Methods = []string{
	MethodFRPDirect, MethodIP6GREDNAT, MethodWGDNAT, MethodGREDNAT, MethodFRPQUIC, MethodGREFRP,
}

// ValidMethod reports whether m is one of Methods.
func ValidMethod(m string) bool {
	for _, x := range Methods {
		if x == m {
			return true
		}
	}
	return false
}

// RequiresIPv6 reports whether the design needs an IPv6 address on both ends.
func RequiresIPv6(m string) bool { return m == MethodIP6GREDNAT }

func isFRP(m string) bool { return m == MethodFRPDirect || m == MethodFRPQUIC }

// UsesFRP reports whether a tunnel of this method keeps an frp config on each end.
func UsesFRP(method string) bool {
	m := Params{Method: method}.method()
	return m == MethodGREFRP || isFRP(m)
}

// tunnelUnit is the systemd unit that brings the tunnel interface itself up.
func tunnelUnit(p Params) string {
	if p.method() == MethodWGDNAT {
		return "wg-quick@" + p.InterfaceName
	}
	return "gre-" + p.InterfaceName
}

func dnatUnit(p Params) string { return "rapido-dnat-" + p.InterfaceName }

// Provision builds a tunnel of p.Method between the relay and the node.
func Provision(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := p.check(); err != nil {
		return err
	}
	if err := checkPathsFree(ctx, relay, node, p); err != nil {
		return err
	}
	switch m := p.method(); {
	case m == MethodGREFRP:
		return provisionGREFRP(ctx, relay, node, p)
	case isFRP(m):
		return provisionFRPDirect(ctx, relay, node, p)
	case m == MethodGREDNAT || m == MethodIP6GREDNAT:
		return provisionGREDNAT(ctx, relay, node, p)
	case m == MethodWGDNAT:
		return provisionWGDNAT(ctx, relay, node, p)
	default:
		return fmt.Errorf("unknown tunnel method %q", p.Method)
	}
}

// Start brings an already provisioned tunnel up (the inverse of Stop).
func Start(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := p.check(); err != nil {
		return err
	}
	switch m := p.method(); {
	case m == MethodGREFRP:
		return startGREFRP(ctx, relay, node, p)
	case isFRP(m):
		return startFRPDirect(ctx, relay, node, p)
	case m == MethodGREDNAT || m == MethodIP6GREDNAT || m == MethodWGDNAT:
		return startDNAT(ctx, relay, node, p)
	default:
		return fmt.Errorf("unknown tunnel method %q", p.Method)
	}
}

// Stop takes the tunnel down without removing any configuration.
func Stop(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := p.check(); err != nil {
		return err
	}
	switch m := p.method(); {
	case m == MethodGREFRP:
		return stopGREFRP(ctx, relay, node, p)
	case isFRP(m):
		return stopFRPDirect(ctx, relay, node, p)
	case m == MethodGREDNAT || m == MethodIP6GREDNAT || m == MethodWGDNAT:
		return stopDNAT(ctx, relay, node, p)
	default:
		return fmt.Errorf("unknown tunnel method %q", p.Method)
	}
}

// Teardown removes everything Provision created, best effort.
func Teardown(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := p.check(); err != nil {
		return err
	}
	switch m := p.method(); {
	case m == MethodGREFRP:
		return teardownGREFRP(ctx, relay, node, p)
	case isFRP(m):
		return teardownFRPDirect(ctx, relay, node, p)
	case m == MethodGREDNAT || m == MethodIP6GREDNAT || m == MethodWGDNAT:
		return teardownDNAT(ctx, relay, node, p)
	default:
		return fmt.Errorf("unknown tunnel method %q", p.Method)
	}
}

// ---------------------------------------------------------------------------
// shared pieces

// tuningConf is the kernel profile every non-legacy tunnel installs on both
// ends. The one that matters most: every user connection through an frp
// tunnel is its own TCP connection node->relay, so the ephemeral port range
// (28k ports by default) is a hard ceiling on concurrent users. The other
// lines raise buffers/backlogs for 80-130 ms paths and bursts of new
// connections, and keep throughput after idle gaps.
const tuningConf = `# Managed by Rapido-Go (tunnel provisioning)
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.ipv4.tcp_rmem = 4096 131072 67108864
net.ipv4.tcp_wmem = 4096 65536 67108864
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.core.netdev_max_backlog = 50000
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.ip_local_reserved_ports = 22,7000,20000-20199,27000-27999,62050-62060
net.ipv4.tcp_tw_reuse = 1
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_keepalive_time = 120
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 4
net.ipv4.tcp_fastopen = 3
fs.file-max = 2097152
fs.nr_open = 2097152
`

const forwardingConf = `net.ipv4.ip_forward = 1
net.netfilter.nf_conntrack_max = 524288
net.netfilter.nf_conntrack_tcp_timeout_established = 7200
`

// ensureTuning writes the kernel profile and applies it. forwarding adds IP
// forwarding and conntrack sizing for the relay of a DNAT design. A failure
// to apply is not fatal - the tunnel works on stock settings, just slower.
func ensureTuning(ctx context.Context, c *sshexec.Client, forwarding bool) error {
	conf := tuningConf
	if forwarding {
		conf += forwardingConf
		if _, err := c.Run(ctx, "modprobe nf_conntrack 2>/dev/null; echo nf_conntrack > /etc/modules-load.d/nf_conntrack.conf"); err != nil {
			return fmt.Errorf("load conntrack: %w", err)
		}
	}
	if err := c.WriteFile(ctx, "/etc/sysctl.d/90-rapido-tunnel.conf", []byte(conf), "644"); err != nil {
		return fmt.Errorf("write sysctl profile: %w", err)
	}
	c.Run(ctx, "sysctl --system >/dev/null 2>&1 || true")
	return nil
}

// ensureFRPDropIn gives the frp service a CPU priority bump and a lighter
// garbage collector - the proxy moves every byte of every user connection.
func ensureFRPDropIn(ctx context.Context, c *sshexec.Client, kind string) error {
	dir := "/etc/systemd/system/" + kind + "@.service.d"
	if _, err := c.Run(ctx, "mkdir -p "+dir); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	content := "[Service]\nNice=-5\nEnvironment=GOGC=200\nLimitNOFILE=infinity\n"
	if err := c.WriteFile(ctx, dir+"/speed.conf", []byte(content), "644"); err != nil {
		return fmt.Errorf("write %s/speed.conf: %w", dir, err)
	}
	return nil
}

const frpPresentCmd = "test -x /usr/local/bin/frps && test -x /usr/local/bin/frpc && echo present || echo missing"

// installFRP makes sure the frp binaries exist on target. It first tries the
// normal download; a relay inside a filtered network often cannot reach
// GitHub, so when that fails and peer already has the binaries they are
// copied from there instead of failing the whole tunnel.
func installFRP(ctx context.Context, target, peer *sshexec.Client) error {
	err := ensureFRPInstalled(ctx, target)
	if err == nil {
		return nil
	}
	if peer == nil {
		return err
	}
	if out, perr := peer.Run(ctx, frpPresentCmd); perr != nil || strings.TrimSpace(out) != "present" {
		return err
	}
	for _, bin := range []string{"frps", "frpc"} {
		data, rerr := peer.RunOut(ctx, "cat /usr/local/bin/"+bin)
		if rerr != nil || len(data) == 0 {
			return fmt.Errorf("download failed (%v) and copying %s from the other end failed too: %v", err, bin, rerr)
		}
		dst := "/usr/local/bin/" + bin
		if _, werr := target.RunStdin(ctx, "cat > "+dst+".new && chmod 755 "+dst+".new && mv "+dst+".new "+dst, data); werr != nil {
			return fmt.Errorf("copy %s: %w", bin, werr)
		}
	}
	return nil
}

func hostForURL(addr string) string {
	if a, err := netip.ParseAddr(addr); err == nil && a.Is6() {
		return "[" + addr + "]"
	}
	return addr
}

func portList(ports []int32) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// frp_direct / frp_quic

func frpsConfigFor(port int32, token string, quic bool) string {
	cfg := frpsConfig(port, token)
	if quic {
		cfg += fmt.Sprintf("quicBindPort = %d\n", port)
	}
	return cfg
}

func provisionFRPDirect(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	quic := p.method() == MethodFRPQUIC

	// The node sits outside the filtered network, so it downloads frp and the
	// relay falls back to copying it from the node.
	if err := installFRP(ctx, node, nil); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if err := installFRP(ctx, relay, node); err != nil {
		return fmt.Errorf("relay: %w", err)
	}

	if err := ensureSystemdUnit(ctx, relay, "/etc/systemd/system/frps@.service", frpsUnitTemplate); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := ensureFRPDropIn(ctx, relay, "frps"); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := ensureTuning(ctx, relay, false); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	frpsPath := fmt.Sprintf("/root/frp/server/server-%d.toml", p.FRPControlPort)
	if err := ensurePathFree(ctx, relay, frpsPath); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if _, err := relay.Run(ctx, "mkdir -p /root/frp/server"); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := relay.WriteFile(ctx, frpsPath, []byte(frpsConfigFor(p.FRPControlPort, p.FRPToken, quic)), "600"); err != nil {
		return fmt.Errorf("relay: write frps config: %w", err)
	}

	if err := ensureSystemdUnit(ctx, node, "/etc/systemd/system/frpc@.service", frpcUnitTemplate); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if err := ensureFRPDropIn(ctx, node, "frpc"); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if err := ensureTuning(ctx, node, false); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	clientPath := fmt.Sprintf("/root/frp/client/client-%d.toml", p.FRPControlPort)
	if err := ensurePathFree(ctx, node, clientPath); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if _, err := node.Run(ctx, "mkdir -p /root/frp/client"); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	protocol := "tcp"
	if quic {
		protocol = "quic"
	}
	cfg := frpcConfigFor(p.relayOuter(), p.FRPControlPort, p.FRPToken, p.Ports, protocol, 100)
	if err := node.WriteFile(ctx, clientPath, []byte(cfg), "600"); err != nil {
		return fmt.Errorf("node: write frpc config: %w", err)
	}
	return startFRPDirect(ctx, relay, node, p)
}

func startFRPDirect(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if _, err := relay.Run(ctx, fmt.Sprintf("systemctl daemon-reload && systemctl enable --now frps@server-%d", p.FRPControlPort)); err != nil {
		return fmt.Errorf("relay: start frps: %w", err)
	}
	time.Sleep(time.Second)
	if out, err := relay.Run(ctx, fmt.Sprintf("systemctl is-active frps@server-%d", p.FRPControlPort)); err != nil || strings.TrimSpace(out) != "active" {
		return fmt.Errorf("relay: frps is not running (%s): check that port %d is free on the relay", strings.TrimSpace(out), p.FRPControlPort)
	}

	// TCP control only: the QUIC transport is UDP, which a plain connect
	// cannot probe, so for it the proxy-registration check below is the test.
	if p.method() == MethodFRPDirect {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		probe := fmt.Sprintf("timeout 8 bash -c 'exec 3<>/dev/tcp/%s/%d'", p.relayOuter(), p.FRPControlPort)
		if _, err := node.Run(checkCtx, probe); err != nil {
			return fmt.Errorf("node: cannot reach the relay's frp port %s:%d (firewall, or the address is not reachable from the node): %w", p.relayOuter(), p.FRPControlPort, err)
		}
	}

	if _, err := node.Run(ctx, fmt.Sprintf("systemctl daemon-reload && systemctl enable --now frpc@client-%d", p.FRPControlPort)); err != nil {
		return fmt.Errorf("node: start frpc: %w", err)
	}

	return waitProxiesRegistered(ctx, node, p)
}

// waitProxiesRegistered waits until every port is actually registered with the
// relay; an active service that has not logged in is the usual silent failure.
func waitProxiesRegistered(ctx context.Context, node *sshexec.Client, p Params) error {
	want := len(p.Ports)
	var got string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		out, _ := node.Run(ctx, fmt.Sprintf(`journalctl -u frpc@client-%d --since "-60s" --no-pager 2>/dev/null | grep -c "start proxy success"`, p.FRPControlPort))
		got = strings.TrimSpace(out)
		if n := atoiSafe(got); n >= want {
			return nil
		}
	}
	tail, _ := node.Run(ctx, fmt.Sprintf(`journalctl -u frpc@client-%d -n 6 --no-pager 2>/dev/null | cut -c1-200`, p.FRPControlPort))
	return fmt.Errorf("node: frpc registered %s of %d ports with the relay. Last log lines: %s", got, want, strings.TrimSpace(tail))
}

// UpdatePorts points a running, panel-built tunnel at a new set of ports:
// the frp client config (or the relay's forwarding script) is rewritten from
// the parameters the tunnel was built with and the service restarted, so the
// tunnel carries exactly p.Ports afterwards. Users on the tunnel reconnect.
func UpdatePorts(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := p.check(); err != nil {
		return err
	}
	if len(p.Ports) == 0 {
		return fmt.Errorf("a tunnel needs at least one port")
	}
	switch m := p.method(); {
	case m == MethodGREFRP || isFRP(m):
		protocol, serverAddr := "tcp", p.relayOuter()
		if m == MethodFRPQUIC {
			protocol = "quic"
		}
		if m == MethodGREFRP {
			serverAddr = p.relayTunnelIP().String()
		}
		clientPath := fmt.Sprintf("/root/frp/client/client-%d.toml", p.FRPControlPort)
		cfg := frpcConfigFor(serverAddr, p.FRPControlPort, p.FRPToken, p.Ports, protocol, 100)
		if err := node.WriteFile(ctx, clientPath, []byte(cfg), "600"); err != nil {
			return fmt.Errorf("node: write frpc config: %w", err)
		}
		if _, err := node.Run(ctx, fmt.Sprintf("systemctl restart frpc@client-%d", p.FRPControlPort)); err != nil {
			return fmt.Errorf("node: restart frpc: %w", err)
		}
		return waitProxiesRegistered(ctx, node, p)
	case m == MethodGREDNAT || m == MethodIP6GREDNAT || m == MethodWGDNAT:
		script := "/usr/local/sbin/" + dnatUnit(p) + ".sh"
		if err := relay.WriteFile(ctx, script, []byte(dnatScript(p.InterfaceName, p.nodeTunnelIP().String(), p.relayTunnelIP().String(), p.Ports)), "755"); err != nil {
			return fmt.Errorf("relay: write forwarding script: %w", err)
		}
		if _, err := relay.Run(ctx, "systemctl restart "+dnatUnit(p)); err != nil {
			return fmt.Errorf("relay: restart port forwarding: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown tunnel method %q", p.Method)
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func stopFRPDirect(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	var errs []string
	run := func(c *sshexec.Client, label, cmd string) {
		if _, err := c.Run(ctx, cmd); err != nil {
			errs = append(errs, label+": "+err.Error())
		}
	}
	run(node, "node: stop frpc", fmt.Sprintf("systemctl disable --now frpc@client-%d", p.FRPControlPort))
	run(relay, "relay: stop frps", fmt.Sprintf("systemctl disable --now frps@server-%d", p.FRPControlPort))
	if len(errs) > 0 {
		return fmt.Errorf("stop had %d error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

func teardownFRPDirect(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	var errs []string
	run := func(c *sshexec.Client, label, cmd string) {
		if c == nil {
			return
		}
		if _, err := c.Run(ctx, cmd); err != nil {
			errs = append(errs, label+": "+err.Error())
		}
	}
	relayFRP, nodeFRP := relay, node
	if p.KeepRelayFRP {
		relayFRP = nil
	}
	if p.KeepNodeFRP {
		nodeFRP = nil
	}
	run(nodeFRP, "node: remove frpc", fmt.Sprintf("systemctl disable --now frpc@client-%d 2>/dev/null; rm -f /root/frp/client/client-%d.toml /etc/systemd/system/multi-user.target.wants/frpc@client-%d.service", p.FRPControlPort, p.FRPControlPort, p.FRPControlPort))
	run(relayFRP, "relay: remove frps", fmt.Sprintf("systemctl disable --now frps@server-%d 2>/dev/null; rm -f /root/frp/server/server-%d.toml /etc/systemd/system/multi-user.target.wants/frps@server-%d.service", p.FRPControlPort, p.FRPControlPort, p.FRPControlPort))
	for _, c := range []*sshexec.Client{relay, node} {
		if c != nil {
			c.Run(ctx, "systemctl daemon-reload")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("teardown had %d error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// gre_dnat / ip6gre_dnat / wg_dnat: a tunnel, then kernel port forwarding

// ip6greUnit is greUnit for GRE carried over IPv6. The inner addresses stay
// IPv4 /30s, so the DNAT rules and the node's agent are unchanged. The inner
// MTU leaves room for the 40-byte IPv6 header plus GRE.
func ip6greUnit(ifaceName, remoteIP, localIP string, tunnelIP netip.Addr) string {
	return fmt.Sprintf(`[Unit]
Description=GRE-over-IPv6 tunnel (%[1]s), managed by Rapido-Go
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=-/sbin/modprobe ip6_gre
ExecStart=-/sbin/ip -6 tunnel add %[1]s mode ip6gre remote %[2]s local %[3]s ttl 255
ExecStart=-/sbin/ip link set %[1]s mtu 1400 up
ExecStart=-/sbin/ip addr add %[4]s/30 dev %[1]s
ExecStop=-/sbin/ip link del %[1]s

[Install]
WantedBy=multi-user.target
`, ifaceName, remoteIP, localIP, tunnelIP)
}

// dnatScript is the relay's port forwarder: every listed port is rewritten to
// the same port on the node's tunnel address and sent through the tunnel.
// Source NAT makes the node answer to the relay's tunnel address, and the MSS
// clamp keeps every segment inside the tunnel MTU. Rules live in a chain of
// their own, so removing them never touches anything else on the box.
func dnatScript(iface, nodeIP, relayIP string, ports []int32) string {
	return fmt.Sprintf(`#!/bin/bash
# Managed by Rapido-Go (tunnel %[1]s): public port -> node over the tunnel.
set -u
IF=%[1]s
NODE=%[2]s
RELAY=%[3]s
PORTS="%[4]s"
CH=RDNAT_%[1]s
IN=$(ip route show default | awk '/default/{print $5; exit}')
ipt() { iptables "$@"; }
apply() {
  sysctl -qw net.ipv4.ip_forward=1
  modprobe nf_conntrack 2>/dev/null || true
  ipt -t nat -N "$CH" 2>/dev/null || ipt -t nat -F "$CH"
  for p in $PORTS; do ipt -t nat -A "$CH" -p tcp --dport "$p" -j DNAT --to-destination "$NODE:$p"; done
  ipt -t nat -C PREROUTING -i "$IN" -j "$CH" 2>/dev/null || ipt -t nat -I PREROUTING 1 -i "$IN" -j "$CH"
  ipt -t nat -C POSTROUTING -o "$IF" -d "$NODE" -p tcp -j SNAT --to-source "$RELAY" 2>/dev/null \
    || ipt -t nat -A POSTROUTING -o "$IF" -d "$NODE" -p tcp -j SNAT --to-source "$RELAY"
  for d in -o -i; do
    ipt -t mangle -C FORWARD "$d" "$IF" -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null \
      || ipt -t mangle -I FORWARD "$d" "$IF" -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
  done
}
remove() {
  ipt -t nat -D PREROUTING -i "$IN" -j "$CH" 2>/dev/null || true
  ipt -t nat -F "$CH" 2>/dev/null || true
  ipt -t nat -X "$CH" 2>/dev/null || true
  ipt -t nat -D POSTROUTING -o "$IF" -d "$NODE" -p tcp -j SNAT --to-source "$RELAY" 2>/dev/null || true
  for d in -o -i; do
    ipt -t mangle -D FORWARD "$d" "$IF" -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null || true
  done
}
case "${1:-}" in
  apply) apply ;;
  remove) remove ;;
  *) echo "usage: $0 apply|remove" >&2; exit 2 ;;
esac
`, iface, nodeIP, relayIP, portList(ports))
}

func dnatServiceUnit(iface, after string) string {
	return fmt.Sprintf(`[Unit]
Description=Rapido-Go port forwarding for tunnel %[1]s
After=network-online.target %[2]s.service
Wants=network-online.target %[2]s.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/rapido-dnat-%[1]s.sh apply
ExecStop=/usr/local/sbin/rapido-dnat-%[1]s.sh remove

[Install]
WantedBy=multi-user.target
`, iface, after)
}

func provisionGREDNAT(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	ipv6 := p.method() == MethodIP6GREDNAT
	if ipv6 {
		for name, a := range map[string]string{"node": p.nodeOuter(), "relay": p.relayOuter()} {
			addr, err := netip.ParseAddr(a)
			if err != nil || !addr.Is6() {
				return fmt.Errorf("ip6gre_dnat needs an IPv6 address for the %s tunnel end, got %q", name, a)
			}
		}
	}
	greUnitPath := "/etc/systemd/system/gre-" + p.InterfaceName + ".service"
	for label, c := range map[string]*sshexec.Client{"relay": relay, "node": node} {
		if err := ensurePathFree(ctx, c, greUnitPath); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	relayUnit := greUnit(p.InterfaceName, p.nodeOuter(), p.relayOuter(), p.relayTunnelIP())
	nodeUnit := greUnit(p.InterfaceName, p.relayOuter(), p.nodeOuter(), p.nodeTunnelIP())
	if ipv6 {
		relayUnit = ip6greUnit(p.InterfaceName, p.nodeOuter(), p.relayOuter(), p.relayTunnelIP())
		nodeUnit = ip6greUnit(p.InterfaceName, p.relayOuter(), p.nodeOuter(), p.nodeTunnelIP())
	}
	if err := relay.WriteFile(ctx, greUnitPath, []byte(relayUnit), "644"); err != nil {
		return fmt.Errorf("relay: write GRE unit: %w", err)
	}
	if err := node.WriteFile(ctx, greUnitPath, []byte(nodeUnit), "644"); err != nil {
		return fmt.Errorf("node: write GRE unit: %w", err)
	}
	return provisionForwarder(ctx, relay, node, p)
}

// provisionForwarder writes the relay's forwarding script + unit and the
// kernel profile on both ends, then starts everything.
func provisionForwarder(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := ensureTuning(ctx, relay, true); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := ensureTuning(ctx, node, false); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	script := "/usr/local/sbin/" + dnatUnit(p) + ".sh"
	if err := relay.WriteFile(ctx, script, []byte(dnatScript(p.InterfaceName, p.nodeTunnelIP().String(), p.relayTunnelIP().String(), p.Ports)), "755"); err != nil {
		return fmt.Errorf("relay: write forwarding script: %w", err)
	}
	if err := relay.WriteFile(ctx, "/etc/systemd/system/"+dnatUnit(p)+".service", []byte(dnatServiceUnit(p.InterfaceName, tunnelUnit(p))), "644"); err != nil {
		return fmt.Errorf("relay: write forwarding unit: %w", err)
	}
	return startDNAT(ctx, relay, node, p)
}

func wgInstallCmd() string {
	return `command -v wg >/dev/null 2>&1 || { export DEBIAN_FRONTEND=noninteractive; apt-get install -y -qq wireguard-tools >/dev/null 2>&1 || dnf install -y wireguard-tools >/dev/null 2>&1 || yum install -y wireguard-tools >/dev/null 2>&1; }; command -v wg >/dev/null 2>&1 && modprobe wireguard 2>/dev/null; command -v wg >/dev/null 2>&1 && echo ok || echo missing`
}

func wgKeygen(ctx context.Context, c *sshexec.Client, iface string) (string, error) {
	out, err := c.Run(ctx, fmt.Sprintf("umask 077; mkdir -p /etc/wireguard; wg genkey > /etc/wireguard/%[1]s.key && wg pubkey < /etc/wireguard/%[1]s.key", iface))
	if err != nil {
		return "", err
	}
	pub := strings.TrimSpace(out)
	if len(pub) != 44 {
		return "", fmt.Errorf("unexpected public key %q", pub)
	}
	return pub, nil
}

func wgWriteConf(ctx context.Context, c *sshexec.Client, iface, body string) error {
	cmd := fmt.Sprintf("umask 077; k=$(cat /etc/wireguard/%[1]s.key); cat > /etc/wireguard/%[1]s.conf <<WGEOF\n[Interface]\nPrivateKey = $k\n%[2]sWGEOF\n", iface, body)
	_, err := c.Run(ctx, cmd)
	return err
}

func provisionWGDNAT(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	for label, c := range map[string]*sshexec.Client{"relay": relay, "node": node} {
		out, err := c.Run(ctx, wgInstallCmd())
		if err != nil || !strings.Contains(out, "ok") {
			return fmt.Errorf("%s: WireGuard is not installed and could not be installed (apt/dnf unreachable?): %v", label, err)
		}
		if err := ensurePathFree(ctx, c, "/etc/wireguard/"+p.InterfaceName+".conf"); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}
	relayPub, err := wgKeygen(ctx, relay, p.InterfaceName)
	if err != nil {
		return fmt.Errorf("relay: generate WireGuard key: %w", err)
	}
	nodePub, err := wgKeygen(ctx, node, p.InterfaceName)
	if err != nil {
		return fmt.Errorf("node: generate WireGuard key: %w", err)
	}
	// Table = off: the tunnel carries only the forwarded ports, it must never
	// install a default route. MTU 1380 fits an IPv6 underlay as well.
	relayConf := fmt.Sprintf("Address = %s/30\nListenPort = %d\nMTU = 1380\nTable = off\n\n[Peer]\nPublicKey = %s\nAllowedIPs = %s/32\n",
		p.relayTunnelIP(), p.FRPControlPort, nodePub, p.nodeTunnelIP())
	nodeConf := fmt.Sprintf("Address = %s/30\nMTU = 1380\nTable = off\n\n[Peer]\nPublicKey = %s\nEndpoint = %s:%d\nAllowedIPs = %s/32\nPersistentKeepalive = 15\n",
		p.nodeTunnelIP(), relayPub, hostForURL(p.relayOuter()), p.FRPControlPort, p.relayTunnelIP())
	if err := wgWriteConf(ctx, relay, p.InterfaceName, relayConf); err != nil {
		return fmt.Errorf("relay: write WireGuard config: %w", err)
	}
	if err := wgWriteConf(ctx, node, p.InterfaceName, nodeConf); err != nil {
		return fmt.Errorf("node: write WireGuard config: %w", err)
	}
	return provisionForwarder(ctx, relay, node, p)
}

// startDNAT brings the tunnel up on both ends, proves it passes traffic, and
// only then turns on the relay's port forwarding - so a broken tunnel never
// leaves public ports pointing into nothing.
func startDNAT(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if _, err := relay.Run(ctx, "systemctl daemon-reload && systemctl enable --now "+tunnelUnit(p)); err != nil {
		return fmt.Errorf("relay: start tunnel: %w", err)
	}
	if _, err := node.Run(ctx, "systemctl daemon-reload && systemctl enable --now "+tunnelUnit(p)); err != nil {
		return fmt.Errorf("node: start tunnel: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := node.Run(pingCtx, fmt.Sprintf("ping -I %s -c 3 -W 3 %s", p.InterfaceName, p.relayTunnelIP())); err != nil {
		hint := "the tunnel is up but passes no traffic (ping to the relay failed)"
		switch p.method() {
		case MethodGREDNAT, MethodIP6GREDNAT:
			hint += " - GRE is IP protocol 47; a firewall or the provider may be dropping it, try the wg_dnat or frp_direct method"
		case MethodWGDNAT:
			hint += fmt.Sprintf(" - check that UDP port %d is open on the relay", p.FRPControlPort)
		}
		return fmt.Errorf("node: %s: %w", hint, err)
	}
	if _, err := relay.Run(ctx, "systemctl daemon-reload && systemctl enable --now "+dnatUnit(p)); err != nil {
		return fmt.Errorf("relay: start port forwarding: %w", err)
	}
	return nil
}

func stopDNAT(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	var errs []string
	run := func(c *sshexec.Client, label, cmd string) {
		if _, err := c.Run(ctx, cmd); err != nil {
			errs = append(errs, label+": "+err.Error())
		}
	}
	run(relay, "relay: stop port forwarding", "systemctl disable --now "+dnatUnit(p))
	run(node, "node: stop tunnel", "systemctl disable --now "+tunnelUnit(p))
	run(relay, "relay: stop tunnel", "systemctl disable --now "+tunnelUnit(p))
	if len(errs) > 0 {
		return fmt.Errorf("stop had %d error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

func teardownDNAT(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	var errs []string
	run := func(c *sshexec.Client, label, cmd string) {
		if c == nil {
			return
		}
		if _, err := c.Run(ctx, cmd); err != nil {
			errs = append(errs, label+": "+err.Error())
		}
	}
	dn := dnatUnit(p)
	run(relay, "relay: remove port forwarding", fmt.Sprintf("systemctl disable --now %[1]s 2>/dev/null; rm -f /usr/local/sbin/%[1]s.sh /etc/systemd/system/%[1]s.service /etc/systemd/system/multi-user.target.wants/%[1]s.service", dn))
	for label, c := range map[string]*sshexec.Client{"node": node, "relay": relay} {
		if p.method() == MethodWGDNAT {
			run(c, label+": remove WireGuard tunnel", fmt.Sprintf("systemctl disable --now wg-quick@%[1]s 2>/dev/null; rm -f /etc/wireguard/%[1]s.conf /etc/wireguard/%[1]s.key", p.InterfaceName))
		} else {
			run(c, label+": remove GRE tunnel", fmt.Sprintf("systemctl disable --now gre-%[1]s 2>/dev/null; rm -f /etc/systemd/system/gre-%[1]s.service /etc/systemd/system/multi-user.target.wants/gre-%[1]s.service", p.InterfaceName))
		}
	}
	for _, c := range []*sshexec.Client{relay, node} {
		if c != nil {
			c.Run(ctx, "systemctl daemon-reload")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("teardown had %d error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}
