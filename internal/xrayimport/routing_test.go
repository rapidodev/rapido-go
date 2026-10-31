package xrayimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func parseRule(t *testing.T, ruleJSON string, splits map[string][]splitTag) (*RoutingRule, string) {
	t.Helper()
	var r xrayRoutingRule
	if err := json.Unmarshal([]byte(ruleJSON), &r); err != nil {
		t.Fatalf("unmarshal %s: %v", ruleJSON, err)
	}
	return parseRoutingRule(r, splits)
}

// The rule every Marzban xray_config ships with, and what the official migration
// script feeds in. It used to come out as `{outbound: BLOCK}`, which matches every
// connection: importing it cut off every user of every node.
func TestTheStockMarzbanRuleKeepsItsCondition(t *testing.T) {
	result, err := ParseXrayConfig([]byte(`{
	  "inbounds": [{"tag": "VLESS TCP", "port": 2053, "protocol": "vless", "settings": {"clients": []}, "streamSettings": {"network": "tcp"}}],
	  "outbounds": [{"protocol": "freedom", "tag": "DIRECT"}, {"protocol": "blackhole", "tag": "BLOCK"}],
	  "routing": {"rules": [{"ip": ["geoip:private"], "outboundTag": "BLOCK", "type": "field"}]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RoutingRules) != 1 {
		t.Fatalf("rules = %+v", result.RoutingRules)
	}
	rule := result.RoutingRules[0]
	if !rule.IPIsPrivate || rule.OutboundTag != "BLOCK" {
		t.Errorf("rule = %+v, want ip_is_private to BLOCK", rule)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "routing rule") {
			t.Errorf("a rule that translates exactly warned: %s", warning)
		}
	}
}

func TestEveryConditionOfARuleIsTranslated(t *testing.T) {
	for name, tc := range map[string]struct {
		rule string
		want RoutingRule
	}{
		"ip list": {
			`{"ip": ["geoip:private", "10.0.0.0/8", "1.1.1.1"], "outboundTag": "x"}`,
			RoutingRule{IPIsPrivate: true, IPCIDR: []string{"10.0.0.0/8", "1.1.1.1"}},
		},
		"every kind of domain": {
			`{"domain": ["example.com", "keyword:ads", "domain:ir", "full:a.example.com", "regexp:^x\\.ir$"], "outboundTag": "x"}`,
			RoutingRule{DomainKeyword: []string{"example.com", "ads"}, DomainSuffix: []string{"ir"}, Domain: []string{"a.example.com"}, DomainRegex: []string{`^x\.ir$`}},
		},
		"the domains spelling": {
			`{"domains": ["domain:ir"], "outboundTag": "x"}`,
			RoutingRule{DomainSuffix: []string{"ir"}},
		},
		"ports as a string": {
			`{"port": "53,443,1000-2000", "outboundTag": "x"}`,
			RoutingRule{Port: []int{53, 443}, PortRange: []string{"1000:2000"}},
		},
		"a port as a number": {
			`{"port": 25, "outboundTag": "x"}`,
			RoutingRule{Port: []int{25}},
		},
		"a range as export writes it": {
			`{"port": "1000:2000", "outboundTag": "x"}`,
			RoutingRule{PortRange: []string{"1000:2000"}},
		},
		"network": {
			`{"network": "tcp, udp", "outboundTag": "x"}`,
			RoutingRule{Network: []string{"tcp", "udp"}},
		},
		"protocol": {
			`{"protocol": ["bittorrent"], "outboundTag": "x"}`,
			RoutingRule{Protocol: []string{"bittorrent"}},
		},
		"source": {
			`{"source": ["geoip:private", "10.0.0.0/8"], "sourcePort": "1000-2000", "outboundTag": "x"}`,
			RoutingRule{SourceIPIsPrivate: true, SourceIPCIDR: []string{"10.0.0.0/8"}, SourcePortRange: []string{"1000:2000"}},
		},
		"user": {
			`{"user": ["ali"], "outboundTag": "x"}`,
			RoutingRule{AuthUser: []string{"ali"}},
		},
		"keys that don't change what matches": {
			`{"port": 25, "ruleTag": "mail", "domainMatcher": "hybrid", "outboundTag": "x"}`,
			RoutingRule{Port: []int{25}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, warning := parseRule(t, tc.rule, nil)
			if got == nil {
				t.Fatalf("skipped: %s", warning)
			}
			if warning != "" {
				t.Errorf("warned: %s", warning)
			}
			tc.want.OutboundTag = "x"
			if !reflect.DeepEqual(*got, tc.want) {
				t.Errorf("got %+v, want %+v", *got, tc.want)
			}
		})
	}
}

// Whatever can't be represented must be skipped: saved without the condition it
// would match more than it says.
func TestARuleThatCantBeRepresentedIsSkippedNotWidened(t *testing.T) {
	splits := map[string][]splitTag{"ok": {{tag: "ok", port: 443}}, "unsupported": nil}
	for name, rule := range map[string]string{
		"no condition at all":                    `{"outboundTag": "x"}`,
		"only a country":                         `{"ip": ["geoip:ir"], "outboundTag": "x"}`,
		"a negated country":                      `{"ip": ["geoip:!private"], "outboundTag": "x"}`,
		"only a geosite":                         `{"domain": ["geosite:category-ads-all"], "outboundTag": "x"}`,
		"only an external list":                  `{"domain": ["ext:custom.dat:ads"], "outboundTag": "x"}`,
		"a broken regexp":                        `{"domain": ["regexp:("], "outboundTag": "x"}`,
		"a condition of a kind Xray added later": `{"port": 25, "vlessRoute": "1-2", "outboundTag": "x"}`,
		"attrs":                                  `{"attrs": {":method": "GET"}, "outboundTag": "x"}`,
		"a balancer":                             `{"port": 25, "balancerTag": "b"}`,
		"no target":                              `{"port": 25}`,
		"both domain and ip":                     `{"domain": ["domain:ir"], "ip": ["1.1.1.1"], "outboundTag": "x"}`,
		"a port out of range":                    `{"port": "70000", "outboundTag": "x"}`,
		"a port that isn't one":                  `{"port": "http", "outboundTag": "x"}`,
		"a network it can't match":               `{"network": "icmp", "outboundTag": "x"}`,
		"only unknown protocols":                 `{"protocol": ["fakedns"], "outboundTag": "x"}`,
		"only unknown sources":                   `{"source": ["geoip:ir"], "outboundTag": "x"}`,
		"a localPort alone":                      `{"localPort": "443", "outboundTag": "x"}`,
		"an inbound not in the file":             `{"inboundTag": "gone", "outboundTag": "x"}`,
		"an inbound that wasn't imported":        `{"inboundTag": "unsupported", "outboundTag": "x"}`,
		"a localPort no inbound has":             `{"inboundTag": "ok", "localPort": "9999", "outboundTag": "x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, warning := parseRule(t, rule, splits)
			if got != nil {
				t.Errorf("kept as %+v", *got)
			}
			if warning == "" {
				t.Error("skipped with no warning, so the admin would never know")
			}
		})
	}
}

