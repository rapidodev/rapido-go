package tunnelprovision

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNodeAndRelayTunnelIPsAreTheUsualDotOneDotTwo(t *testing.T) {
	p := Params{TunnelSubnet: netip.MustParsePrefix("10.100.123.0/30")}
	if got := p.nodeTunnelIP(); got.String() != "10.100.123.1" {
		t.Errorf("nodeTunnelIP = %s, want 10.100.123.1", got)
	}
	if got := p.relayTunnelIP(); got.String() != "10.100.123.2" {
		t.Errorf("relayTunnelIP = %s, want 10.100.123.2", got)
	}
}

func TestGREUnitSwapsRemoteAndLocalBetweenTheTwoSides(t *testing.T) {
	// The relay's unit dials the node and binds the relay's own tunnel
	// address; the node's unit is the exact mirror image. Getting this
	// backwards on either side is exactly the kind of mistake that would
	// only surface as "tunnel doesn't pass traffic", so it's worth pinning
	// directly.
	p := Params{
		InterfaceName: "t1a2b3c",
		NodeAddress:   "176.120.17.11",
		RelayHost:     "5.202.4.182",
		TunnelSubnet:  netip.MustParsePrefix("10.100.123.0/30"),
	}
	relaySide := greUnit(p.InterfaceName, p.NodeAddress, p.RelayHost, p.relayTunnelIP())
	if !strings.Contains(relaySide, "remote 176.120.17.11 local 5.202.4.182") {
		t.Errorf("relay-side GRE unit = %q, want remote=node local=relay", relaySide)
	}
	if !strings.Contains(relaySide, "ip addr add 10.100.123.2/30") {
		t.Errorf("relay-side GRE unit does not bind the relay's own tunnel ip: %q", relaySide)
	}

	nodeSide := greUnit(p.InterfaceName, p.RelayHost, p.NodeAddress, p.nodeTunnelIP())
	if !strings.Contains(nodeSide, "remote 5.202.4.182 local 176.120.17.11") {
		t.Errorf("node-side GRE unit = %q, want remote=relay local=node", nodeSide)
	}
	if !strings.Contains(nodeSide, "ip addr add 10.100.123.1/30") {
		t.Errorf("node-side GRE unit does not bind the node's own tunnel ip: %q", nodeSide)
	}
}

func TestFRPCConfigWritesOneFullyExpandedProxyBlockPerPort(t *testing.T) {
	// The actual regression test for this whole package's reason to exist:
	// a real production outage this session came from frp's own Go-template
	// port-range syntax silently duplicating a port across two [[proxies]]
	// blocks. This asserts the plain-Go-loop replacement produces exactly
	// one block per port, with matching local/remote, and nothing templated.
	cfg := frpcConfig("10.100.123.2", 7005, "sekret", []int32{20001, 20005, 20009})

	if strings.Contains(cfg, "{{") || strings.Contains(cfg, "parseNumberRangePair") {
		t.Errorf("frpcConfig emitted template syntax, want fully expanded blocks: %q", cfg)
	}
	if got := strings.Count(cfg, "[[proxies]]"); got != 3 {
		t.Errorf("proxy block count = %d, want 3 (one per port, no duplicates)", got)
	}
	for _, port := range []int32{20001, 20005, 20009} {
		if strings.Count(cfg, "localPort = "+itoa(port)) != 1 {
			t.Errorf("localPort = %d does not appear exactly once", port)
		}
		if strings.Count(cfg, "remotePort = "+itoa(port)) != 1 {
			t.Errorf("remotePort = %d does not appear exactly once", port)
		}
	}
	if !strings.Contains(cfg, `serverAddr = "10.100.123.2"`) || !strings.Contains(cfg, "serverPort = 7005") {
		t.Errorf("server address/port missing or wrong: %q", cfg)
	}
	if !strings.Contains(cfg, `auth.token = "sekret"`) {
		t.Error("auth token missing")
	}
}

// itoa is a tiny local formatter so the test above reads as plain string
// building rather than threading strconv through every assertion.
func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestFRPSConfigCarriesThePortAndToken(t *testing.T) {
	cfg := frpsConfig(7005, "sekret")
	if !strings.Contains(cfg, "bindPort = 7005") || !strings.Contains(cfg, `auth.token = "sekret"`) {
		t.Errorf("frps config missing port/token: %q", cfg)
	}
}
