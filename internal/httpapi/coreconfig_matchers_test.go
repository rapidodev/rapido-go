package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCoreConfigChecksRuleMatcherValues(t *testing.T) {
	rule := func(mutate func(*routingRuleDTO)) coreConfigDTO {
		r := routingRuleDTO{OutboundTag: "direct"}
		mutate(&r)
		return coreConfigDTO{RoutingRules: []routingRuleDTO{r}}
	}
	cases := []struct {
		name string
		dto  coreConfigDTO
		want string
	}{
		{"cidr and bare ip", rule(func(r *routingRuleDTO) { r.IPCIDR = []string{"10.0.0.0/8", "1.1.1.1", "fc00::/7", "2001:db8::1"} }), ""},
		{"not an ip", rule(func(r *routingRuleDTO) { r.IPCIDR = []string{"geoip:ir"} }), "invalid IP or CIDR geoip:ir"},
		{"prefix too long", rule(func(r *routingRuleDTO) { r.IPCIDR = []string{"10.0.0.0/33"} }), "invalid IP or CIDR"},
		{"source cidr", rule(func(r *routingRuleDTO) { r.SourceIPCIDR = []string{"192.168.0.0/16"} }), ""},
		{"bad source cidr", rule(func(r *routingRuleDTO) { r.SourceIPCIDR = []string{"nope"} }), "invalid IP or CIDR nope"},
		{"port wraps uint16", rule(func(r *routingRuleDTO) { r.Port = []int{70000} }), "invalid port 70000"},
		{"port zero", rule(func(r *routingRuleDTO) { r.Port = []int{0} }), "invalid port 0"},
		{"source port", rule(func(r *routingRuleDTO) { r.SourcePort = []int{53, 443} }), ""},
		{"bad source port", rule(func(r *routingRuleDTO) { r.SourcePort = []int{-1} }), "invalid port -1"},
		{"range", rule(func(r *routingRuleDTO) { r.PortRange = []string{"1000:2000", ":2000", "1000:"} }), ""},
		{"range without a colon", rule(func(r *routingRuleDTO) { r.PortRange = []string{"1000"} }), "invalid port range 1000"},
		{"range open both ways", rule(func(r *routingRuleDTO) { r.PortRange = []string{":"} }), "invalid port range"},
		{"range backwards", rule(func(r *routingRuleDTO) { r.PortRange = []string{"2000:1000"} }), "invalid port range"},
		{"range too high", rule(func(r *routingRuleDTO) { r.SourcePortRange = []string{"1:70000"} }), "invalid port range"},
		{"regex", rule(func(r *routingRuleDTO) { r.DomainRegex = []string{`^.*\.ir$`} }), ""},
		{"bad regex", rule(func(r *routingRuleDTO) { r.DomainRegex = []string{"("} }), "invalid domain_regex"},
		{"users", rule(func(r *routingRuleDTO) { r.AuthUser = []string{"ali"} }), ""},
		{"empty user", rule(func(r *routingRuleDTO) { r.AuthUser = []string{""} }), "auth_user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateCoreConfig(tc.dto)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("validateCoreConfig = %q, want valid", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("validateCoreConfig = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestValidateCoreConfigChecksGroupOptionsAndDNSPort(t *testing.T) {
	member := outboundDTO{Tag: "a", Type: "direct"}
	group := func(g outboundDTO) coreConfigDTO { return coreConfigDTO{Outbounds: []outboundDTO{member, g}} }
	cases := []struct {
		name string
		dto  coreConfigDTO
		want string
	}{
		{"urltest settings", group(outboundDTO{Tag: "g", Type: "urltest", Outbounds: []string{"a"}, URL: "https://example.com/204", Interval: "3m", Tolerance: 50}), ""},
		{"url scheme", group(outboundDTO{Tag: "g", Type: "urltest", Outbounds: []string{"a"}, URL: "ftp://example.com"}), "url must be"},
		{"interval", group(outboundDTO{Tag: "g", Type: "urltest", Outbounds: []string{"a"}, Interval: "soon"}), "interval must be"},
		{"negative interval", group(outboundDTO{Tag: "g", Type: "urltest", Outbounds: []string{"a"}, Interval: "-1m"}), "interval must be"},
		{"tolerance", group(outboundDTO{Tag: "g", Type: "urltest", Outbounds: []string{"a"}, Tolerance: 70000}), "tolerance must be"},
		{"selector default", group(outboundDTO{Tag: "g", Type: "selector", Outbounds: []string{"a"}, Default: "a"}), ""},
		{"selector default not a member", group(outboundDTO{Tag: "g", Type: "selector", Outbounds: []string{"a"}, Default: "b"}), "default must be"},
		{"vless flow", coreConfigDTO{Outbounds: []outboundDTO{{Tag: "v", Type: "vless", Server: "s", ServerPort: 1, UUID: "u", Flow: "xtls-rprx-vision"}}}, ""},
		{"unknown flow", coreConfigDTO{Outbounds: []outboundDTO{{Tag: "v", Type: "vless", Server: "s", ServerPort: 1, UUID: "u", Flow: "xtls-rprx-origin"}}}, "unsupported flow"},
		{"dns port", coreConfigDTO{DNSServers: []dnsServerDTO{{Tag: "d", Type: "udp", Address: "1.1.1.1", Port: 70000}}}, "invalid port 70000"},
		{"dns default port", coreConfigDTO{DNSServers: []dnsServerDTO{{Tag: "d", Type: "udp", Address: "1.1.1.1"}}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validateCoreConfig(tc.dto)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("validateCoreConfig = %q, want valid", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("validateCoreConfig = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestValidateInboundTags(t *testing.T) {
	if msg := validateInboundTags([]inboundDetailDTO{{Tag: "a"}, {Tag: "b"}}); msg != "" {
		t.Fatalf("distinct tags refused: %q", msg)
	}
	if msg := validateInboundTags([]inboundDetailDTO{{Tag: "a"}, {Tag: ""}}); !strings.Contains(msg, "required") {
		t.Fatalf("empty tag: %q", msg)
	}
	if msg := validateInboundTags([]inboundDetailDTO{{Tag: "a"}, {Tag: "a"}}); !strings.Contains(msg, "duplicate") {
		t.Fatalf("duplicate tag: %q", msg)
	}
}

func TestParseNodeConfigSchema(t *testing.T) {
	cases := map[string]nodeConfigSchema{
		"":     {},
		"1":    {},
		"nope": {},
		"2":    {compact: true},
		"3":    {compact: true, delta: true},
		"4":    {compact: true, delta: true, extendedRules: true},
		"5":    {compact: true, delta: true, extendedRules: true},
	}
	for raw, want := range cases {
		if got := parseNodeConfigSchema(raw); got != want {
			t.Errorf("schema %q = %+v, want %+v", raw, got, want)
		}
	}
}

func TestNodeConfigSchemaCacheKeysKeepVariantsApart(t *testing.T) {
	p := nodeProfile{}
	seen := map[string]string{}
	for _, raw := range []string{"", "3", "4"} {
		key := parseNodeConfigSchema(raw).cacheKey(p)
		if other, dup := seen[key]; dup {
			t.Fatalf("schema %q and %q share cache key %q", raw, other, key)
		}
		seen[key] = raw
	}
}

// An older agent drops a matcher it doesn't know and keeps the rest of the rule,
// so what is left would apply to far more traffic than the rule says.
func TestRulesForLeavesOutWhatAnOlderAgentCannotHonour(t *testing.T) {
	plain := routingRuleDTO{Inbound: []string{"main"}, OutboundTag: "a"}
	bySource := routingRuleDTO{Inbound: []string{"main"}, SourceIPCIDR: []string{"10.0.0.0/8"}, OutboundTag: "b"}
	byUser := routingRuleDTO{AuthUser: []string{"ali"}, OutboundTag: "c"}
	last := routingRuleDTO{Domain: []string{"example.com"}, OutboundTag: "d"}
	core := coreConfigDTO{RoutingRules: []routingRuleDTO{plain, bySource, byUser, last}}

	got := nodeConfigSchema{compact: true, delta: true}.rulesFor(core)
	if len(got.RoutingRules) != 2 || got.RoutingRules[0].OutboundTag != "a" || got.RoutingRules[1].OutboundTag != "d" {
		t.Fatalf("older agent got %+v, want only the plain rules, in order", got.RoutingRules)
	}
	if len(core.RoutingRules) != 4 || core.RoutingRules[1].OutboundTag != "b" {
		t.Fatalf("the shared snapshot was modified: %+v", core.RoutingRules)
	}

	all := nodeConfigSchema{extendedRules: true}.rulesFor(core)
	if len(all.RoutingRules) != 4 {
		t.Fatalf("schema 4 agent got %d rules, want all 4", len(all.RoutingRules))
	}
}

// Rolling this out must not restart a single core: with no rule using the new
// matchers, every agent keeps receiving byte-for-byte what it has now.
func TestRulesForDoesNotChangeAConfigUsingNoNewMatchers(t *testing.T) {
	core := coreConfigDTO{LogLevel: "warn", RoutingRules: []routingRuleDTO{
		{Inbound: []string{"main"}, InboundPort: []int{20100}, OutboundTag: "a"},
		{Domain: []string{"example.com"}, IPCIDR: []string{"10.0.0.0/8"}, Port: []int{443}, OutboundTag: "b"},
	}}
	before, err := json.Marshal(core)
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []nodeConfigSchema{{}, {compact: true, delta: true}, {compact: true, delta: true, extendedRules: true}} {
		after, err := json.Marshal(schema.rulesFor(core))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("schema %+v changed the config:\n%s\n%s", schema, before, after)
		}
	}
}

func TestRuleUsesExtendedMatchersCoversEveryNewField(t *testing.T) {
	for name, rule := range map[string]routingRuleDTO{
		"domain_regex":         {DomainRegex: []string{"x"}},
		"source_ip_cidr":       {SourceIPCIDR: []string{"10.0.0.0/8"}},
		"source_ip_is_private": {SourceIPIsPrivate: true},
		"source_port":          {SourcePort: []int{1}},
		"source_port_range":    {SourcePortRange: []string{"1:2"}},
		"auth_user":            {AuthUser: []string{"a"}},
	} {
		if !ruleUsesExtendedMatchers(rule) {
			t.Errorf("%s must count as an extended matcher", name)
		}
	}
	if ruleUsesExtendedMatchers(routingRuleDTO{Inbound: []string{"m"}, InboundPort: []int{1}, Domain: []string{"d"}, IPCIDR: []string{"1.1.1.1"}, Port: []int{1}, PortRange: []string{"1:2"}, Network: []string{"tcp"}, Protocol: []string{"tls"}, IPIsPrivate: true}) {
		t.Error("the original matchers must not count as extended")
	}
}
