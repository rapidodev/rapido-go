package xrayimport

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// xrayRoutingRule is one entry of Xray's routing.rules. Every key that can narrow
// what a rule matches has to be read here: one that is dropped doesn't make the
// rule more limited, it makes it match more, and a rule left with no condition at
// all matches every connection. That is how the stock Marzban rule
// `geoip:private -> BLOCK` used to become "block everything".
type xrayRoutingRule struct {
	Type        string          `json:"type"`
	InboundTag  stringOrList    `json:"inboundTag"`
	LocalPort   json.RawMessage `json:"localPort"`
	OutboundTag string          `json:"outboundTag"`
	BalancerTag string          `json:"balancerTag"`
	Domain      stringOrList    `json:"domain"`
	Domains     stringOrList    `json:"domains"`
	IP          stringOrList    `json:"ip"`
	Port        json.RawMessage `json:"port"`
	Network     stringOrList    `json:"network"`
	Protocol    stringOrList    `json:"protocol"`
	Source      stringOrList    `json:"source"`
	SourceIP    stringOrList    `json:"sourceIP"`
	SourcePort  json.RawMessage `json:"sourcePort"`
	User        stringOrList    `json:"user"`

	// unknownKeys are the keys of this rule nothing above reads. Xray keeps adding
	// conditions, and one this importer has never heard of still narrows the rule.
	unknownKeys []string
}

var knownRuleKeys = map[string]bool{
	"type": true, "inboundTag": true, "localPort": true, "outboundTag": true, "balancerTag": true,
	"domain": true, "domains": true, "ip": true, "port": true, "network": true, "protocol": true,
	"source": true, "sourceIP": true, "sourcePort": true, "user": true,
	// Neither changes what a rule matches: a name for logs, and a matching algorithm.
	"ruleTag": true, "domainMatcher": true,
}

func (r *xrayRoutingRule) UnmarshalJSON(b []byte) error {
	type plain xrayRoutingRule
	if err := json.Unmarshal(b, (*plain)(r)); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	for key := range keys {
		if !knownRuleKeys[key] {
			r.unknownKeys = append(r.unknownKeys, key)
		}
	}
	sort.Strings(r.unknownKeys)
	return nil
}

// sniffedProtocols is what a rule's protocol can name here, internal/httpapi's
// validProtocols: one outside it would fail the whole import when saved.
var sniffedProtocols = map[string]bool{
	"tls": true, "http": true, "quic": true, "dns": true, "stun": true,
	"bittorrent": true, "dtls": true, "ssh": true, "rdp": true, "ntp": true,
}

// ruleBuilder collects what one rule's translation had to say. Within one
// condition an unknown entry only narrows the rule, so it is dropped with a
// warning. A condition with nothing left can't be evaluated at all, and leaving it
// out would widen the rule, so that skips the whole rule.
type ruleBuilder struct {
	warning string
	skip    bool
}

func (b *ruleBuilder) warn(format string, args ...any) {
	b.warning = appendWarning(b.warning, fmt.Sprintf(format, args...))
}

func (b *ruleBuilder) skipRule(format string, args ...any) {
	b.warn(format, args...)
	b.skip = true
}

