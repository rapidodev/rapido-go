package tunneldiscover

import (
	"reflect"
	"strings"
	"testing"

	"github.com/legendary1205/rapido-go/internal/tunnelprovision"
)

// The fixtures below are copied from real nodes of this fleet.

const frpcTemplateConfig = `serverAddr = "127.0.0.1"
serverPort = 7001
loginFailExit = false
auth.method = "token"
auth.token = "***"
transport.protocol = "tcp"
transport.tcpMux = false
transport.poolCount = 100
transport.heartbeatInterval = 10
{{- range $_, $v := parseNumberRangePair "20001,20005,20009,20013,20110,20111,20112,20113,20114,20115,20116,20117" "20001,20005,20009,20013,20110,20111,20112,20113,20114,20115,20116,20117" }}
[[proxies]]
name = "tcp-{{ $v.First }}"
type = "tcp"
localIP = "127.0.0.1"
localPort = {{ $v.First }}
remotePort = {{ $v.Second }}
{{- end }}
`

const haproxyConfig = `global
    maxconn 200000
    stats socket /run/haproxy/admin.sock mode 600 level admin

defaults
    mode tcp
    timeout connect 4s

frontend frp_in
    bind 127.0.0.1:7001
    default_backend frp_relay

backend frp_relay
    option splice-auto
    default-server inter 1s fall 2 rise 10
    server relay_v6 [2001:db8:3::fe4]:7000 check
    server relay_v4 203.0.113.82:7000 check backup
`

func TestParseFRPClientReadsThePortsOutOfTheTemplateLine(t *testing.T) {
	c := ParseFRPClient(frpcTemplateConfig)
	if c.ServerAddr != "127.0.0.1" || c.ServerPort != 7001 || c.Protocol != "tcp" || c.PoolCount != 100 {
		t.Errorf("unexpected client config: %+v", c)
	}
	want := []int32{20001, 20005, 20009, 20013, 20110, 20111, 20112, 20113, 20114, 20115, 20116, 20117}
	if !reflect.DeepEqual(c.RemotePorts, want) {
		t.Errorf("remote ports = %v, want %v", c.RemotePorts, want)
	}
}

func TestParseFRPClientReadsExpandedProxyBlocksAndRanges(t *testing.T) {
	c := ParseFRPClient("serverAddr = \"10.100.0.2\"\nserverPort = 27000\ntransport.protocol = \"quic\"\n[[proxies]]\nremotePort = 20005\n[[proxies]]\nremotePort = 20001\n[[proxies]]\nremotePort = 20001\n")
	if c.Protocol != "quic" || !reflect.DeepEqual(c.RemotePorts, []int32{20001, 20005}) {
		t.Errorf("got %+v, want quic and de-duplicated sorted ports", c)
	}
	if got := ParseFRPClient(`{{ range $_, $v := parseNumberRangePair "20100-20102,20200" "20100-20102,20200" }}`).RemotePorts; !reflect.DeepEqual(got, []int32{20100, 20101, 20102, 20200}) {
		t.Errorf("range expansion = %v", got)
	}
}

func TestParseHAProxyFindsTheFrontendBackendAndSocket(t *testing.T) {
	ha := ParseHAProxy(haproxyConfig)
	if ha.Socket != "/run/haproxy/admin.sock" {
		t.Errorf("socket = %q", ha.Socket)
	}
	if ha.Frontend["127.0.0.1:7001"] != "frp_relay" {
		t.Errorf("frontend map = %v", ha.Frontend)
	}
	want := []HAServer{
		{Name: "relay_v6", Addr: "[2001:db8:3::fe4]:7000", Backup: false},
		{Name: "relay_v4", Addr: "203.0.113.82:7000", Backup: true},
	}
	if !reflect.DeepEqual(ha.Backends["frp_relay"], want) {
		t.Errorf("servers = %+v, want %+v", ha.Backends["frp_relay"], want)
	}
}

