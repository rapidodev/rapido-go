package tunnelprovision

import (
	"net/netip"
	"strings"
	"testing"
)

func TestEveryListedMethodIsValidAndUnknownOnesAreNot(t *testing.T) {
	for _, m := range Methods {
		if !ValidMethod(m) {
			t.Errorf("%q is listed in Methods but ValidMethod rejects it", m)
		}
	}
	for _, bad := range []string{"", "gre", "frp", "wireguard", "GRE_FRP"} {
		if ValidMethod(bad) {
			t.Errorf("ValidMethod(%q) = true, want false", bad)
		}
	}
}

func TestOnlyTheIPv6GREMethodRequiresIPv6(t *testing.T) {
	for _, m := range Methods {
		if got, want := RequiresIPv6(m), m == MethodIP6GREDNAT; got != want {
			t.Errorf("RequiresIPv6(%q) = %v, want %v", m, got, want)
		}
	}
}

func TestEmptyMethodMeansTheOriginalGREFRPDesign(t *testing.T) {
	// Rows created before methods existed carry no method in Params; they
	// must keep taking the gre_frp path.
	if got := (Params{}).method(); got != MethodGREFRP {
		t.Errorf("empty Method resolves to %q, want %q", got, MethodGREFRP)
	}
	if got := (Params{Method: MethodWGDNAT}).method(); got != MethodWGDNAT {
		t.Errorf("explicit Method lost: %q", got)
	}
}

func TestTunnelAddressesFallBackToTheSSHAddresses(t *testing.T) {
	p := Params{NodeAddress: "176.120.17.46", RelayHost: "185.124.175.44"}
	if p.nodeOuter() != "176.120.17.46" || p.relayOuter() != "185.124.175.44" {
		t.Errorf("without overrides the outer addresses must be the SSH ones, got %q / %q", p.nodeOuter(), p.relayOuter())
	}
	p.NodeTunnelAddr, p.RelayTunnelAddr = "2a12:de40:51::4aa4", "2a05:9080:3::fe4"
	if p.nodeOuter() != "2a12:de40:51::4aa4" || p.relayOuter() != "2a05:9080:3::fe4" {
		t.Errorf("overrides ignored: %q / %q", p.nodeOuter(), p.relayOuter())
	}
}

func TestFRPQUICConfigsSwitchTheTransportAndOpenTheUDPPort(t *testing.T) {
	server := frpsConfigFor(27001, "tok", true)
	if !strings.Contains(server, "quicBindPort = 27001") || !strings.Contains(server, "bindPort = 27001") {
		t.Errorf("QUIC frps config must listen on UDP and TCP: %q", server)
	}
	if plain := frpsConfigFor(27001, "tok", false); strings.Contains(plain, "quicBindPort") {
		t.Errorf("plain frps config must not open a QUIC port: %q", plain)
	}

	client := frpcConfigFor("2a05:9080:3::fe4", 27001, "tok", []int32{20001, 20005}, "quic", 100)
	for _, want := range []string{
		`serverAddr = "2a05:9080:3::fe4"`, `transport.protocol = "quic"`, "transport.poolCount = 100",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("QUIC frpc config missing %q: %q", want, client)
		}
	}
	if got := strings.Count(client, "[[proxies]]"); got != 2 {
		t.Errorf("proxy block count = %d, want 2", got)
	}
}

func TestClassicFRPCConfigNowKeepsAHundredWorkConnectionsReady(t *testing.T) {
	if cfg := frpcConfig("10.100.0.2", 27000, "t", []int32{20001}); !strings.Contains(cfg, "transport.poolCount = 100") || !strings.Contains(cfg, `transport.protocol = "tcp"`) {
		t.Errorf("frpcConfig must stay TCP with a 100-connection pool: %q", cfg)
	}
}

func TestGREUnitClampsTheMTUBelowTheUnderlay(t *testing.T) {
	unit := greUnit("t1a2b3c", "1.2.3.4", "5.6.7.8", netip.MustParseAddr("10.100.0.1"))
	if !strings.Contains(unit, "mtu 1452") {
		t.Errorf("GRE unit must set an MTU that fits a 1476-byte underlay: %q", unit)
	}
}

