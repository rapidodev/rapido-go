// Package tunnelprovision builds and tears down a GRE+FRP tunnel between
// one of this fleet's own nodes and an external relay box, over SSH to
// both ends (internal/sshexec) - the scripted, parameterized version of
// the exact manual sequence this session's relay work went through by
// hand for all four of the fleet's existing relays: a GRE interface on
// each side (systemd-managed so it survives a reboot), frps on the relay,
// frpc on the node forwarding a fixed list of ports.
//
// Deliberately NOT using frp's own Go-template port-range syntax
// ({{- range ... parseNumberRangePair ... }}) the existing hand-set-up
// configs use for their [[proxies]] list: that syntax's two-list pairing
// is exactly what caused a real production outage this session (a
// duplicated port silently crash-looped frpc) - this package writes one
// fully-expanded [[proxies]] block per port instead, in plain Go, so the
// data a human would have to eyeball to catch that bug is instead just a
// slice this code iterates correctly by construction.
package tunnelprovision

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// Params is everything one GRE+FRP tunnel needs, already resolved to
// concrete values - allocation (which subnet, which control port, the
// random token) happens in the HTTP handler, not here, so this package
// stays a pure "given these exact values, make it so" executor.
type Params struct {
	InterfaceName  string
	NodeAddress    string // the node's real IP (nodes.address)
	RelayHost      string // the relay's real IP/hostname - also the GRE local address on that side
	TunnelSubnet   netip.Prefix
	FRPControlPort int32
	FRPToken       string
	Ports          []int32
}

// nodeTunnelIP/relayTunnelIP are the two usable addresses of the /30:
// network+1 for the node, network+2 for the relay - matching every GRE
// tunnel already running on this fleet (see cmd/node/cli.go's own
// interface-naming doc comment for the matching node-side convention this
// mirrors).
func (p Params) nodeTunnelIP() netip.Addr  { return addOffset(p.TunnelSubnet.Addr(), 1) }
func (p Params) relayTunnelIP() netip.Addr { return addOffset(p.TunnelSubnet.Addr(), 2) }