func TestAnalyzeRecognisesFRPWithHAProxyFailover(t *testing.T) {
	res := Analyze(NodeFacts{
		Route4:  "default via 198.51.100.1 dev eth0 proto static\n",
		FRPC:    []ConfigFile{{Path: "/root/frp/client/client-7000.toml", Content: frpcTemplateConfig}},
		HAProxy: &ConfigFile{Path: "/etc/haproxy/haproxy.cfg", Content: haproxyConfig},
		WGDump:  "croatia\tPRIV\tPUB\t51820\toff\ncroatia\tPEER\t(none)\t154.47.29.15:51820\t0.0.0.0/0,::/0\t1\t2\t3\t25\n",
	}, nil)
	if !res.Found || res.Method != tunnelprovision.MethodFRPDirect {
		t.Fatalf("verdict = %+v", res)
	}
	if res.ControlPort != 7000 {
		t.Errorf("control port = %d, want the relay's 7000 (not the local 7001)", res.ControlPort)
	}
	if res.Details.HAProxy == nil || res.Details.HAProxy.Socket != "/run/haproxy/admin.sock" || len(res.Details.HAProxy.Servers) != 2 {
		t.Errorf("haproxy details = %+v", res.Details.HAProxy)
	}
	if !reflect.DeepEqual(res.Details.RelayAddrs, []string{"2001:db8:3::fe4", "203.0.113.82"}) {
		t.Errorf("relay addrs = %v, want IPv6 primary then IPv4 backup", res.Details.RelayAddrs)
	}
	if res.RelayHost != "203.0.113.82" {
		t.Errorf("SSH host = %q, want the IPv4 address", res.RelayHost)
	}
	if len(res.Ports) != 12 || res.Details.NodeNIC != "eth0" {
		t.Errorf("ports=%d nic=%q", len(res.Ports), res.Details.NodeNIC)
	}
	if !strings.Contains(res.Details.Protocol, "HAProxy") || !strings.Contains(res.Details.Protocol, "IPv6 with IPv4 backup") {
		t.Errorf("protocol label = %q", res.Details.Protocol)
	}
	// A VPN exit (allowed 0.0.0.0/0) must never be mistaken for a tunnel.
	if res.Details.Interface != "" {
		t.Errorf("a VPN exit interface was taken for the tunnel: %q", res.Details.Interface)
	}
}

func TestAnalyzeRecognisesPlainFRPOverIPv6(t *testing.T) {
	res := Analyze(NodeFacts{FRPC: []ConfigFile{{Path: "/p", Content: "serverAddr = \"2001:db8:3::12c8\"\nserverPort = 7000\n[[proxies]]\nremotePort = 20003\n"}}}, nil)
	if !res.Found || res.Method != tunnelprovision.MethodFRPDirect || res.RelayHost != "2001:db8:3::12c8" {
		t.Fatalf("verdict = %+v", res)
	}
	if res.Details.HAProxy != nil || !strings.Contains(res.Details.Protocol, "IPv6") {
		t.Errorf("details = %+v", res.Details)
	}
}

func TestAnalyzeRecognisesGREPlusFRPThroughTheInnerAddress(t *testing.T) {
	res := Analyze(NodeFacts{
		Addrs:    "6: t090672    inet 10.100.0.9/30 scope global t090672\\       valid_lft forever\n2: eth0    inet 198.51.100.10/24 brd 198.51.100.255 scope global eth0\n",
		IPTunnel: "t090672: gre/ip remote 203.0.113.77 local 198.51.100.10 ttl 255\ngre0: gre/ip remote any local any ttl inherit nopmtudisc\nvti0: ip/ip remote 185.126.12.17 local 198.51.100.10 ttl inherit nopmtudisc key 42\n",
		FRPC:     []ConfigFile{{Path: "/p", Content: "serverAddr = \"10.100.0.10\"\nserverPort = 27000\n[[proxies]]\nremotePort = 20002\n"}},
	}, nil)
	if res.Method != tunnelprovision.MethodGREFRP || res.Interface != "t090672" || res.RelayHost != "203.0.113.77" {
		t.Fatalf("verdict = %+v", res)
	}
}

func TestAnalyzeRecognisesForwardingDesignsFromTheInterface(t *testing.T) {
	gre := Analyze(NodeFacts{IPTunnel: "t5r82: gre/ip remote 203.0.113.82 local 198.51.100.46 ttl 255\n"}, nil)
	if gre.Method != tunnelprovision.MethodGREDNAT || gre.RelayHost != "203.0.113.82" {
		t.Errorf("gre verdict = %+v", gre)
	}
	gre6 := Analyze(NodeFacts{IPTunnel: "tx: ip6gre/ipv6 remote 2001:db8:3::1 local 2a12:de40:51::1 encaplimit 4\n"}, nil)
	if gre6.Method != tunnelprovision.MethodIP6GREDNAT {
		t.Errorf("ip6gre verdict = %+v", gre6)
	}
	wg := Analyze(NodeFacts{WGDump: "twg\tPRIV\tPUB\t51820\toff\ntwg\tPEER\t(none)\t203.0.113.82:27001\t10.100.0.14/32\t1\t2\t3\t15\n"}, nil)
	if wg.Method != tunnelprovision.MethodWGDNAT || wg.Interface != "twg" || wg.RelayHost != "203.0.113.82" {
		t.Errorf("wireguard verdict = %+v", wg)
	}
}

