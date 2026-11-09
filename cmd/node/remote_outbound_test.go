package main

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"reflect"
	"testing"

	boxtls "github.com/sagernet/sing-box/common/tls"
	sbox "github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/legendary1205/rapido-go/internal/nodecore"
	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

// Outbounds to another provider's server, end to end: a node stands in for that
// provider (a VLESS inbound behind the transport under test), and a second node
// sends a client's traffic there through a core config outbound built the way
// the panel describes it, echo and all.

const providerUser = "alice"

// startProvider is the other provider's server: VLESS for testUserA, with TLS
// for serverName when it is set, changed further by edit.
func startProvider(t *testing.T, serverName string, edit func(*sbox.VLESSInboundOptions)) (uint16, *nodecore.Node) {
	t.Helper()
	port := freeTCPPort(t)
	in := inboundSpec{Tag: "provider-in", Protocol: "vless", ListenPort: port, Users: []userSpec{{Name: providerUser, UUID: testUserA}}}
	if serverName != "" {
		cert, key := selfSignedPEM(t, serverName)
		in.TLS = &tlsSpec{ServerName: serverName, Certificate: cert, Key: key}
	}
	opts, _, err := buildOptionsPlan(startRequest{Inbounds: []inboundSpec{in}})
	if err != nil {
		t.Fatal(err)
	}
	// Set on the options rather than the spec: this side is a stand-in for any
	// server out there, including what this panel's own inbounds don't offer.
	if edit != nil {
		edit(opts.Inbounds[0].Options.(*sbox.VLESSInboundOptions))
	}
	node, err := nodecore.New(context.Background(), opts, traffic.NewManager())
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	return port, node
}

// startRelay is this panel's node: a plain VLESS inbound for testUserB whose every
// connection the core config sends out through remote.
func startRelay(t *testing.T, remote outboundSpec) uint16 {
	t.Helper()
	port := freeTCPPort(t)
	remote.Tag = "provider"
	startPlanned(t, startRequest{
		Inbounds: []inboundSpec{{Tag: "in", Protocol: "vless", ListenPort: port, Users: []userSpec{{Name: "bob", UUID: testUserB}}}},
		Core: &coreSpec{
			Outbounds:    []outboundSpec{remote},
			RoutingRules: []routingRuleSpec{{Inbound: []string{"in"}, OutboundTag: "provider"}},
		},
	})
	return port
}

// echoThroughProvider sends an echo through the relay and reports whether it came
// back by way of the provider, which counts what its user moved.
func echoThroughProvider(t *testing.T, provider *nodecore.Node, remote outboundSpec) error {
	t.Helper()
	echoHost, echoPort := startEcho(t)
	relay := startRelay(t, remote)
	provider.Traffic.Drain()
	if err := echoVia(relay, testUserB, echoHost, echoPort); err != nil {
		return err
	}
	if usage := provider.Traffic.Drain()[providerUser]; usage.Up == 0 || usage.Down == 0 {
		t.Fatalf("the echo came back without going through the provider: %+v", usage)
	}
	return nil
}