// Within one condition entries are alternatives, so losing one only narrows it.
func TestAnEntryItCantRepresentNarrowsTheRuleAndSaysSo(t *testing.T) {
	got, warning := parseRule(t, `{"ip": ["geoip:private", "geoip:ir"], "outboundTag": "x"}`, nil)
	if got == nil || !got.IPIsPrivate {
		t.Fatalf("rule = %+v (%s)", got, warning)
	}
	if !strings.Contains(warning, "geoip:ir") {
		t.Errorf("warning = %q, want it to name what was dropped", warning)
	}
}

func TestARuleSurvivesExportAndImportWithItsMeaning(t *testing.T) {
	for name, rule := range map[string]RoutingRule{
		"private ips":  {IPIsPrivate: true, OutboundTag: "x"},
		"addresses":    {IPCIDR: []string{"10.0.0.0/8"}, OutboundTag: "x"},
		"domains":      {Domain: []string{"a.ir"}, DomainSuffix: []string{"b.ir"}, DomainKeyword: []string{"ads"}, DomainRegex: []string{`^c\.ir$`}, OutboundTag: "x"},
		"ports":        {Port: []int{25}, PortRange: []string{"1000:2000"}, OutboundTag: "x"},
		"network":      {Network: []string{"tcp"}, OutboundTag: "x"},
		"protocol":     {Protocol: []string{"bittorrent"}, OutboundTag: "x"},
		"source":       {SourceIPCIDR: []string{"10.0.0.0/8"}, SourceIPIsPrivate: true, OutboundTag: "x"},
		"source ports": {SourcePort: []int{1234}, SourcePortRange: []string{"1000:2000"}, OutboundTag: "x"},
		"user":         {AuthUser: []string{"ali"}, OutboundTag: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			exported, err := json.Marshal(buildXrayRoutingRule(rule))
			if err != nil {
				t.Fatal(err)
			}
			got, warning := parseRule(t, string(exported), nil)
			if got == nil {
				t.Fatalf("lost on the way back: %s (exported %s)", warning, exported)
			}
			if warning != "" {
				t.Errorf("warned: %s", warning)
			}
			if !reflect.DeepEqual(*got, rule) {
				t.Errorf("got %+v, want %+v (exported %s)", *got, rule, exported)
			}
		})
	}
}

func TestSavedProtocolNamesMatchWhatCoreConfigAccepts(t *testing.T) {
	// internal/httpapi/coreconfig.go's validProtocols, which this package can't
	// import: a name it refuses would fail the whole import when saved.
	for protocol := range sniffedProtocols {
		switch protocol {
		case "tls", "http", "quic", "dns", "stun", "bittorrent", "dtls", "ssh", "rdp", "ntp":
		default:
			t.Errorf("%q isn't accepted by core config", protocol)
		}
	}
}
