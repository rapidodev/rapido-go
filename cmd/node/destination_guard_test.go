package main

import (
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"golang.org/x/net/dns/dnsmessage"

	"github.com/sagernet/sing-vmess/vless"
)

// fakeDNS answers every A query with addr, the way an attacker's domain answers
// with 127.0.0.1, and every other query with nothing.
func fakeDNS(t *testing.T, addr [4]byte) int {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var msg dnsmessage.Message
			if err := msg.Unpack(buf[:n]); err != nil || len(msg.Questions) == 0 {
				continue
			}
			question := msg.Questions[0]
			reply := dnsmessage.Message{
				Header:    dnsmessage.Header{ID: msg.ID, Response: true, Authoritative: true},
				Questions: msg.Questions,
			}
			if question.Type == dnsmessage.TypeA {
				reply.Answers = []dnsmessage.Resource{{
					Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
					Body:   &dnsmessage.AResource{A: addr},
				}}
			}
			packed, err := reply.Pack()
			if err == nil {
				conn.WriteTo(packed, from)
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// echoViaDomain is echoVia to a destination given by name.
func echoViaDomain(nodePort uint16, uuid, domain string, echoPort int) error {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(nodePort))), 2*time.Second)
	if err != nil {
		return err
	}
	defer raw.Close()
	client, err := vless.NewClient(uuid, "", logger.NOP())
	if err != nil {
		return err
	}
	conn, err := client.DialConn(raw, M.Socksaddr{Fqdn: domain, Port: uint16(echoPort)})
	if err != nil {
		return err
	}
	const payload = "ping-through-the-node"
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	buf := make([]byte, len(payload))
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(conn, buf)
	return err
}

func guardedNode(t *testing.T, core *coreSpec) uint16 {
	t.Helper()
	port := freeTCPPort(t)
	startPlanned(t, startRequest{
		Inbounds: []inboundSpec{{Tag: "main", Protocol: "vless", ListenPort: port, Users: []userSpec{{Name: "alice", UUID: testUserA}}}},
		Core:     core,
	})
	return port
}

func withFakeDNS(t *testing.T, core coreSpec) *coreSpec {
	t.Helper()
	core.DNSServers = append(core.DNSServers, dnsServerSpec{Tag: "fake", Type: "udp", Address: "127.0.0.1", Port: fakeDNS(t, [4]byte{127, 0, 0, 1})})
	return &core
}

func TestAProxyUserCannotReachTheNodesOwnNetwork(t *testing.T) {
	withDestinationGuard(t)
	echoHost, echoPort := startEcho(t)

	refused := func(t *testing.T, name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s reached a service on the node's loopback", name)
		}
	}

	t.Run("with no core config at all", func(t *testing.T) {
		port := guardedNode(t, nil)
		refused(t, "an address", echoVia(port, testUserA, echoHost, echoPort))
		refused(t, "localhost", echoViaDomain(port, testUserA, "localhost", echoPort))
	})
	t.Run("a name that resolves to loopback", func(t *testing.T) {
		port := guardedNode(t, withFakeDNS(t, coreSpec{}))
		refused(t, "the name", echoViaDomain(port, testUserA, "rebind.test", echoPort))
	})
	t.Run("a rule sending the inbound straight out", func(t *testing.T) {
		port := guardedNode(t, withFakeDNS(t, coreSpec{
			RoutingRules: []routingRuleSpec{{Inbound: []string{"main"}, OutboundTag: "direct"}},
		}))
		refused(t, "an address", echoVia(port, testUserA, echoHost, echoPort))
		refused(t, "the name", echoViaDomain(port, testUserA, "rebind.test", echoPort))
	})
	t.Run("a rule sending it to a custom direct outbound", func(t *testing.T) {
		port := guardedNode(t, withFakeDNS(t, coreSpec{
			Outbounds:    []outboundSpec{{Tag: "out", Type: "direct"}},
			RoutingRules: []routingRuleSpec{{Inbound: []string{"main"}, OutboundTag: "out"}},
		}))
		refused(t, "the name", echoViaDomain(port, testUserA, "rebind.test", echoPort))
	})
	t.Run("a rule that blocks private addresses opens nothing", func(t *testing.T) {
		port := guardedNode(t, &coreSpec{
			RoutingRules: []routingRuleSpec{{IPIsPrivate: true, OutboundTag: "block"}},
		})
		refused(t, "an address", echoVia(port, testUserA, echoHost, echoPort))
	})
}

