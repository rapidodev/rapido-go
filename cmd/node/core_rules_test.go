package main

import (
	"context"
	"testing"
	"time"

	sbox "github.com/sagernet/sing-box/option"

	"github.com/legendary1205/rapido-go/internal/nodecore"
	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

func TestBuildCoreOptionsCarriesTheExtendedRuleMatchers(t *testing.T) {
	_, route, _, _, _, err := buildCoreOptions(&coreSpec{RoutingRules: []routingRuleSpec{{
		DomainRegex:       []string{`\.ir$`},
		SourceIPCIDR:      []string{"10.0.0.0/8"},
		SourceIPIsPrivate: true,
		SourcePort:        []int{1234},
		SourcePortRange:   []string{"1000:2000"},
		AuthUser:          []string{"ali"},
		OutboundTag:       "block",
	}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(route.Rules) != 1 {
		t.Fatalf("rules = %d, want 1", len(route.Rules))
	}
	raw := route.Rules[0].DefaultOptions.RawDefaultRule
	if len(raw.DomainRegex) != 1 || raw.DomainRegex[0] != `\.ir$` {
		t.Errorf("domain_regex = %v", raw.DomainRegex)
	}
	if len(raw.SourceIPCIDR) != 1 || raw.SourceIPCIDR[0] != "10.0.0.0/8" {
		t.Errorf("source_ip_cidr = %v", raw.SourceIPCIDR)
	}
	if !raw.SourceIPIsPrivate {
		t.Error("source_ip_is_private was dropped")
	}
	if len(raw.SourcePort) != 1 || raw.SourcePort[0] != 1234 {
		t.Errorf("source_port = %v", raw.SourcePort)
	}
	if len(raw.SourcePortRange) != 1 || raw.SourcePortRange[0] != "1000:2000" {
		t.Errorf("source_port_range = %v", raw.SourcePortRange)
	}
	if len(raw.AuthUser) != 1 || raw.AuthUser[0] != "ali" {
		t.Errorf("auth_user = %v", raw.AuthUser)
	}
}

func TestBuildCoreOptionsCarriesBalancerSettings(t *testing.T) {
	outbounds, _, _, _, _, err := buildCoreOptions(&coreSpec{Outbounds: []outboundSpec{
		{Tag: "a", Type: "direct"},
		{Tag: "auto", Type: "urltest", Outbounds: []string{"a", "direct"}, URL: "https://example.com/204", Interval: "90s", Tolerance: 25},
		{Tag: "pick", Type: "selector", Outbounds: []string{"a", "direct"}, Default: "direct"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, ob := range outbounds {
		switch opts := ob.Options.(type) {
		case *sbox.URLTestOutboundOptions:
			found++
			if opts.URL != "https://example.com/204" || time.Duration(opts.Interval) != 90*time.Second || opts.Tolerance != 25 {
				t.Errorf("urltest = %+v", opts)
			}
		case *sbox.SelectorOutboundOptions:
			found++
			if opts.Default != implicitOutboundTagDirect {
				t.Errorf("selector default = %q, want the built-in direct's real tag", opts.Default)
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d groups, want 2", found)
	}
}

func TestBuildCoreOptionsRejectsABadBalancerInterval(t *testing.T) {
	_, _, _, _, _, err := buildCoreOptions(&coreSpec{Outbounds: []outboundSpec{
		{Tag: "a", Type: "direct"},
		{Tag: "auto", Type: "urltest", Outbounds: []string{"a"}, Interval: "soon"},
	}}, nil)
	if err == nil {
		t.Fatal("want an error for an unparseable interval")
	}
}

// What the forms can produce has to be something sing-box really starts with.
func TestSingBoxAcceptsTheNewRuleMatchersAndBalancerSettings(t *testing.T) {
	opts, err := buildOptions(startRequest{Core: &coreSpec{
		Outbounds: []outboundSpec{
			{Tag: "a", Type: "direct"},
			{Tag: "b", Type: "direct"},
			{Tag: "auto", Type: "urltest", Outbounds: []string{"a", "b"}, URL: "https://127.0.0.1:9/204", Interval: "10m", Tolerance: 50},
			{Tag: "pick", Type: "selector", Outbounds: []string{"a", "b"}, Default: "b"},
		},
		RoutingRules: []routingRuleSpec{
			{DomainRegex: []string{`^.*\.ir$`}, OutboundTag: "block"},
			{SourceIPCIDR: []string{"10.0.0.0/8", "2001:db8::1"}, OutboundTag: "a"},
			{SourceIPIsPrivate: true, OutboundTag: "b"},
			{SourcePort: []int{53}, SourcePortRange: []string{"1000:2000", ":99"}, OutboundTag: "auto"},
			{AuthUser: []string{"ali", "sara"}, OutboundTag: "pick"},
			{IPCIDR: []string{"1.1.1.1", "10.0.0.0/8"}, PortRange: []string{"1000:", ":2000"}, OutboundTag: "direct"},
		},
	}})
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}
	node, err := nodecore.New(context.Background(), opts, traffic.NewManager())
	if err != nil {
		t.Fatalf("nodecore.New: %v", err)
	}
	if err := node.Start(); err != nil {
		t.Fatalf("sing-box rejected the config: %v", err)
	}
	node.Close()
}

// Why the panel checks these values up front: on a node, any one of them makes
// sing-box refuse the whole config, and the node stays on the old one.
func TestSingBoxRefusesWhatThePanelValidates(t *testing.T) {
	for name, rule := range map[string]routingRuleSpec{
		"bad ip":     {IPCIDR: []string{"geoip:ir"}, OutboundTag: "direct"},
		"bad source": {SourceIPCIDR: []string{"nope"}, OutboundTag: "direct"},
		"bad regex":  {DomainRegex: []string{"("}, OutboundTag: "direct"},
	} {
		t.Run(name, func(t *testing.T) {
			opts, err := buildOptions(startRequest{Core: &coreSpec{RoutingRules: []routingRuleSpec{rule}}})
			if err != nil {
				return
			}
			node, err := nodecore.New(context.Background(), opts, traffic.NewManager())
			if err != nil {
				return
			}
			defer node.Close()
			if err := node.Start(); err == nil {
				t.Fatal("sing-box accepted it, so the panel's check is stricter than it needs to be")
			}
		})
	}
}