func TestAnalyzeReadsForwardedPortsOffTheRelay(t *testing.T) {
	relay := &RelayFacts{
		Route4: "default via 203.0.113.1 dev eth0 proto static\n",
		DNAT: "-N RDNAT_t1\n-A PREROUTING -i eth0 -j RDNAT_t1\n" +
			"-A RDNAT_t1 -p tcp -m tcp --dport 20001 -j DNAT --to-destination 10.100.0.13:20001\n" +
			"-A RDNAT_t1 -p tcp -m tcp --dport 20005 -j DNAT --to-destination 10.100.0.13:20005\n" +
			"-A PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 93.184.216.34:80\n",
	}
	res := Analyze(NodeFacts{IPTunnel: "t1: gre/ip remote 203.0.113.82 local 198.51.100.46 ttl 255\n"}, relay)
	if !reflect.DeepEqual(res.Ports, []int32{20001, 20005}) {
		t.Errorf("ports = %v, want only the forwards that point into the tunnel", res.Ports)
	}
	if res.Details.RelayNIC != "eth0" {
		t.Errorf("relay nic = %q", res.Details.RelayNIC)
	}
}

func TestAnalyzeSaysNothingWhenThereIsNoTunnel(t *testing.T) {
	res := Analyze(NodeFacts{IPTunnel: "gre0: gre/ip remote any local any ttl inherit nopmtudisc\n"}, nil)
	if res.Found {
		t.Errorf("found a tunnel where there is none: %+v", res)
	}
}

func TestSmallParsers(t *testing.T) {
	if got := DefaultRouteDev("default via 198.51.100.1 dev eth0 proto static metric 100\n"); got != "eth0" {
		t.Errorf("DefaultRouteDev = %q", got)
	}
	ss := "LISTEN 0 4096 *:20001 *:* users:((\"frps\",pid=3701,fd=17))\nLISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:((\"sshd\",pid=9,fd=3))\nLISTEN 0 4096 [::]:7000 [::]:* users:((\"frps\",pid=3701,fd=3))\n"
	if got := ParseSSListeners(ss, "frps"); !reflect.DeepEqual(got, []int32{7000, 20001}) {
		t.Errorf("ParseSSListeners = %v", got)
	}
	if h, p, ok := splitHostPort("[2001:db8:3::fe4]:7000"); !ok || h != "2001:db8:3::fe4" || p != 7000 {
		t.Errorf("splitHostPort v6 = %q %d %v", h, p, ok)
	}
}

func TestSectionAndFileSplitting(t *testing.T) {
	out := "###ROUTE4\ndefault via 1.2.3.1 dev eth0\n###FRPC\n@@FILE /a.toml\nserverAddr = \"x\"\n@@FILE /b.toml\nserverPort = 1\n###END\n"
	s := sections(out)
	if !strings.Contains(s["ROUTE4"], "eth0") {
		t.Errorf("sections = %v", s)
	}
	f := files(s["FRPC"])
	if len(f) != 2 || f[0].Path != "/a.toml" || !strings.Contains(f[1].Content, "serverPort") {
		t.Errorf("files = %+v", f)
	}
}

func TestPathAddrsPrefersWhatTheNodeKnowsThenTheTunnelThenTheHost(t *testing.T) {
	v6, v4 := PathAddrs(Details{RelayAddrs: []string{"2001:db8:3::fe4", "203.0.113.82"}}, "", "203.0.113.44")
	if v6 != "2001:db8:3::fe4" || v4 != "203.0.113.82" {
		t.Errorf("detected: %q %q", v6, v4)
	}
	v6, v4 = PathAddrs(Details{}, "2001:db8::2", "203.0.113.9")
	if v6 != "2001:db8::2" || v4 != "203.0.113.9" {
		t.Errorf("built: %q %q", v6, v4)
	}
	v6, v4 = PathAddrs(Details{}, "", "relay.example.com")
	if v6 != "" || v4 != "" {
		t.Errorf("a hostname is never pinged: %q %q", v6, v4)
	}
}
