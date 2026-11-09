package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/legendary1205/rapido-go/internal/xrayimport"
)

const (
	testRealityPublicKey = "tNusmlSozuXV95B7bOpp7J_vPBWfCis3SChNyuEL9BA"
	// testECHConfig is an ECHConfigList for cloudflare-ech.com, in base64.
	testECHConfig = "AE3+DQBJAAAgACAXs75gt43sJfOCELDz/NA22JSJrfmuEAMbYx9uuadRagAMAAEAAQABAAIAAQADABJjbG91ZGZsYXJlLWVjaC5jb20AAA=="
)

// The shared cases in core-config-cases.json decide valid or not; these check
// that the reason names what is wrong.
func TestValidateOutboundStreamSaysWhatIsWrong(t *testing.T) {
	vless := func(edit func(*outboundDTO)) outboundDTO {
		ob := outboundDTO{Tag: "p", Type: "vless", Server: "s", ServerPort: 443, UUID: "u"}
		edit(&ob)
		return ob
	}
	for want, ob := range map[string]outboundDTO{
		"tls_alpn is not used by type shadowsocks": {Tag: "p", Type: "shadowsocks", TLSALPN: []string{"h2"}},
		"tls_fingerprint is not used by type tuic": {Tag: "p", Type: "tuic", TLSFingerprint: "chrome"},
		"invalid tls_fingerprint chrome_psk":       vless(func(o *outboundDTO) { o.TLSEnabled, o.TLSFingerprint = true, "chrome_psk" }),
		"tls_fingerprint needs TLS":                vless(func(o *outboundDTO) { o.TLSFingerprint = "chrome" }),
		"reality_public_key must be 43 characters": vless(func(o *outboundDTO) { o.TLSServerName, o.RealityPublicKey = "x", "abc" }),
		"reality_short_id must be hex": vless(func(o *outboundDTO) {
			o.TLSServerName, o.RealityPublicKey, o.RealityShortID = "x", testRealityPublicKey, "abc"
		}),
		"reality needs tls_server_name": vless(func(o *outboundDTO) { o.RealityPublicKey = testRealityPublicKey }),
		"reality works over plain TCP, grpc or http, not ws": vless(func(o *outboundDTO) {
			o.TLSServerName, o.RealityPublicKey, o.TransportType = "x", testRealityPublicKey, "ws"
		}),
		"tls_ech_config must be an ECHConfigList": vless(func(o *outboundDTO) { o.TLSEnabled, o.TLSECHConfig = true, "https://1.1.1.1/dns-query" }),
		"flow xtls-rprx-vision needs TLS or REALITY and no transport": vless(func(o *outboundDTO) {
			o.Flow, o.TLSEnabled, o.TransportType = "xtls-rprx-vision", true, "grpc"
		}),
		"invalid transport_type xhttp":                                                  vless(func(o *outboundDTO) { o.TransportType = "xhttp" }),
		"a grpc transport takes transport_service_name":                                 vless(func(o *outboundDTO) { o.TransportType, o.TransportPath = "grpc", "/x" }),
		"transport_path must start with /":                                              vless(func(o *outboundDTO) { o.TransportType, o.TransportPath = "ws", "x" }),
		"transport_path, transport_host and transport_service_name need transport_type": vless(func(o *outboundDTO) { o.TransportHost = "h" }),
	} {
		got := validateOutboundStream(ob)
		if !strings.HasPrefix(got, "outbound p: ") || !strings.Contains(got, want) {
			t.Errorf("%+v: %q, want it to say %q", ob, got, want)
		}
	}

	for name, ob := range map[string]outboundDTO{
		"no new field":    {Tag: "p", Type: "direct", BindInterface: "wg0", DirectFallback: true},
		"plain vless tls": vless(func(o *outboundDTO) { o.TLSEnabled, o.TLSServerName = true, "x" }),
		"reality + vision": vless(func(o *outboundDTO) {
			o.Flow, o.TLSServerName, o.RealityPublicKey = "xtls-rprx-vision", "x", testRealityPublicKey
		}),
		"ech": vless(func(o *outboundDTO) { o.TLSEnabled, o.TLSECHConfig = true, testECHConfig }),
		"url-safe ech": vless(func(o *outboundDTO) {
			o.TLSEnabled, o.TLSECHConfig = true, strings.NewReplacer("+", "-", "/", "_", "=", "").Replace(testECHConfig)
		}),
		"h2 hosts":      vless(func(o *outboundDTO) { o.TransportType, o.TransportHost = "http", "a.example,b.example:8443" }),
		"grpc gun path": vless(func(o *outboundDTO) { o.TransportType, o.TransportServiceName = "grpc", "/my/path|alt" }),
	} {
		if got := validateOutboundStream(ob); got != "" {
			t.Errorf("%s refused: %s", name, got)
		}
	}
}

