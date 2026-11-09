package xrayimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const testPublicKey = "tNusmlSozuXV95B7bOpp7J_vPBWfCis3SChNyuEL9BA"

func parseOneOutbound(t *testing.T, outbound string) (*Outbound, []string) {
	t.Helper()
	result, err := ParseXrayConfig([]byte(`{"outbounds":[` + outbound + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outbounds) == 0 {
		return nil, result.Warnings
	}
	return &result.Outbounds[0], result.Warnings
}

// What another provider's panel hands out, as Xray writes it.
func TestParseOutboundReadsTheProvidersStreamSettings(t *testing.T) {
	cases := map[string]struct {
		json string
		want Outbound
	}{
		"vless reality vision": {
			`{"tag":"p","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.1","port":443,"users":[{"id":"u","flow":"xtls-rprx-vision","encryption":"none"}]}]},
			  "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"www.example.com","publicKey":"` + testPublicKey + `","shortId":"0123abcd","fingerprint":"chrome"}}}`,
			Outbound{Tag: "p", Type: "vless", Server: "203.0.113.1", ServerPort: 443, UUID: "u", Flow: "xtls-rprx-vision",
				TLSEnabled: true, TLSServerName: "www.example.com", TLSFingerprint: "chrome", RealityPublicKey: testPublicKey, RealityShortID: "0123abcd"},
		},
		"vmess ws tls, host in headers": {
			`{"tag":"p","protocol":"vmess","settings":{"vnext":[{"address":"cdn.example.com","port":443,"users":[{"id":"u","security":"auto"}]}]},
			  "streamSettings":{"network":"ws","security":"tls","tlsSettings":{"serverName":"cdn.example.com","allowInsecure":true,"alpn":["http/1.1"],"fingerprint":"firefox"},
			  "wsSettings":{"path":"/ray?ed=2048","headers":{"Host":"cdn.example.com"}}}}`,
			Outbound{Tag: "p", Type: "vmess", Server: "cdn.example.com", ServerPort: 443, UUID: "u", Security: "auto",
				TLSEnabled: true, TLSServerName: "cdn.example.com", TLSInsecure: true, TLSALPN: []string{"http/1.1"}, TLSFingerprint: "firefox",
				TransportType: "ws", TransportPath: "/ray?ed=2048", TransportHost: "cdn.example.com"},
		},
		"ws host the newer way": {
			`{"tag":"p","protocol":"vless","settings":{"vnext":[{"address":"h","port":80,"users":[{"id":"u"}]}]},"streamSettings":{"network":"ws","wsSettings":{"path":"/w","host":"h.example"}}}`,
			Outbound{Tag: "p", Type: "vless", Server: "h", ServerPort: 80, UUID: "u", TransportType: "ws", TransportPath: "/w", TransportHost: "h.example"},
		},
		"trojan grpc": {
			`{"tag":"p","protocol":"trojan","settings":{"servers":[{"address":"h","port":443,"password":"pw"}]},"streamSettings":{"network":"grpc","security":"tls","tlsSettings":{"serverName":"h"},"grpcSettings":{"serviceName":"svc"}}}`,
			Outbound{Tag: "p", Type: "trojan", Server: "h", ServerPort: 443, Password: "pw", TLSEnabled: true, TLSServerName: "h", TransportType: "grpc", TransportServiceName: "svc"},
		},
		"httpupgrade": {
			`{"tag":"p","protocol":"vless","settings":{"vnext":[{"address":"h","port":80,"users":[{"id":"u"}]}]},"streamSettings":{"network":"httpupgrade","httpupgradeSettings":{"path":"/up","host":"h.example"}}}`,
			Outbound{Tag: "p", Type: "vless", Server: "h", ServerPort: 80, UUID: "u", TransportType: "httpupgrade", TransportPath: "/up", TransportHost: "h.example"},
		},
		"h2, by either name": {
			`{"tag":"p","protocol":"vless","settings":{"vnext":[{"address":"h","port":443,"users":[{"id":"u"}]}]},"streamSettings":{"network":"http","security":"tls","httpSettings":{"path":"/h2","host":["a.example","b.example"]}}}`,
			Outbound{Tag: "p", Type: "vless", Server: "h", ServerPort: 443, UUID: "u", TLSEnabled: true, TransportType: "http", TransportPath: "/h2", TransportHost: "a.example,b.example"},
		},
		"ech": {
			`{"tag":"p","protocol":"trojan","settings":{"servers":[{"address":"h","port":443,"password":"pw"}]},"streamSettings":{"security":"tls","tlsSettings":{"serverName":"i","echConfigList":"AE3+DQBJAAAgACAXs75gt43sJfOCELDz/NA22JSJrfmuEAMbYx9uuadRagAMAAEAAQABAAIAAQADABJjbG91ZGZsYXJlLWVjaC5jb20AAA=="}}}`,
			Outbound{Tag: "p", Type: "trojan", Server: "h", ServerPort: 443, Password: "pw", TLSEnabled: true, TLSServerName: "i",
				TLSECHConfig: "AE3+DQBJAAAgACAXs75gt43sJfOCELDz/NA22JSJrfmuEAMbYx9uuadRagAMAAEAAQABAAIAAQADABJjbG91ZGZsYXJlLWVjaC5jb20AAA=="},
		},
	}
	for name, tc := range cases {
		got, warnings := parseOneOutbound(t, tc.json)
		if got == nil {
			t.Errorf("%s: skipped: %v", name, warnings)
			continue
		}
		if !reflect.DeepEqual(*got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, *got, tc.want)
		}
		if len(warnings) > 0 {
			t.Errorf("%s: warnings %v", name, warnings)
		}
	}
}

