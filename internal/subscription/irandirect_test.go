package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	boxCertificate "github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	dnstransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing-box/protocol/tun"
	"github.com/sagernet/sing-box/protocol/vless"
	singjson "github.com/sagernet/sing/common/json"

	box "github.com/legendary1205/rapido-go/internal/nodecore/box"
	"github.com/legendary1205/rapido-go/internal/proxysettings"
)

// The profile Happ and INCY receive, byte for byte: a change here changes
// what every Happ user's routing does, so it has to be deliberate.
const wantHappIranDirectProfile = `{"Name":"Iran direct","GlobalProxy":"true","DomainStrategy":"IPIfNonMatch",` +
	`"Geoipurl":"https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/geoip-lite.dat",` +
	`"Geositeurl":"https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/geosite-lite.dat",` +
	`"DirectSites":["domain:ir","geosite:category-ir"],"DirectIp":["geoip:ir","geoip:private"],` +
	`"ProxySites":[],"ProxyIp":[],"BlockSites":[],"BlockIp":[]}`

func TestHappIranDirectRoutingIsAnOnaddLinkToTheProfile(t *testing.T) {
	link := HappIranDirectRouting()
	encoded, ok := strings.CutPrefix(link, "happ://routing/onadd/")
	if !ok {
		t.Fatalf("routing link = %q, want an onadd link so Happ switches to the profile", link)
	}
	// "/" would end the link's path early and "+" can turn into a space on the
	// way through a URL parser; the fixed profile happens to need neither.
	if !regexp.MustCompile(`^[A-Za-z0-9]+=*$`).MatchString(encoded) {
		t.Errorf("base64 %q carries characters a link parser may split on", encoded)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("not base64: %v", err)
	}
	if string(raw) != wantHappIranDirectProfile {
		t.Errorf("profile =\n%s\nwant\n%s", raw, wantHappIranDirectProfile)
	}
	// Proxies and load balancers commonly cap one header near 8 KB.
	if len(link) > 1024 {
		t.Errorf("routing header is %d bytes, keep it well under the usual header limits", len(link))
	}
}