// An agent from before these fields would dial a REALITY or ws server some other
// way: it gets the outbound as a block of the same tag instead.
func TestOutboundsForFailsClosedOnAnOlderAgent(t *testing.T) {
	plain := outboundDTO{Tag: "exit", Type: "direct", BindInterface: "wg0", DirectFallback: true}
	remote := outboundDTO{Tag: "provider", Type: "vless", Server: "s", ServerPort: 443, UUID: "u", TLSServerName: "x", RealityPublicKey: testRealityPublicKey}
	ws := outboundDTO{Tag: "cdn", Type: "vmess", Server: "s", ServerPort: 443, UUID: "u", Security: "auto", TransportType: "ws"}
	core := coreConfigDTO{Outbounds: []outboundDTO{plain, remote, ws}}

	old := nodeConfigSchema{compact: true, delta: true, extendedRules: true}.outboundsFor(core)
	want := []outboundDTO{plain, {Tag: "provider", Type: "block"}, {Tag: "cdn", Type: "block"}}
	if !reflect.DeepEqual(old.Outbounds, want) {
		t.Errorf("older agent gets %+v, want %+v", old.Outbounds, want)
	}
	if core.Outbounds[1].RealityPublicKey == "" {
		t.Fatal("the shared snapshot was modified")
	}
	if got := (nodeConfigSchema{streamOutbounds: true}).outboundsFor(core); !reflect.DeepEqual(got.Outbounds, core.Outbounds) {
		t.Errorf("schema 5 agent gets %+v", got.Outbounds)
	}
}

func TestOutboundUsesStreamFieldsCoversEveryNewField(t *testing.T) {
	for name, ob := range map[string]outboundDTO{
		"tls_alpn":               {TLSALPN: []string{"h2"}},
		"tls_fingerprint":        {TLSFingerprint: "chrome"},
		"tls_ech_config":         {TLSECHConfig: testECHConfig},
		"reality_public_key":     {RealityPublicKey: testRealityPublicKey},
		"reality_short_id":       {RealityShortID: "ab"},
		"transport_type":         {TransportType: "ws"},
		"transport_path":         {TransportPath: "/"},
		"transport_host":         {TransportHost: "h"},
		"transport_service_name": {TransportServiceName: "s"},
	} {
		if !outboundUsesStreamFields(ob) {
			t.Errorf("%s is not seen", name)
		}
		raw, _ := json.Marshal(ob)
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("%s: the JSON key is %s", name, raw)
		}
	}
}

// Every new field goes out to Xray JSON and comes back from it the same.
func TestRemoteOutboundsRoundTripThroughXrayJSON(t *testing.T) {
	outbounds := []outboundDTO{
		{Tag: "reality", Type: "vless", Server: "203.0.113.1", ServerPort: 443, UUID: "u", Flow: "xtls-rprx-vision", TLSEnabled: true,
			TLSServerName: "www.example.com", TLSFingerprint: "firefox", RealityPublicKey: testRealityPublicKey, RealityShortID: "0123abcd"},
		{Tag: "reality-grpc", Type: "trojan", Server: "203.0.113.2", ServerPort: 443, Password: "p", TLSEnabled: true,
			TLSServerName: "www.example.com", TLSFingerprint: "chrome", RealityPublicKey: testRealityPublicKey, TransportType: "grpc", TransportServiceName: "svc"},
		{Tag: "ws", Type: "vmess", Server: "cdn.example.com", ServerPort: 443, UUID: "u", Security: "auto", TLSEnabled: true,
			TLSServerName: "cdn.example.com", TLSInsecure: true, TLSALPN: []string{"http/1.1"}, TLSFingerprint: "chrome",
			TransportType: "ws", TransportPath: "/ray?ed=2048", TransportHost: "cdn.example.com"},
		{Tag: "upgrade", Type: "vless", Server: "203.0.113.3", ServerPort: 80, UUID: "u", TransportType: "httpupgrade", TransportPath: "/up", TransportHost: "h.example"},
		{Tag: "h2", Type: "vless", Server: "203.0.113.4", ServerPort: 443, UUID: "u", TLSEnabled: true, TLSServerName: "a.example",
			TransportType: "http", TransportPath: "/h2", TransportHost: "a.example,b.example"},
		{Tag: "ech", Type: "trojan", Server: "203.0.113.5", ServerPort: 443, Password: "p", TLSEnabled: true, TLSServerName: "inner.example", TLSECHConfig: testECHConfig},
	}
	if msg := validateCoreConfig(coreConfigDTO{Outbounds: outbounds}); msg != "" {
		t.Fatal(msg)
	}
	raw, err := xrayimport.BuildXrayJSON(nil, coreOutboundsToExport(outbounds), nil, nil, "warn", false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := xrayimport.ParseXrayConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range parsed.Warnings {
		// The routing domainStrategy every export carries is the only one expected.
		if strings.Contains(w, "outbound") {
			t.Errorf("warning: %s", w)
		}
	}
	var back []outboundDTO
	for _, ob := range parsed.Outbounds {
		back = append(back, outboundFromImport(ob))
	}
	if !reflect.DeepEqual(back, outbounds) {
		got, _ := json.MarshalIndent(back, "", " ")
		t.Errorf("came back as\n%s\nfrom\n%s", got, raw)
	}
}