// parseRoutingRule resolves one Xray routing rule into this codebase's shape. A
// rule naming both an inboundTag and a localPort matches exactly the one split tag
// for that port; one naming only an inboundTag matches every split tag that
// original inbound produced. A rule that can't be represented is skipped with a
// warning: never saved as something that matches more than it says.
func parseRoutingRule(r xrayRoutingRule, tagSplits map[string][]splitTag) (*RoutingRule, string) {
	if r.Type != "" && r.Type != "field" {
		return nil, fmt.Sprintf("rule type %q is not supported here, skipped", r.Type)
	}
	if r.OutboundTag == "" {
		if r.BalancerTag != "" {
			return nil, fmt.Sprintf("routes to balancer %q, which has no equivalent here, skipped", r.BalancerTag)
		}
		return nil, "names no outboundTag, skipped"
	}
	if len(r.unknownKeys) > 0 {
		return nil, fmt.Sprintf("uses %s, which can't be represented here, skipped", strings.Join(r.unknownKeys, ", "))
	}

	rule := &RoutingRule{OutboundTag: r.OutboundTag}
	b := &ruleBuilder{}
	b.parseInbounds(r, tagSplits, rule)
	b.parseDomains(append(append(stringOrList{}, r.Domain...), r.Domains...), rule)
	b.parseIPs(r.IP, rule)
	if (len(r.Domain) > 0 || len(r.Domains) > 0) && len(r.IP) > 0 {
		// Xray requires both to match, sing-box either: this would match more.
		b.skipRule("names both a domain and an ip, which can't both be required here, skipped")
	}
	rule.Port, rule.PortRange = b.parsePorts(r.Port, "port")
	rule.SourcePort, rule.SourcePortRange = b.parsePorts(r.SourcePort, "sourcePort")
	b.parseNetworks(r.Network, rule)
	b.parseProtocols(r.Protocol, rule)
	b.parseSources(append(append(stringOrList{}, r.Source...), r.SourceIP...), rule)
	rule.AuthUser = r.User

	if b.skip {
		return nil, b.warning
	}
	if !rule.hasCondition() {
		return nil, appendWarning(b.warning, "has no condition this panel can represent, and would match all traffic, skipped")
	}
	return rule, b.warning
}

func (r *RoutingRule) hasCondition() bool {
	return len(r.Inbound) > 0 || len(r.Domain) > 0 || len(r.DomainSuffix) > 0 || len(r.DomainKeyword) > 0 ||
		len(r.DomainRegex) > 0 || len(r.IPCIDR) > 0 || r.IPIsPrivate || len(r.Port) > 0 || len(r.PortRange) > 0 ||
		len(r.Network) > 0 || len(r.Protocol) > 0 || len(r.SourceIPCIDR) > 0 || r.SourceIPIsPrivate ||
		len(r.SourcePort) > 0 || len(r.SourcePortRange) > 0 || len(r.AuthUser) > 0
}

func (b *ruleBuilder) parseInbounds(r xrayRoutingRule, tagSplits map[string][]splitTag, rule *RoutingRule) {
	if len(r.InboundTag) == 0 {
		if len(r.LocalPort) > 0 {
			b.skipRule("names a localPort with no inboundTag, which can't be represented here, skipped")
		}
		return
	}
	ports, portWarnings := parsePortList(r.LocalPort, "routing rule localPort")
	if len(portWarnings) > 0 {
		b.warn("%s", strings.Join(portWarnings, "; "))
	}
	for _, origTag := range r.InboundTag {
		splits, ok := tagSplits[origTag]
		if !ok {
			b.warn("references inbound tag %q, which wasn't found among this file's own inbounds, skipped for that tag", origTag)
			continue
		}
		if len(ports) == 0 {
			// No localPort named - matches every port that original tag
			// expanded into.
			for _, s := range splits {
				rule.Inbound = append(rule.Inbound, s.tag)
			}
			continue
		}
		for _, p := range ports {
			found := false
			for _, s := range splits {
				if s.port == p {
					rule.Inbound = append(rule.Inbound, s.tag)
					found = true
					break
				}
			}
			if !found {
				b.warn("localPort %d doesn't match any port inbound %q actually has, skipped", p, origTag)
			}
		}
	}
	if len(rule.Inbound) == 0 {
		b.skipRule("none of the inbounds it names were imported, skipped")
	}
}

func (b *ruleBuilder) parseDomains(entries stringOrList, rule *RoutingRule) {
	if len(entries) == 0 {
		return
	}
	kept := 0
	for _, entry := range entries {
		kind, value, hasKind := strings.Cut(entry, ":")
		switch {
		case !hasKind:
			// A bare string is a substring match in Xray.
			rule.DomainKeyword = append(rule.DomainKeyword, entry)
		case kind == "keyword" && value != "":
			rule.DomainKeyword = append(rule.DomainKeyword, value)
		case kind == "domain" && value != "":
			rule.DomainSuffix = append(rule.DomainSuffix, value)
		case kind == "full" && value != "":
			rule.Domain = append(rule.Domain, value)
		case kind == "regexp" && validRegexp(value):
			rule.DomainRegex = append(rule.DomainRegex, value)
		default:
			b.warn("domain %q can't be represented here, dropped from the rule", entry)
			continue
		}
		kept++
	}
	if kept == 0 {
		b.skipRule("none of its domains can be represented here, skipped")
	}
}