func TestOutboundCarriesTrafficOverEachTransport(t *testing.T) {
	const name = "provider.example.com"
	cases := []struct {
		name       string
		serverName string
		server     *sbox.V2RayTransportOptions
		outbound   outboundSpec
		// wrong is the same outbound with what the transport checks spoiled.
		wrong outboundSpec
	}{
		{
			name: "ws over tls", serverName: name,
			server: &sbox.V2RayTransportOptions{Type: "ws", WebsocketOptions: sbox.V2RayWebsocketOptions{Path: "/ray"}},
			outbound: outboundSpec{TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TLSALPN: []string{"http/1.1"},
				TransportType: "ws", TransportPath: "/ray", TransportHost: name},
			wrong: outboundSpec{TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TransportType: "ws", TransportPath: "/elsewhere"},
		},
		{
			name:     "grpc",
			server:   &sbox.V2RayTransportOptions{Type: "grpc", GRPCOptions: sbox.V2RayGRPCOptions{ServiceName: "svc"}},
			outbound: outboundSpec{TransportType: "grpc", TransportServiceName: "svc"},
			wrong:    outboundSpec{TransportType: "grpc", TransportServiceName: "other"},
		},
		{
			name: "grpc over tls", serverName: name,
			server:   &sbox.V2RayTransportOptions{Type: "grpc", GRPCOptions: sbox.V2RayGRPCOptions{ServiceName: "svc"}},
			outbound: outboundSpec{TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TransportType: "grpc", TransportServiceName: "svc"},
			wrong:    outboundSpec{TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TransportType: "grpc", TransportServiceName: "other"},
		},
		{
			name:     "httpupgrade",
			server:   &sbox.V2RayTransportOptions{Type: "httpupgrade", HTTPUpgradeOptions: sbox.V2RayHTTPUpgradeOptions{Path: "/up", Host: "cdn.example.com"}},
			outbound: outboundSpec{TransportType: "httpupgrade", TransportPath: "/up", TransportHost: "cdn.example.com"},
			wrong:    outboundSpec{TransportType: "httpupgrade", TransportPath: "/up", TransportHost: "other.example.com"},
		},
		{
			name: "http/2 over tls", serverName: name,
			server:   &sbox.V2RayTransportOptions{Type: "http", HTTPOptions: sbox.V2RayHTTPOptions{Path: "/h2", Host: badoption.Listable[string]{name}}},
			outbound: outboundSpec{TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TransportType: "http", TransportPath: "/h2", TransportHost: name},
			wrong:    outboundSpec{TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TransportType: "http", TransportPath: "/other", TransportHost: name},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipRacyTransport(t, tc.server.Type)
			port, provider := startProvider(t, tc.serverName, func(in *sbox.VLESSInboundOptions) { in.Transport = tc.server })
			dial := func(ob outboundSpec) outboundSpec {
				ob.Type, ob.Server, ob.ServerPort, ob.UUID = "vless", "127.0.0.1", int(port), testUserA
				return ob
			}
			if err := echoThroughProvider(t, provider, dial(tc.outbound)); err != nil {
				t.Fatalf("through the provider: %v", err)
			}
			echoHost, echoPort := startEcho(t)
			if err := echoVia(startRelay(t, dial(tc.wrong)), testUserB, echoHost, echoPort); err == nil {
				t.Error("an outbound the provider's transport should refuse got through")
			}
		})
	}
}

// skipRacyTransport skips, under the race detector only, a transport whose
// sing-box v1.14.0 client sets its connection up on one goroutine while another
// already reads it (v2raygrpclite.GunConn, v2rayhttp.HTTP2Conn). That race is
// sing-box's, not this code's, and the detector would fail the whole run on it.
func skipRacyTransport(t *testing.T, transport string) {
	t.Helper()
	if raceDetectorEnabled && (transport == "grpc" || transport == "http") {
		t.Skipf("sing-box's own %s client transport races under the race detector", transport)
	}
}

