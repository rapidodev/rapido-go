//go:build with_utls

package main

import (
	"net/http"
	"testing"

	sbox "github.com/sagernet/sing-box/option"
)

// realityOutbound is a core config outbound to a REALITY server on this host, as
// a pasted link fills it in.
func realityOutbound(port uint16, publicKey, flow string) outboundSpec {
	return outboundSpec{
		Type: "vless", Server: "127.0.0.1", ServerPort: int(port), UUID: testUserA, Flow: flow,
		TLSEnabled: true, TLSServerName: realityTestServerName, RealityPublicKey: publicKey, RealityShortID: "abcd",
	}
}

// A node sends its users' traffic through another provider's REALITY server, with
// and without Vision, imitating chrome as it does when the link names no
// fingerprint; and a wrong public key gets nowhere.
func TestOutboundCarriesTrafficToARealityServer(t *testing.T) {
	for _, flow := range []string{"", "xtls-rprx-vision"} {
		t.Run("flow="+flow, func(t *testing.T) {
			spec, publicKey := realitySpecFor(t, realityTarget(t))
			port, provider := startProvider(t, "", func(in *sbox.VLESSInboundOptions) {
				in.Users[0].Flow = flow
				in.TLS = realityInboundTLS(t, spec)
			})
			if err := echoThroughProvider(t, provider, realityOutbound(port, publicKey, flow)); err != nil {
				t.Fatalf("through REALITY: %v", err)
			}

			_, otherKey := realitySpecFor(t, realityTarget(t))
			echoHost, echoPort := startEcho(t)
			if err := echoVia(startRelay(t, realityOutbound(port, otherKey, flow)), testUserB, echoHost, echoPort); err == nil {
				t.Error("an outbound with another server's public key got through")
			}
		})
	}
}

// REALITY under gRPC, the other shape REALITY links come in.
func TestOutboundCarriesTrafficToARealityServerOverGRPC(t *testing.T) {
	skipRacyTransport(t, "grpc")
	spec, publicKey := realitySpecFor(t, realityTarget(t))
	port, provider := startProvider(t, "", func(in *sbox.VLESSInboundOptions) {
		in.TLS = realityInboundTLS(t, spec)
		in.Transport = &sbox.V2RayTransportOptions{Type: "grpc", GRPCOptions: sbox.V2RayGRPCOptions{ServiceName: "svc"}}
	})
	remote := realityOutbound(port, publicKey, "")
	remote.TLSFingerprint = "firefox"
	remote.TransportType, remote.TransportServiceName = "grpc", "svc"
	if err := echoThroughProvider(t, provider, remote); err != nil {
		t.Fatalf("through REALITY over gRPC: %v", err)
	}
}

// A uTLS fingerprint over ordinary TLS, under Vision as such links usually are.
func TestOutboundCarriesTrafficWithAFingerprint(t *testing.T) {
	const name = "provider.example.com"
	port, provider := startProvider(t, name, func(in *sbox.VLESSInboundOptions) { in.Users[0].Flow = "xtls-rprx-vision" })
	remote := outboundSpec{
		Type: "vless", Server: "127.0.0.1", ServerPort: int(port), UUID: testUserA, Flow: "xtls-rprx-vision",
		TLSEnabled: true, TLSServerName: name, TLSInsecure: true, TLSFingerprint: "firefox",
	}
	if err := echoThroughProvider(t, provider, remote); err != nil {
		t.Fatalf("with a fingerprint: %v", err)
	}
}

// Paste a link, test it before saving: the draft reaches the test URL through the
// provider's REALITY server, in a core of its own.
func TestDraftTestReachesARealityServer(t *testing.T) {
	generate204(t, 0)
	spec, publicKey := realitySpecFor(t, realityTarget(t))
	port, provider := startProvider(t, "", func(in *sbox.VLESSInboundOptions) {
		in.TLS = realityInboundTLS(t, spec)
	})
	draft := realityOutbound(port, publicKey, "")
	// By name, as links usually give it: the draft's own core has to resolve it.
	draft.Tag, draft.Server = "pasted", "localhost"
	srv := newTestServer(t)
	rec, results := testOutbounds(t, srv, outboundTestRequest{Outbounds: []outboundSpec{draft}})
	if rec.Code != http.StatusOK || len(results) != 1 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	if r := results[0]; !r.Draft || r.StatusCode != http.StatusNoContent || r.Error != "" {
		t.Fatalf("draft = %+v, want a 204 through REALITY", r)
	}
	if usage := provider.Traffic.Drain()[providerUser]; usage.Up == 0 {
		t.Error("the draft's request did not go through the provider")
	}
}

// realityInboundTLS is the TLS block buildOptionsPlan gives a REALITY inbound.
func realityInboundTLS(t *testing.T, spec *realitySpec) *sbox.InboundTLSOptions {
	t.Helper()
	opts, _, err := buildOptionsPlan(startRequest{Inbounds: []inboundSpec{{
		Tag: "r", Protocol: "vless", ListenPort: freeTCPPort(t),
		Users: []userSpec{{Name: providerUser, UUID: testUserA}},
		TLS:   &tlsSpec{ServerName: realityTestServerName, Reality: spec},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return opts.Inbounds[0].Options.(*sbox.VLESSInboundOptions).TLS
}