func validRegexp(expr string) bool {
	if expr == "" {
		return false
	}
	_, err := regexp.Compile(expr)
	return err == nil
}

func validIPOrCIDR(s string) bool {
	if _, err := netip.ParsePrefix(s); err == nil {
		return true
	}
	_, err := netip.ParseAddr(s)
	return err == nil
}

func (b *ruleBuilder) parseIPs(entries stringOrList, rule *RoutingRule) {
	if len(entries) == 0 {
		return
	}
	kept := 0
	for _, entry := range entries {
		switch {
		case entry == "geoip:private":
			rule.IPIsPrivate = true
		case validIPOrCIDR(entry):
			rule.IPCIDR = append(rule.IPCIDR, entry)
		default:
			b.warn("ip %q can't be represented here, dropped from the rule", entry)
			continue
		}
		kept++
	}
	if kept == 0 {
		b.skipRule("none of its ips can be represented here, skipped")
	}
}

func (b *ruleBuilder) parseSources(entries stringOrList, rule *RoutingRule) {
	if len(entries) == 0 {
		return
	}
	kept := 0
	for _, entry := range entries {
		switch {
		case entry == "geoip:private":
			rule.SourceIPIsPrivate = true
		case validIPOrCIDR(entry):
			rule.SourceIPCIDR = append(rule.SourceIPCIDR, entry)
		default:
			b.warn("source %q can't be represented here, dropped from the rule", entry)
			continue
		}
		kept++
	}
	if kept == 0 {
		b.skipRule("none of its sources can be represented here, skipped")
	}
}

func (b *ruleBuilder) parseNetworks(entries stringOrList, rule *RoutingRule) {
	for _, entry := range entries {
		// Xray writes this as one comma-separated string.
		for _, network := range strings.Split(entry, ",") {
			network = strings.ToLower(strings.TrimSpace(network))
			switch network {
			case "":
			case "tcp", "udp":
				rule.Network = append(rule.Network, network)
			default:
				b.skipRule("network %q can't be represented here, skipped", network)
			}
		}
	}
}

func (b *ruleBuilder) parseProtocols(entries stringOrList, rule *RoutingRule) {
	if len(entries) == 0 {
		return
	}
	for _, entry := range entries {
		if sniffedProtocols[entry] {
			rule.Protocol = append(rule.Protocol, entry)
		} else {
			b.warn("protocol %q can't be represented here, dropped from the rule", entry)
		}
	}
	if len(rule.Protocol) == 0 {
		b.skipRule("none of its protocols can be represented here, skipped")
	}
}

// parsePorts reads a destination or source port condition: a number, or a string
// like "53,443,1000-2000". Unlike a localPort a range is fine here, in this
// codebase's own "start:end" form, which is also how a rule comes back from export.
func (b *ruleBuilder) parsePorts(raw json.RawMessage, field string) ([]int, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		text = number.String()
	} else if err := json.Unmarshal(raw, &text); err != nil {
		b.skipRule("%s is neither a number nor a string, skipped", field)
		return nil, nil
	}
	var ports []int
	var ranges []string
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		low, high, isRange := strings.Cut(strings.ReplaceAll(part, "-", ":"), ":")
		if !isRange {
			port, ok := parsePort(low)
			if !ok {
				b.skipRule("%s %q is not a port, skipped", field, part)
				return nil, nil
			}
			ports = append(ports, port)
			continue
		}
		if _, ok := parsePort(low); !ok {
			b.skipRule("%s %q is not a port range, skipped", field, part)
			return nil, nil
		}
		if _, ok := parsePort(high); !ok {
			b.skipRule("%s %q is not a port range, skipped", field, part)
			return nil, nil
		}
		ranges = append(ranges, low+":"+high)
	}
	return ports, ranges
}

func parsePort(s string) (int, bool) {
	port, err := strconv.Atoi(s)
	return port, err == nil && port >= 1 && port <= 65535
}