// The mapping onto sing-box's options, one field at a time; and an outbound using
// none of the new fields builds exactly what it did before them.
func TestBuildOutboundTLSAndTransport(t *testing.T) {
	plain := outboundSpec{Tag: "p", Type: "vless", Server: "h", ServerPort: 443, UUID: "u", TLSEnabled: true, TLSServerName: "h", TLSInsecure: true}
	if got := buildOutboundTLS(plain, false); !reflect.DeepEqual(got, &sbox.OutboundTLSOptions{Enabled: true, ServerName: "h", Insecure: true}) {
		t.Errorf("plain tls = %+v", got)
	}
	if got, err := buildOutboundTransport(plain); got != nil || err != nil {
		t.Errorf("plain transport = %+v, %v", got, err)
	}
	if got := buildOutboundTLS(outboundSpec{Type: "vless"}, false); got != nil {
		t.Errorf("no tls = %+v", got)
	}

	reality := buildOutboundTLS(outboundSpec{Type: "vless", TLSServerName: "www.example.com", RealityPublicKey: "k", RealityShortID: "ab"}, false)
	if reality == nil || !reality.Enabled || reality.Reality == nil || !reality.Reality.Enabled || reality.Reality.PublicKey != "k" || reality.Reality.ShortID != "ab" {
		t.Fatalf("reality = %+v, want REALITY on without tls_enabled", reality)
	}
	if reality.UTLS == nil || !reality.UTLS.Enabled || reality.UTLS.Fingerprint != "chrome" {
		t.Errorf("reality utls = %+v, want chrome by default", reality.UTLS)
	}
	fp := buildOutboundTLS(outboundSpec{Type: "trojan", TLSEnabled: true, TLSFingerprint: "firefox", TLSALPN: []string{"h2", "http/1.1"}}, false)
	if fp.UTLS == nil || fp.UTLS.Fingerprint != "firefox" || !reflect.DeepEqual([]string(fp.ALPN), []string{"h2", "http/1.1"}) || fp.Reality != nil {
		t.Errorf("fingerprint = %+v", fp)
	}

	raw := []byte{0, 4, 0xfe, 0x0d, 0, 0}
	ech := buildOutboundTLS(outboundSpec{Type: "trojan", TLSEnabled: true, TLSECHConfig: base64.StdEncoding.EncodeToString(raw)}, false)
	if ech.ECH == nil || !ech.ECH.Enabled || len(ech.ECH.Config) != 1 {
		t.Fatalf("ech = %+v", ech.ECH)
	}
	if block, rest := pem.Decode([]byte(ech.ECH.Config[0])); block == nil || block.Type != "ECH CONFIGS" || !reflect.DeepEqual(block.Bytes, raw) || len(rest) != 0 {
		t.Errorf("ech config = %q, want the list in an ECH CONFIGS block", ech.ECH.Config[0])
	}

	for name, tc := range map[string]struct {
		spec outboundSpec
		want *sbox.V2RayTransportOptions
	}{
		"ws": {outboundSpec{TransportType: "ws", TransportPath: "/w", TransportHost: "h.example"},
			&sbox.V2RayTransportOptions{Type: "ws", WebsocketOptions: sbox.V2RayWebsocketOptions{Path: "/w", Headers: badoption.HTTPHeader{"Host": {"h.example"}}}}},
		"grpc": {outboundSpec{TransportType: "grpc", TransportServiceName: "svc"},
			&sbox.V2RayTransportOptions{Type: "grpc", GRPCOptions: sbox.V2RayGRPCOptions{ServiceName: "svc"}}},
		"httpupgrade": {outboundSpec{TransportType: "httpupgrade", TransportPath: "/u", TransportHost: "h.example"},
			&sbox.V2RayTransportOptions{Type: "httpupgrade", HTTPUpgradeOptions: sbox.V2RayHTTPUpgradeOptions{Path: "/u", Host: "h.example"}}},
		"http": {outboundSpec{TransportType: "http", TransportPath: "/h", TransportHost: "a.example,b.example"},
			&sbox.V2RayTransportOptions{Type: "http", HTTPOptions: sbox.V2RayHTTPOptions{Path: "/h", Host: badoption.Listable[string]{"a.example", "b.example"}}}},
	} {
		got, err := buildOutboundTransport(tc.spec)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %+v, %v, want %+v", name, got, err, tc.want)
		}
	}
	if _, err := buildOutboundTransport(outboundSpec{Tag: "p", TransportType: "xhttp"}); err == nil {
		t.Error("an unknown transport was built")
	}

	// Every one of the three TCP types gets the transport and the TLS.
	for _, kind := range []string{"vless", "vmess", "trojan"} {
		opts, err := leafOutboundOptions(outboundSpec{Tag: "p", Type: kind, Server: "h", ServerPort: 1, TLSEnabled: true, TransportType: "grpc"}, "")
		if err != nil {
			t.Fatal(err)
		}
		var transport *sbox.V2RayTransportOptions
		var tls *sbox.OutboundTLSOptions
		switch o := opts.(type) {
		case *sbox.VLESSOutboundOptions:
			transport, tls = o.Transport, o.TLS
		case *sbox.VMessOutboundOptions:
			transport, tls = o.Transport, o.TLS
		case *sbox.TrojanOutboundOptions:
			transport, tls = o.Transport, o.TLS
		}
		if transport == nil || transport.Type != "grpc" || tls == nil || !tls.Enabled {
			t.Errorf("%s = %+v", kind, opts)
		}
	}
}

// ECH, with the ECHConfigList the way a share link or Xray carries it: the
// provider holds the key, and the outbound gets through. A provider without ECH
// answers the outer hello instead, which the outbound must refuse - so passing
// here means ECH was really in use.
func TestOutboundCarriesTrafficWithECH(t *testing.T) {
	const name = "inner.example.com"
	configPEM, keyPEM, err := boxtls.ECHKeygenDefault("public.example.com")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(configPEM))
	if block == nil {
		t.Fatal("no ECH config block")
	}
	remote := func(port uint16) outboundSpec {
		return outboundSpec{
			Type: "vless", Server: "127.0.0.1", ServerPort: int(port), UUID: testUserA,
			TLSEnabled: true, TLSServerName: name, TLSInsecure: true,
			TLSECHConfig: base64.StdEncoding.EncodeToString(block.Bytes),
		}
	}

	port, provider := startProvider(t, name, func(in *sbox.VLESSInboundOptions) {
		in.TLS.ECH = &sbox.InboundECHOptions{Enabled: true, Key: badoption.Listable[string]{keyPEM}}
	})
	if err := echoThroughProvider(t, provider, remote(port)); err != nil {
		t.Fatalf("through the provider with ECH: %v", err)
	}

	plainPort, _ := startProvider(t, name, nil)
	echoHost, echoPort := startEcho(t)
	if err := echoVia(startRelay(t, remote(plainPort)), testUserB, echoHost, echoPort); err == nil {
		t.Error("an ECH outbound got through a server that has no ECH")
	}
}
