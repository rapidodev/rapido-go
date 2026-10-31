package subscription

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/legendary1205/rapido-go/internal/proxysettings"
)

func TestLoadYAMLTemplateMissingPathReturnsNotOK(t *testing.T) {
	if _, ok := LoadYAMLTemplate(""); ok {
		t.Error("empty path should return ok=false")
	}
	if _, ok := LoadYAMLTemplate(filepath.Join(t.TempDir(), "does-not-exist.yml")); ok {
		t.Error("nonexistent file should return ok=false")
	}
}

func TestLoadJSONTemplateMissingPathReturnsNotOK(t *testing.T) {
	if _, ok := LoadJSONTemplate(""); ok {
		t.Error("empty path should return ok=false")
	}
	if _, ok := LoadJSONTemplate(filepath.Join(t.TempDir(), "does-not-exist.json")); ok {
		t.Error("nonexistent file should return ok=false")
	}
}

// The built-in template stands in for anything an operator's file can go wrong with,
// including the Jinja files carried over from the Python panel.
func TestClashConfigServesTheBuiltInTemplateWhenTheFileCannotBeUsed(t *testing.T) {
	dir := t.TempDir()
	jinja := filepath.Join(dir, "jinja.yml")
	if err := os.WriteFile(jinja, []byte("proxies:\n{{ conf | yaml }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"none": "", "missing": filepath.Join(dir, "missing.yml"), "jinja": jinja} {
		raw, err := ClashConfig([]map[string]any{{"name": "Germany", "type": "vless"}}, path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: output is not valid YAML: %v\n%s", name, err, raw)
		}
		if _, ok := doc["rule-providers"]; !ok {
			t.Errorf("%s: not the built-in template:\n%s", name, raw)
		}
		group := doc["proxy-groups"].([]any)[0].(map[string]any)
		if names := group["proxies"].([]any); len(names) != 1 || names[0] != "Germany" {
			t.Errorf("%s: group proxies = %+v", name, group["proxies"])
		}
	}
}

// Rendering must not change a thing in the template but the server lists. A quoting
// slip on values such as "*" or a nameserver's #group would break a real client.
func TestClashConfigKeepsTheBuiltInTemplateIntact(t *testing.T) {
	var want map[string]any
	if err := yaml.Unmarshal(defaultClashTemplate, &want); err != nil {
		t.Fatal(err)
	}
	raw, err := ClashConfig([]map[string]any{{"name": "Germany", "type": "vless"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, raw)
	}
	for key, value := range want {
		if key == "proxy-groups" {
			continue
		}
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s changed: got %+v, want %+v", key, got[key], value)
		}
	}
}

// mihomo refuses the whole profile over a rule naming a group or provider it doesn't
// have, so a slip here would take every Clash client down at once.
func TestBuiltInClashTemplateRulesPointAtThingsThatExist(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal(defaultClashTemplate, &doc); err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{"DIRECT": true, "REJECT": true}
	for _, g := range doc["proxy-groups"].([]any) {
		targets[g.(map[string]any)["name"].(string)] = true
	}
	providers := doc["rule-providers"].(map[string]any)
	rules := doc["rules"].([]any)
	if last := rules[len(rules)-1].(string); !strings.HasPrefix(last, "MATCH,") {
		t.Errorf("last rule = %q, want a catch-all", last)
	}
	for _, r := range rules {
		fields := strings.Split(r.(string), ",")
		target := fields[len(fields)-1]
		if fields[0] == "MATCH" {
			target = fields[1]
		} else if len(fields) > 3 {
			target = fields[2] // the rest are options such as no-resolve
		}
		if !targets[target] {
			t.Errorf("rule %q goes to %q, which doesn't exist", r, target)
		}
		if fields[0] == "RULE-SET" {
			if _, ok := providers[fields[1]]; !ok {
				t.Errorf("rule %q names a provider that doesn't exist", r)
			}
		}
	}
}

func TestCheckYAMLTemplateSaysWhyAFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yml")
	if err := os.WriteFile(good, []byte("mixed-port: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jinja := filepath.Join(dir, "jinja.yml")
	if err := os.WriteFile(jinja, []byte("proxies:\n{{ conf | yaml }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckYAMLTemplate(""); err != nil {
		t.Errorf("no template configured: %v", err)
	}
	if err := CheckYAMLTemplate(good); err != nil {
		t.Errorf("a good file: %v", err)
	}
	if err := CheckYAMLTemplate(filepath.Join(dir, "missing.yml")); err == nil {
		t.Error("a missing file went unreported")
	}
	if err := CheckYAMLTemplate(jinja); err == nil {
		t.Error("a Jinja file went unreported")
	}
}

// TestClashConfigUsesRealTemplateFile is the merge-path regression test:
// with a configured template file, ClashConfig must preserve every static
// key from the file (mixed-port, dns, rule-providers, rules - whatever an
// operator's file carries) and ONLY replace "proxies" and each
// proxy-group's own "proxies" placeholder with the real, current data.
func TestClashConfigUsesRealTemplateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clash.yml")
	content := `
mixed-port: 10801
dns:
  enable: true
proxy-groups:
- name: Net Shield
  type: select
  proxies: []
rules:
- RULE-SET,ads,REJECT
- MATCH,Net Shield
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	raw, err := ClashConfig([]map[string]any{{"name": "Germany", "type": "vless"}}, path)
	if err != nil {
		t.Fatalf("ClashConfig: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, raw)
	}
	if doc["mixed-port"] != uint64(10801) {
		t.Errorf("mixed-port from the template was not preserved: %+v", doc["mixed-port"])
	}
	dns, ok := doc["dns"].(map[string]any)
	if !ok || dns["enable"] != true {
		t.Errorf("dns block from the template was not preserved: %+v", doc["dns"])
	}
	rules, ok := doc["rules"].([]any)
	if !ok || len(rules) != 2 || rules[0] != "RULE-SET,ads,REJECT" {
		t.Errorf("rules from the template were not preserved verbatim: %+v", doc["rules"])
	}
	groups := doc["proxy-groups"].([]any)
	group := groups[0].(map[string]any)
	if group["name"] != "Net Shield" {
		t.Errorf("proxy-group name from the template was not preserved: %+v", group["name"])
	}
	names := group["proxies"].([]any)
	if len(names) != 1 || names[0] != "Germany" {
		t.Errorf("proxy-group's proxies placeholder was not filled with real data: %+v", names)
	}
}

// TestV2rayJSONConfigUsesRealTemplateFile is the same merge-path check for
// the v2ray-json format: the generated outbound must be prepended to the
// template's own outbounds (matching Python's `outbounds +
// json_template["outbounds"]`), and every other key (log/dns/routing/
// inbounds/policy) must survive untouched.
func TestV2rayJSONConfigUsesRealTemplateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v2ray.json")
	content := `{
		"log": {"loglevel": "warning"},
		"dns": {"servers": ["1.1.1.1"]},
		"outbounds": [
			{"tag": "direct", "protocol": "freedom"},
			{"tag": "block", "protocol": "blackhole"}
		]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	template, ok := LoadJSONTemplate(path)
	if !ok {
		t.Fatal("LoadJSONTemplate returned ok=false for a real file")
	}

	in := EffectiveInbound{Network: "tcp", Port: 443, Security: "tls", SNI: "example.com"}
	settings := proxysettings.Settings{Type: proxysettings.VLESS, VLESS: &proxysettings.VLESSSettings{ID: "uuid-1"}}
	cfg, err := V2rayJSONConfig("My Node", "1.2.3.4", in, settings, template)
	if err != nil {
		t.Fatalf("V2rayJSONConfig: %v", err)
	}

	if _, ok := cfg["log"]; !ok {
		t.Errorf("log block from the template was not preserved: %+v", cfg)
	}
	if _, ok := cfg["dns"]; !ok {
		t.Errorf("dns block from the template was not preserved: %+v", cfg)
	}
	outbounds, ok := cfg["outbounds"].([]any)
	if !ok || len(outbounds) != 3 {
		t.Fatalf("expected 3 outbounds (generated proxy + template's direct + block), got %+v", cfg["outbounds"])
	}
	proxy := outbounds[0].(map[string]any)
	if proxy["protocol"] != "vless" {
		t.Errorf("first outbound must be the generated proxy, got %+v", proxy)
	}
	direct := outbounds[1].(map[string]any)
	if direct["tag"] != "direct" {
		t.Errorf("template's own outbounds must follow the generated proxy, got %+v", outbounds[1])
	}
}