func addOffset(a netip.Addr, n int) netip.Addr {
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v += uint32(n)
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

const (
	frpsUnitTemplate = `[Unit]
Description=FRP Server Service (%i)
Documentation=https://gofrp.org/en/docs/overview/
After=network.target nss-lookup.target network-online.target

[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
ExecStart=/usr/local/bin/frps -c /root/frp/server/%i.toml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=10s
LimitNOFILE=infinity

[Install]
WantedBy=multi-user.target
`
	frpcUnitTemplate = `[Unit]
Description=FRP Client Service (%i)
Documentation=https://gofrp.org/en/docs/overview/
After=network.target nss-lookup.target network-online.target

[Service]
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
ExecStart=/usr/local/bin/frpc -c /root/frp/client/%i.toml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=10s
LimitNOFILE=infinity

[Install]
WantedBy=multi-user.target
`
	// frpVersion/frpDownloadURL mirror the exact release already installed
	// by hand on every relay/node this session touched (frp_0.71.0_linux_
	// amd64) - kept in sync with that, not "whatever is newest today", so
	// a panel-provisioned tunnel behaves identically to the ones already
	// running rather than mixing versions across the fleet.
	frpVersion = "0.71.0"
)

func frpDownloadURL() string {
	return fmt.Sprintf("https://github.com/fatedier/frp/releases/download/v%s/frp_%s_linux_amd64.tar.gz", frpVersion, frpVersion)
}

// ensureFRPInstalled installs the frp binaries and both systemd templates
// if they are not already present - idempotent, so running it against one
// of the fleet's existing relays (which already has them, installed by
// hand) is a silent no-op, not a reinstall.
func ensureFRPInstalled(ctx context.Context, c *sshexec.Client) error {
	out, err := c.Run(ctx, "test -x /usr/local/bin/frps && test -x /usr/local/bin/frpc && echo present || echo missing")
	if err != nil {
		return fmt.Errorf("check for frp binaries: %w", err)
	}
	if strings.TrimSpace(out) == "present" {
		return nil
	}
	installCmd := fmt.Sprintf(`set -e
cd /tmp
curl -fsSL %s -o frp.tar.gz
tar -xzf frp.tar.gz
cd frp_%s_linux_amd64
install -m 755 frps /usr/local/bin/frps
install -m 755 frpc /usr/local/bin/frpc
cd /tmp
rm -rf frp.tar.gz frp_%s_linux_amd64
`, frpDownloadURL(), frpVersion, frpVersion)
	if _, err := c.Run(ctx, installCmd); err != nil {
		return fmt.Errorf("install frp binaries: %w", err)
	}
	return nil
}

func ensureSystemdUnit(ctx context.Context, c *sshexec.Client, path, content string) error {
	out, err := c.Run(ctx, "test -f "+path+" && echo present || echo missing")
	if err != nil {
		return fmt.Errorf("check for %s: %w", path, err)
	}
	if strings.TrimSpace(out) == "present" {
		return nil
	}
	if err := c.WriteFile(ctx, path, []byte(content), "644"); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func greUnit(ifaceName, remoteIP, localIP string, tunnelIP netip.Addr) string {
	return fmt.Sprintf(`[Unit]
Description=GRE tunnel (%s), managed by Rapido-Go
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=-/sbin/ip tunnel add %s mode gre remote %s local %s ttl 255
ExecStart=-/sbin/ip link set %s up
ExecStart=-/sbin/ip addr add %s/30 dev %s
ExecStop=-/sbin/ip link del %s

[Install]
WantedBy=multi-user.target
`, ifaceName, ifaceName, remoteIP, localIP, ifaceName, tunnelIP, ifaceName, ifaceName)
}

func frpsConfig(port int32, token string) string {
	return fmt.Sprintf(`bindAddr = "::"
bindPort = %d
transport.heartbeatTimeout = 90
transport.maxPoolCount = 65535
transport.tcpMux = false
transport.tcpMuxKeepaliveInterval = 10
transport.tcpKeepalive = 120
auth.method = "token"
auth.token = "%s"
`, port, token)
}

func frpcConfig(serverAddr string, port int32, token string, ports []int32) string {
	var b strings.Builder
	fmt.Fprintf(&b, `serverAddr = "%s"
serverPort = %d

loginFailExit = false

auth.method = "token"
auth.token = "%s"

transport.protocol = "tcp"
transport.tcpMux = false
transport.tcpMuxKeepaliveInterval = 10
transport.dialServerTimeout = 10
transport.dialServerKeepalive = 120
transport.poolCount = 20
transport.heartbeatInterval = 30
transport.heartbeatTimeout = 90
transport.tls.enable = false
transport.quic.keepalivePeriod = 10
transport.quic.maxIdleTimeout = 30
transport.quic.maxIncomingStreams = 100000
`, serverAddr, port, token)
	for _, p := range ports {
		fmt.Fprintf(&b, `
[[proxies]]
name = "tcp-%d"
type = "tcp"
localIP = "127.0.0.1"
localPort = %d
remotePort = %d
transport.useEncryption = false
transport.useCompression = false
`, p, p, p)
	}
	return b.String()
}

// Provision brings both ends of one GRE+FRP tunnel up. Order matters: the
// relay side (GRE + frps) is fully up before the node side starts, so the
// node-side GRE bring-up's own connectivity check (pinging the relay's
// tunnel address) has something real to reach - a node-first order would
// make that check meaningless on a first-time setup.
func Provision(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	if err := ensureFRPInstalled(ctx, relay); err != nil {
		return fmt.Errorf("relay: %w", err)
	}
	if err := ensureSystemdUnit(ctx, relay, "/etc/systemd/system/frps@.service", frpsUnitTemplate); err != nil {
		return fmt.Errorf("relay: %w", err)
	}

	greUnitPath := "/etc/systemd/system/gre-" + p.InterfaceName + ".service"
	relayGRE := greUnit(p.InterfaceName, p.NodeAddress, p.RelayHost, p.relayTunnelIP())
	if err := relay.WriteFile(ctx, greUnitPath, []byte(relayGRE), "644"); err != nil {
		return fmt.Errorf("relay: write GRE unit: %w", err)
	}
	if _, err := relay.Run(ctx, "systemctl daemon-reload && systemctl enable --now gre-"+p.InterfaceName); err != nil {
		return fmt.Errorf("relay: start GRE tunnel: %w", err)
	}

	frpsPath := fmt.Sprintf("/root/frp/server/server-%d.toml", p.FRPControlPort)
	if err := relay.WriteFile(ctx, frpsPath, []byte(frpsConfig(p.FRPControlPort, p.FRPToken)), "600"); err != nil {
		return fmt.Errorf("relay: write frps config: %w", err)
	}
	if _, err := relay.Run(ctx, fmt.Sprintf("systemctl daemon-reload && systemctl enable --now frps@server-%d", p.FRPControlPort)); err != nil {
		return fmt.Errorf("relay: start frps: %w", err)
	}

	if err := ensureFRPInstalled(ctx, node); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if err := ensureSystemdUnit(ctx, node, "/etc/systemd/system/frpc@.service", frpcUnitTemplate); err != nil {
		return fmt.Errorf("node: %w", err)
	}

	nodeGRE := greUnit(p.InterfaceName, p.RelayHost, p.NodeAddress, p.nodeTunnelIP())
	if err := node.WriteFile(ctx, greUnitPath, []byte(nodeGRE), "644"); err != nil {
		return fmt.Errorf("node: write GRE unit: %w", err)
	}
	if _, err := node.Run(ctx, "systemctl daemon-reload && systemctl enable --now gre-"+p.InterfaceName); err != nil {
		return fmt.Errorf("node: start GRE tunnel: %w", err)
	}

	// The one real correctness check before frpc is even started: if the
	// node can't ping the relay's tunnel address, frpc would just sit in a
	// reconnect loop with no clearer symptom than "it's not working" -
	// exactly what every manual round of this session's relay debugging
	// started from. Catching it here, synchronously, turns that into an
	// immediate, specific error instead of a silent background failure.
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := node.Run(pingCtx, fmt.Sprintf("ping -I %s -c 3 -W 2 %s", p.InterfaceName, p.relayTunnelIP())); err != nil {
		return fmt.Errorf("node: GRE tunnel is up but not passing traffic (ping to the relay failed): %w", err)
	}

	clientPath := fmt.Sprintf("/root/frp/client/client-%d.toml", p.FRPControlPort)
	frpcContent := frpcConfig(p.relayTunnelIP().String(), p.FRPControlPort, p.FRPToken, p.Ports)
	if err := node.WriteFile(ctx, clientPath, []byte(frpcContent), "600"); err != nil {
		return fmt.Errorf("node: write frpc config: %w", err)
	}
	if _, err := node.Run(ctx, fmt.Sprintf("systemctl daemon-reload && systemctl enable --now frpc@client-%d", p.FRPControlPort)); err != nil {
		return fmt.Errorf("node: start frpc: %w", err)
	}

	// frpc needs a moment to actually dial frps and register its proxies -
	// checking immediately after `systemctl enable --now` returns would
	// read the service's startup state, not whether it actually connected.
	time.Sleep(3 * time.Second)
	status, err := node.Run(ctx, fmt.Sprintf("systemctl is-active frpc@client-%d", p.FRPControlPort))
	if err != nil || strings.TrimSpace(status) != "active" {
		return fmt.Errorf("node: frpc did not stay running (status: %s): %w", strings.TrimSpace(status), err)
	}

	return nil
}

// Teardown removes everything Provision created, best-effort: every step
// runs even if an earlier one fails (a half-provisioned tunnel, or one
// whose relay is unreachable, must still be deletable), with every error
// collected rather than stopping at the first one.
func Teardown(ctx context.Context, relay, node *sshexec.Client, p Params) error {
	var errs []string
	run := func(c *sshexec.Client, label, cmd string) {
		if c == nil {
			return
		}
		if _, err := c.Run(ctx, cmd); err != nil {
			errs = append(errs, label+": "+err.Error())
		}
	}

	run(relay, "relay: stop frps", fmt.Sprintf("systemctl disable --now frps@server-%d 2>/dev/null; rm -f /root/frp/server/server-%d.toml /etc/systemd/system/multi-user.target.wants/frps@server-%d.service", p.FRPControlPort, p.FRPControlPort, p.FRPControlPort))
	run(relay, "relay: stop GRE", "systemctl disable --now gre-"+p.InterfaceName+" 2>/dev/null; rm -f /etc/systemd/system/gre-"+p.InterfaceName+".service")

	run(node, "node: stop frpc", fmt.Sprintf("systemctl disable --now frpc@client-%d 2>/dev/null; rm -f /root/frp/client/client-%d.toml /etc/systemd/system/multi-user.target.wants/frpc@client-%d.service", p.FRPControlPort, p.FRPControlPort, p.FRPControlPort))
	run(node, "node: stop GRE", "systemctl disable --now gre-"+p.InterfaceName+" 2>/dev/null; rm -f /etc/systemd/system/gre-"+p.InterfaceName+".service")

	if relay != nil {
		relay.Run(ctx, "systemctl daemon-reload")
	}
	if node != nil {
		node.Run(ctx, "systemctl daemon-reload")
	}

	if len(errs) > 0 {
		return fmt.Errorf("teardown had %d error(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}
