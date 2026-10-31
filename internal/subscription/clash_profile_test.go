package subscription

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/certs"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/proxysettings"
)

// clashDoc renders proxies and returns the proxies' names and each group's list.
func clashDoc(t *testing.T, proxies []map[string]any, templatePath string) (names []string, groups map[string][]string) {
	t.Helper()
	raw, err := ClashConfig(proxies, templatePath)
	if err != nil {
		t.Fatalf("ClashConfig: %v", err)
	}
	var doc struct {
		Proxies []struct {
			Name string `yaml:"name"`
		} `yaml:"proxies"`
		Groups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not YAML: %v\n%s", err, raw)
	}
	for _, p := range doc.Proxies {
		names = append(names, p.Name)
	}
	groups = map[string][]string{}
	for _, g := range doc.Groups {
		groups[g.Name] = g.Proxies
	}
	return names, groups
}

func writeTemplate(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clash.yml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Clash Meta refuses the whole profile over a repeated name, which is what a new
// panel's two VLESS inbounds on the default remark produced.
func TestClashConfigNeverRepeatsAName(t *testing.T) {
	names, groups := clashDoc(t, []map[string]any{
		{"name": "same", "type": "ss"},
		{"name": "same", "type": "ss"},
		{"name": "same (2)", "type": "ss"},
		{"name": "Net Shield", "type": "ss"},
		{"name": "DIRECT", "type": "ss"},
	}, "")
	want := []string{"same", "same (2)", "same (2) (2)", "Net Shield (2)", "DIRECT (2)"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("names = %q, want %q", names, want)
	}
	if got := groups["Net Shield"]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the group lists %q, want the same names as the proxies", got)
	}
}

// A group with nothing in it costs the whole profile too, and going direct would
// look like a working VPN.
func TestClashConfigWithNoServerStaysAValidProfileThatRejects(t *testing.T) {
	names, groups := clashDoc(t, nil, "")
	if len(names) != 0 {
		t.Errorf("names = %q", names)
	}
	if got := groups["Net Shield"]; len(got) != 1 || got[0] != "REJECT" {
		t.Errorf("group = %q, want [REJECT]", got)
	}
}

func TestClashConfigKeepsWhatATemplateGroupListsThatStillExists(t *testing.T) {
	path := writeTemplate(t, `proxy-groups:
- name: Select
  type: select
  proxies: [Auto, DIRECT, "Old Germany"]
- name: Auto
  type: url-test
  proxies: []
- name: Block
  type: select
  proxies: [REJECT]
rules:
- MATCH,Select
`)
	_, groups := clashDoc(t, []map[string]any{{"name": "a", "type": "ss"}}, path)
	for group, want := range map[string]string{"Select": "Auto|DIRECT|a", "Auto": "a", "Block": "REJECT|a"} {
		if got := strings.Join(groups[group], "|"); got != want {
			t.Errorf("%s = %q, want %q", group, got, want)
		}
	}
}

func TestClashProxyLeavesOutHysteriaWithNoSpeeds(t *testing.T) {
	settings := proxysettings.Settings{Type: proxysettings.Hysteria, Hysteria: &proxysettings.HysteriaSettings{AuthString: "x"}}
	node, err := ClashProxy("h", "1.2.3.4", EffectiveInbound{Port: 443, Security: "tls"}, settings, true)
	if err != nil || node != nil {
		t.Errorf("no speeds: node = %v, err = %v, want nothing", node, err)
	}
	node, err = ClashProxy("h", "1.2.3.4", EffectiveInbound{Port: 443, Security: "tls", UpMbps: 10, DownMbps: 20}, settings, true)
	if err != nil || node == nil {
		t.Errorf("with speeds: node = %v, err = %v", node, err)
	}
}

func TestClashProxyGivesRealityAFingerprint(t *testing.T) {
	settings := proxysettings.Settings{Type: proxysettings.VLESS, VLESS: &proxysettings.VLESSSettings{ID: "u"}}
	node, err := ClashProxy("r", "1.2.3.4", EffectiveInbound{Port: 443, Network: "tcp", Security: "reality", RealityPublicKey: "k"}, settings, true)
	if err != nil {
		t.Fatal(err)
	}
	if node["client-fingerprint"] != "chrome" {
		t.Errorf("client-fingerprint = %v, want chrome", node["client-fingerprint"])
	}
	node, err = ClashProxy("r", "1.2.3.4", EffectiveInbound{Port: 443, Network: "tcp", Security: "tls"}, settings, true)
	if err != nil {
		t.Fatal(err)
	}
	if node["client-fingerprint"] != nil {
		t.Errorf("plain TLS got a fingerprint nobody asked for: %v", node["client-fingerprint"])
	}
}

func TestOnlyABareAddressOnThePanelsOwnCertificateSkipsVerification(t *testing.T) {
	pair, err := certs.GenerateSelfSignedLeaf("vless-main")
	if err != nil {
		t.Fatal(err)
	}
	selfSigned := pgtype.Text{String: pair.CertPEM, Valid: true}
	yes, no := pgtype.Bool{Bool: true, Valid: true}, pgtype.Bool{Bool: false, Valid: true}

	cases := []struct {
		name     string
		cert     pgtype.Text
		host     generated.Host
		security string
		want     bool
	}{
		{"default host", selfSigned, generated.Host{Address: "{SERVER_IP}", Security: "inbound_default"}, "tls", true},
		{"an address", selfSigned, generated.Host{Address: "203.0.113.5", Security: "inbound_default"}, "tls", true},
		{"a domain", selfSigned, generated.Host{Address: "cdn.example.com", Security: "inbound_default"}, "tls", false},
		{"names an SNI", selfSigned, generated.Host{Address: "{SERVER_IP}", Security: "inbound_default", Sni: pgtype.Text{String: "a.example.com", Valid: true}}, "tls", false},
		{"said no", selfSigned, generated.Host{Address: "{SERVER_IP}", Security: "inbound_default", Allowinsecure: no}, "tls", false},
		{"said yes on a domain", selfSigned, generated.Host{Address: "cdn.example.com", Security: "inbound_default", Allowinsecure: yes}, "tls", true},
		{"no certificate", pgtype.Text{}, generated.Host{Address: "{SERVER_IP}", Security: "inbound_default"}, "tls", false},
		{"no TLS", selfSigned, generated.Host{Address: "{SERVER_IP}", Security: "inbound_default"}, "none", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eff := BuildEffectiveInbound(generated.Inbound{Security: tc.security, TlsCertificate: tc.cert}, tc.host)
			if eff.AllowInsecure != tc.want {
				t.Errorf("AllowInsecure = %v, want %v", eff.AllowInsecure, tc.want)
			}
		})
	}
}

func TestSingBoxConfigNeverRepeatsATag(t *testing.T) {
	raw, err := SingBoxConfig([]map[string]any{
		{"type": "vless", "tag": "same"},
		{"type": "vless", "tag": "same"},
		{"type": "vless", "tag": "proxy"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"same (2)"`, `"proxy (2)"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %s in %s", want, raw)
		}
	}
}