func singBoxDoc(t *testing.T, iranDirect bool) map[string]any {
	t.Helper()
	raw, err := SingBoxConfig([]map[string]any{{"type": "vless", "tag": "node-a"}}, nil, iranDirect)
	if err != nil {
		t.Fatalf("SingBoxConfig: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	return doc
}

func TestSingBoxConfigSendsIranianTrafficDirect(t *testing.T) {
	doc := singBoxDoc(t, true)
	route := doc["route"].(map[string]any)
	if route["final"] != "proxy" {
		t.Errorf("route.final = %v, everything else must still go through the proxy", route["final"])
	}

	wantRules := []any{
		map[string]any{"inbound": "tun-in", "action": "sniff", "timeout": "1s"},
		map[string]any{"protocol": "dns", "action": "hijack-dns"},
		map[string]any{"ip_is_private": true, "outbound": "direct"},
		map[string]any{"domain_suffix": []any{"ir"}, "outbound": "direct"},
		map[string]any{"rule_set": []any{"geosite-ir", "geoip-ir"}, "outbound": "direct"},
	}
	if !reflect.DeepEqual(route["rules"], wantRules) {
		t.Errorf("route.rules = %v\nwant %v", route["rules"], wantRules)
	}

	wantRuleSets := []any{
		map[string]any{
			"tag": "geosite-ir", "type": "remote", "format": "binary",
			"url":             "https://raw.githubusercontent.com/Chocolate4U/Iran-sing-box-rules/rule-set/geosite-ir.srs",
			"download_detour": "proxy", "update_interval": "1d",
		},
		map[string]any{
			"tag": "geoip-ir", "type": "remote", "format": "binary",
			"url":             "https://raw.githubusercontent.com/Chocolate4U/Iran-sing-box-rules/rule-set/geoip-ir.srs",
			"download_detour": "proxy", "update_interval": "1d",
		},
	}
	if !reflect.DeepEqual(route["rule_set"], wantRuleSets) {
		t.Errorf("route.rule_set = %v\nwant %v", route["rule_set"], wantRuleSets)
	}

	// Iranian names resolve on the device's own resolver, by name alone: a
	// rule on a resolved address is the legacy shape sing-box 1.14 deprecated.
	dnsSection := doc["dns"].(map[string]any)
	wantDNSRules := []any{
		map[string]any{"domain_suffix": []any{"ir"}, "server": "dns-direct"},
		map[string]any{"rule_set": []any{"geosite-ir"}, "server": "dns-direct"},
	}
	if !reflect.DeepEqual(dnsSection["rules"], wantDNSRules) {
		t.Errorf("dns.rules = %v\nwant %v", dnsSection["rules"], wantDNSRules)
	}
	if dnsSection["final"] != "dns-remote" {
		t.Errorf("dns.final = %v, every other name must still resolve through the tunnel", dnsSection["final"])
	}

	// Kept between starts, the rule-sets need not download again each time.
	wantExperimental := map[string]any{"cache_file": map[string]any{"enabled": true}}
	if !reflect.DeepEqual(doc["experimental"], wantExperimental) {
		t.Errorf("experimental = %v, want %v", doc["experimental"], wantExperimental)
	}
}

// TestSingBoxConfigIranDirectReferencesOnlyWhatItDeclares: sing-box refuses
// to start over a rule naming an outbound, DNS server or rule-set that is
// not in the document.
func TestSingBoxConfigIranDirectReferencesOnlyWhatItDeclares(t *testing.T) {
	doc := singBoxDoc(t, true)
	outbounds := map[string]bool{}
	for _, o := range doc["outbounds"].([]any) {
		outbounds[o.(map[string]any)["tag"].(string)] = true
	}
	servers := map[string]bool{}
	for _, s := range doc["dns"].(map[string]any)["servers"].([]any) {
		servers[s.(map[string]any)["tag"].(string)] = true
	}
	route := doc["route"].(map[string]any)
	ruleSets := map[string]bool{}
	for _, rs := range route["rule_set"].([]any) {
		rs := rs.(map[string]any)
		ruleSets[rs["tag"].(string)] = true
		if !outbounds[rs["download_detour"].(string)] {
			t.Errorf("rule-set %v downloads through %v, which is no outbound", rs["tag"], rs["download_detour"])
		}
	}
	checkRuleSets := func(where string, rule map[string]any) {
		names, _ := rule["rule_set"].([]any)
		for _, n := range names {
			if !ruleSets[n.(string)] {
				t.Errorf("%s names rule-set %v, which is not declared", where, n)
			}
		}
	}
	for _, r := range route["rules"].([]any) {
		rule := r.(map[string]any)
		if out, ok := rule["outbound"].(string); ok && !outbounds[out] {
			t.Errorf("route rule %v names outbound %q, which is not declared", rule, out)
		}
		checkRuleSets("route rule", rule)
	}
	for _, r := range doc["dns"].(map[string]any)["rules"].([]any) {
		rule := r.(map[string]any)
		if !servers[rule["server"].(string)] {
			t.Errorf("dns rule %v names server %v, which is not declared", rule, rule["server"])
		}
		for _, legacy := range []string{"ip_cidr", "ip_is_private", "ip_accept_any", "rule_set_ip_cidr_match_source"} {
			if _, present := rule[legacy]; present {
				t.Errorf("dns rule %v filters on an address (%s)", rule, legacy)
			}
		}
		checkRuleSets("dns rule", rule)
	}
}

func TestSingBoxConfigWithoutIranDirectIsUnchanged(t *testing.T) {
	doc := singBoxDoc(t, false)
	route := doc["route"].(map[string]any)
	if _, present := route["rule_set"]; present {
		t.Errorf("route.rule_set present with the setting off: %v", route["rule_set"])
	}
	if rules := route["rules"].([]any); len(rules) != 3 {
		t.Errorf("route.rules = %v, want only sniff, hijack-dns and the private bypass", rules)
	}
	if _, present := doc["dns"].(map[string]any)["rules"]; present {
		t.Errorf("dns.rules present with the setting off")
	}
	if _, present := doc["experimental"]; present {
		t.Errorf("experimental present with the setting off: %v", doc["experimental"])
	}
}

// TestSingBoxConfigParsesInSingBox reads both documents with sing-box's own
// option parser (the version this module builds, 1.14), which refuses any
// field it does not know - the check a client runs before anything else.
// The fields used are all from sing-box 1.12 or earlier.
func TestSingBoxConfigParsesInSingBox(t *testing.T) {
	inbounds := inbound.NewRegistry()
	tun.RegisterInbound(inbounds)
	outbounds := outbound.NewRegistry()
	direct.RegisterOutbound(outbounds)
	group.RegisterSelector(outbounds)
	vless.RegisterOutbound(outbounds)
	dnsTransports := dns.NewTransportRegistry()
	local.RegisterTransport(dnsTransports)
	dnstransport.RegisterHTTPS(dnsTransports)
	ctx := box.Context(context.Background(), inbounds, outbounds, endpoint.NewRegistry(), dnsTransports,
		boxService.NewRegistry(), boxCertificate.NewRegistry())

	in := EffectiveInbound{Network: "ws", Port: 443, Security: "tls", SNI: "example.com", Path: "/ws", HostHeader: "example.com"}
	settings := proxysettings.Settings{Type: proxysettings.VLESS, VLESS: &proxysettings.VLESSSettings{ID: "bf000d23-0752-40b4-affe-68f7707a9661"}}
	for _, iranDirect := range []bool{false, true} {
		out, _, err := SingBoxOutbound("node-a", "example.com", in, settings, "")
		if err != nil {
			t.Fatalf("SingBoxOutbound: %v", err)
		}
		raw, err := SingBoxConfig([]map[string]any{out}, nil, iranDirect)
		if err != nil {
			t.Fatalf("SingBoxConfig: %v", err)
		}
		opts, err := singjson.UnmarshalExtendedContext[option.Options](ctx, raw)
		if err != nil {
			t.Fatalf("iranDirect=%v: sing-box refuses the document: %v", iranDirect, err)
		}
		if got := len(opts.Route.RuleSet); iranDirect && got != 2 || !iranDirect && got != 0 {
			t.Errorf("iranDirect=%v: %d rule-sets parsed", iranDirect, got)
		}
	}
}

func TestV2rayJSONFallbackSendsIranianTrafficDirect(t *testing.T) {
	in := EffectiveInbound{Network: "tcp", Port: 443, Security: "tls", SNI: "example.com"}
	settings := proxysettings.Settings{Type: proxysettings.VLESS, VLESS: &proxysettings.VLESSSettings{ID: "uuid-1"}}

	cfg, err := V2rayJSONConfig("My Node", "1.2.3.4", in, settings, nil, true)
	if err != nil {
		t.Fatalf("V2rayJSONConfig: %v", err)
	}
	raw, err := json.Marshal(cfg["routing"])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"domainStrategy":"AsIs","rules":[` +
		`{"domain":["regexp:.*\\.ir$","domain:ir","geosite:private"],"outboundTag":"direct","type":"field"},` +
		`{"ip":["geoip:ir","geoip:private"],"outboundTag":"direct","type":"field"}]}`
	if string(raw) != want {
		t.Errorf("routing =\n%s\nwant\n%s", raw, want)
	}
	// "direct" must exist for the rules to name it.
	outbounds := cfg["outbounds"].([]map[string]any)
	if outbounds[0]["tag"] != "proxy" || outbounds[1]["tag"] != "direct" {
		t.Errorf("outbounds = %v, want the proxy first (Xray's default) and direct next", outbounds)
	}

	off, err := V2rayJSONConfig("My Node", "1.2.3.4", in, settings, nil, false)
	if err != nil {
		t.Fatalf("V2rayJSONConfig: %v", err)
	}
	if _, present := off["routing"]; present {
		t.Errorf("routing present with the setting off: %v", off["routing"])
	}
}

// TestV2rayJSONTemplateIsUntouchedByIranDirect: an operator's template is
// served as written, whatever the setting says - its routing included.
func TestV2rayJSONTemplateIsUntouchedByIranDirect(t *testing.T) {
	templateRouting := map[string]any{"domainStrategy": "IPIfNonMatch", "rules": []any{
		map[string]any{"type": "field", "outboundTag": "block", "domain": []any{"geosite:category-ads-all"}},
	}}
	template := map[string]any{
		"dns":       map[string]any{"servers": []any{"1.1.1.1"}},
		"routing":   templateRouting,
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
	}
	in := EffectiveInbound{Network: "tcp", Port: 443, Security: "tls", SNI: "example.com"}
	settings := proxysettings.Settings{Type: proxysettings.VLESS, VLESS: &proxysettings.VLESSSettings{ID: "uuid-1"}}

	on, err := V2rayJSONConfig("My Node", "1.2.3.4", in, settings, template, true)
	if err != nil {
		t.Fatalf("V2rayJSONConfig: %v", err)
	}
	off, err := V2rayJSONConfig("My Node", "1.2.3.4", in, settings, template, false)
	if err != nil {
		t.Fatalf("V2rayJSONConfig: %v", err)
	}
	if !reflect.DeepEqual(on, off) {
		t.Errorf("the setting changed a templated profile:\non  %v\noff %v", on, off)
	}
	if !reflect.DeepEqual(on["routing"], templateRouting) {
		t.Errorf("routing = %v, want the template's own %v", on["routing"], templateRouting)
	}
}