// The guard must not stand between an admin and a range they routed on purpose,
// and a resolved name has to still connect, or every site would have broken.
func TestAnAdminCanStillRouteAPrivateRangeOnPurpose(t *testing.T) {
	withDestinationGuard(t)
	echoHost, echoPort := startEcho(t)

	t.Run("named by ip_cidr", func(t *testing.T) {
		port := guardedNode(t, withFakeDNS(t, coreSpec{
			RoutingRules: []routingRuleSpec{{IPCIDR: []string{"127.0.0.0/8"}, OutboundTag: "direct"}},
		}))
		if err := echoVia(port, testUserA, echoHost, echoPort); err != nil {
			t.Errorf("an address in the routed range: %v", err)
		}
		if err := echoViaDomain(port, testUserA, "rebind.test", echoPort); err != nil {
			t.Errorf("a name resolving into the routed range: %v", err)
		}
	})
	t.Run("a range outside it stays closed", func(t *testing.T) {
		port := guardedNode(t, &coreSpec{
			RoutingRules: []routingRuleSpec{{IPCIDR: []string{"10.10.34.0/24"}, OutboundTag: "direct"}},
		})
		if err := echoVia(port, testUserA, echoHost, echoPort); err == nil {
			t.Error("loopback was reachable though only 10.10.34.0/24 was routed")
		}
	})
	t.Run("all of them by ip_is_private", func(t *testing.T) {
		port := guardedNode(t, &coreSpec{
			RoutingRules: []routingRuleSpec{{IPIsPrivate: true, OutboundTag: "direct"}},
		})
		if err := echoVia(port, testUserA, echoHost, echoPort); err != nil {
			t.Errorf("an address: %v", err)
		}
	})
}

func TestTheGuardOnlyLooksUpNamesLeavingThroughTheNodesOwnRoutes(t *testing.T) {
	outbounds := map[string]outboundSpec{
		"plain":    {Tag: "plain", Type: "direct"},
		"bound":    {Tag: "bound", Type: "direct", BindInterface: "wg0"},
		"fallback": {Tag: "fallback", Type: "direct", BindInterface: "wg0", DirectFallback: true},
		"exit":     {Tag: "exit", Type: "socks", Server: "1.1.1.1", ServerPort: 1080},
		"pick":     {Tag: "pick", Type: "selector", Outbounds: []string{"exit", "plain"}},
		"exits":    {Tag: "exits", Type: "urltest", Outbounds: []string{"exit", "bound"}},
		"loop":     {Tag: "loop", Type: "selector", Outbounds: []string{"loop"}},
	}
	for tag, want := range map[string]bool{
		"direct": true, "plain": true, "fallback": true, "pick": true,
		"bound": false, "exit": false, "exits": false, "loop": false, "block": false, "unknown": false,
	} {
		if got := reachesNodeNetwork(tag, outbounds, map[string]bool{}); got != want {
			t.Errorf("reachesNodeNetwork(%q) = %v, want %v", tag, got, want)
		}
	}
}

func TestPublicAddressesAreNotCoveredByTheExtraRanges(t *testing.T) {
	for _, public := range []string{"1.1.1.1", "8.8.8.8", "185.199.108.153", "100.128.0.1", "198.20.0.1", "168.63.129.17"} {
		addr := netip.MustParseAddr(public)
		for _, cidr := range nonPublicCIDRs {
			if netip.MustParsePrefix(cidr).Contains(addr) {
				t.Errorf("%s is public but inside %s", public, cidr)
			}
		}
	}
}