func TestIP6GREUnitUsesAnIPv6UnderlayAndIPv4Inside(t *testing.T) {
	unit := ip6greUnit("t1a2b3c", "2a12:de40:51::1", "2a05:9080:3::1", netip.MustParseAddr("10.100.0.2"))
	for _, want := range []string{
		"ip -6 tunnel add t1a2b3c mode ip6gre remote 2a12:de40:51::1 local 2a05:9080:3::1",
		"ip addr add 10.100.0.2/30 dev t1a2b3c",
		"mtu 1400",
		"ip link del t1a2b3c",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("ip6gre unit missing %q: %q", want, unit)
		}
	}
}

func TestDNATScriptForwardsEveryPortAndOnlyThroughItsOwnChain(t *testing.T) {
	script := dnatScript("t1a2b3c", "10.100.0.1", "10.100.0.2", []int32{20001, 20005, 20110})
	for _, want := range []string{
		`NODE=10.100.0.1`, `RELAY=10.100.0.2`, `IF=t1a2b3c`, `PORTS="20001 20005 20110"`,
		"CH=RDNAT_t1a2b3c",
		`--to-destination "$NODE:$p"`,
		`-j SNAT --to-source "$RELAY"`,
		"--clamp-mss-to-pmtu",
		"apply)", "remove)",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("DNAT script missing %q:\n%s", want, script)
		}
	}
	// Removing a tunnel must never flush anything but its own chain.
	if strings.Contains(script, "iptables -F") || strings.Contains(script, "-t nat -F PREROUTING") {
		t.Errorf("DNAT script must not flush shared chains:\n%s", script)
	}
}

func TestDNATUnitRunsTheScriptAfterTheTunnelUnit(t *testing.T) {
	unit := dnatServiceUnit("t1a2b3c", "wg-quick@t1a2b3c")
	for _, want := range []string{
		"After=network-online.target wg-quick@t1a2b3c.service",
		"ExecStart=/usr/local/sbin/rapido-dnat-t1a2b3c.sh apply",
		"ExecStop=/usr/local/sbin/rapido-dnat-t1a2b3c.sh remove",
		"RemainAfterExit=yes",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("DNAT unit missing %q: %q", want, unit)
		}
	}
}

func TestTunnelUnitNamesDependOnTheMethod(t *testing.T) {
	if got := tunnelUnit(Params{InterfaceName: "tabc123", Method: MethodWGDNAT}); got != "wg-quick@tabc123" {
		t.Errorf("wg unit = %q", got)
	}
	for _, m := range []string{MethodGREDNAT, MethodIP6GREDNAT, MethodGREFRP, ""} {
		if got := tunnelUnit(Params{InterfaceName: "tabc123", Method: m}); got != "gre-tabc123" {
			t.Errorf("unit for method %q = %q, want gre-tabc123", m, got)
		}
	}
}

func TestHostForURLBracketsOnlyIPv6(t *testing.T) {
	cases := map[string]string{
		"185.124.175.44":    "185.124.175.44",
		"relay.example.org": "relay.example.org",
		"2a05:9080:3::fe4":  "[2a05:9080:3::fe4]",
	}
	for in, want := range cases {
		if got := hostForURL(in); got != want {
			t.Errorf("hostForURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPortListAndAtoiSafe(t *testing.T) {
	if got := portList([]int32{20001, 20005}); got != "20001 20005" {
		t.Errorf("portList = %q", got)
	}
	for in, want := range map[string]int{"12": 12, "12\n": 12, "": 0, "x": 0, "7 of 9": 7} {
		if got := atoiSafe(in); got != want {
			t.Errorf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTuningProfileRaisesTheEphemeralPortCeilingAndReservesServicePorts(t *testing.T) {
	// Every frp user connection is its own node->relay TCP connection, so the
	// ephemeral range is the cap on concurrent users - this is the line the
	// whole profile exists for. The reserved list keeps those ports from being
	// handed out as ephemeral ones before the services bind them.
	for _, want := range []string{
		"net.ipv4.ip_local_port_range = 1024 65535",
		"net.ipv4.ip_local_reserved_ports = 22,7000,20000-20199,27000-27999",
		"net.ipv4.tcp_congestion_control = bbr",
		"net.core.somaxconn = 65535",
	} {
		if !strings.Contains(tuningConf, want) {
			t.Errorf("tuning profile missing %q", want)
		}
	}
	if !strings.Contains(forwardingConf, "net.ipv4.ip_forward = 1") {
		t.Error("forwarding profile must enable ip_forward")
	}
}