// What would only narrow how it connects is dropped with a word; what would make
// it connect some other way skips the outbound rather than saving a broken one.
func TestParseOutboundSaysWhatItLeavesOut(t *testing.T) {
	vless := func(stream string) string {
		return `{"tag":"p","protocol":"vless","settings":{"vnext":[{"address":"h","port":443,"users":[{"id":"u"}]}]},"streamSettings":` + stream + `}`
	}
	for name, tc := range map[string]struct {
		json, warning string
		skipped       bool
	}{
		"xhttp":             {vless(`{"network":"xhttp"}`), `transport "xhttp" isn't supported`, true},
		"kcp":               {vless(`{"network":"kcp"}`), `transport "kcp" isn't supported`, true},
		"quic":              {vless(`{"network":"quic"}`), `transport "quic" isn't supported`, true},
		"another security":  {vless(`{"security":"xtls"}`), `security "xtls" isn't supported`, true},
		"reality, no key":   {vless(`{"security":"reality","realitySettings":{"serverName":"x"}}`), "without a publicKey", true},
		"spiderX":           {vless(`{"security":"reality","realitySettings":{"serverName":"x","publicKey":"` + testPublicKey + `","spiderX":"/"}}`), "spiderX", false},
		"multiMode":         {vless(`{"network":"grpc","grpcSettings":{"serviceName":"s","multiMode":true}}`), "multiMode", false},
		"odd fingerprint":   {vless(`{"security":"tls","tlsSettings":{"fingerprint":"hellochrome_100"}}`), `fingerprint "hellochrome_100"`, false},
		"ech by dns lookup": {vless(`{"security":"tls","tlsSettings":{"echConfigList":"cloudflare-ech.com+https://1.1.1.1/dns-query"}}`), "echConfigList", false},
	} {
		got, warnings := parseOneOutbound(t, tc.json)
		if (got == nil) != tc.skipped {
			t.Errorf("%s: skipped = %v, want %v", name, got == nil, tc.skipped)
		}
		if !strings.Contains(strings.Join(warnings, "; "), tc.warning) {
			t.Errorf("%s: warnings %v, want one about %q", name, warnings, tc.warning)
		}
	}
}

// An outbound that uses none of the new fields exports exactly as it did:
// tlsSettings with serverName and allowInsecure only, and no network at all.
func TestBuildXrayOutboundLeavesAPlainOutboundAsItWas(t *testing.T) {
	got, _ := json.Marshal(buildXrayOutbound(Outbound{Tag: "p", Type: "vless", Server: "h", ServerPort: 443, UUID: "u", TLSEnabled: true, TLSServerName: "h", BindInterface: "wg0"}))
	want := `{"protocol":"vless","settings":{"vnext":[{"address":"h","port":443,"users":[{"id":"u"}]}]},"streamSettings":{"security":"tls","sockopt":{"interface":"wg0"},"tlsSettings":{"allowInsecure":false,"serverName":"h"}},"tag":"p"}`
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// REALITY needs a fingerprint in Xray; the one the node uses by default goes out.
func TestBuildXrayOutboundWritesRealityWithItsDefaultFingerprint(t *testing.T) {
	out := buildXrayOutbound(Outbound{Tag: "p", Type: "vless", Server: "h", ServerPort: 443, UUID: "u", TLSServerName: "www.example.com", RealityPublicKey: testPublicKey})
	stream := out["streamSettings"].(map[string]any)
	reality := stream["realitySettings"].(map[string]any)
	if stream["security"] != "reality" || reality["fingerprint"] != "chrome" || reality["publicKey"] != testPublicKey || reality["serverName"] != "www.example.com" {
		t.Errorf("stream = %v", stream)
	}
	if _, ok := stream["tlsSettings"]; ok {
		t.Error("REALITY also wrote tlsSettings")
	}
}
