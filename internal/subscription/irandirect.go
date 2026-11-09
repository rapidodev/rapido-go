package subscription

import (
	"encoding/base64"
	"encoding/json"
)

// Iranian sites and IPs must leave from the user's own connection, never
// through the VPN: an Iranian site seen from a foreign exit is slower, often
// refuses the visit outright, and costs the user international traffic for
// what is domestic. Only the client app can split traffic like that, so the
// subscription tells each app what to send direct in whatever form that app
// understands:
//
//   - Happ and INCY, on a share-link subscription: a routing profile in the
//     "routing" response header (HappIranDirectRouting).
//   - sing-box: route and DNS rules plus two remote rule-sets (SingBoxConfig).
//   - v2ray-json with no operator template: a routing block (V2rayJSONConfig).
//     An operator's own template is served untouched.
//   - Clash: the built-in template already does this through its
//     rule-providers, and an operator's template is the operator's own.
//
// Every rule list comes from Chocolate4U's Iran rules, the set the built-in
// Clash template uses too.
const (
	// The v2ray geo files a Happ profile downloads. The lite builds hold the
	// categories the profile names (geoip: ir, private; geosite: category-ir)
	// in about 2 MB, where the full builds are about 25 MB: Happ gives up on a
	// geo file download after 3 minutes, which a 25 MB file over an Iranian
	// connection can easily take.
	iranGeoIPDatURL   = "https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/geoip-lite.dat"
	iranGeoSiteDatURL = "https://raw.githubusercontent.com/Chocolate4U/Iran-v2ray-rules/release/geosite-lite.dat"

	// The sing-box rule-sets (binary .srs) on the repository's rule-set branch.
	iranGeoIPRuleSetURL   = "https://raw.githubusercontent.com/Chocolate4U/Iran-sing-box-rules/rule-set/geoip-ir.srs"
	iranGeoSiteRuleSetURL = "https://raw.githubusercontent.com/Chocolate4U/Iran-sing-box-rules/rule-set/geosite-ir.srs"

	// HappRoutingHeader is the response header Happ and INCY read a routing
	// profile from.
	HappRoutingHeader = "routing"
)

// happRoutingProfile is a Happ routing profile (happ.su/main/dev-docs/routing).
// Happ fills every field left out from its own default profile, which is why
// there are no DNS fields: by default it resolves proxied names through the
// tunnel ("remote" DNS) and direct ones over the user's own connection
// ("domestic" DNS), which is the split wanted here.
type happRoutingProfile struct {
	// Name stays the same for good: Happ replaces a profile of the same name
	// on every subscription update instead of adding a second one.
	Name string `json:"Name"`
	// GlobalProxy "true": whatever no rule names still goes through the VPN.
	GlobalProxy string `json:"GlobalProxy"`
	// IPIfNonMatch resolves a name that no site rule matched so the IP rules
	// get their turn, catching Iranian services on non-Iranian domains.
	DomainStrategy string   `json:"DomainStrategy"`
	Geoipurl       string   `json:"Geoipurl"`
	Geositeurl     string   `json:"Geositeurl"`
	DirectSites    []string `json:"DirectSites"`
	DirectIp       []string `json:"DirectIp"`
	ProxySites     []string `json:"ProxySites"`
	ProxyIp        []string `json:"ProxyIp"`
	BlockSites     []string `json:"BlockSites"`
	BlockIp        []string `json:"BlockIp"`
}

// happIranDirectProfile is the profile HappIranDirectRouting carries.
//
// geosite:category-ir rather than geosite:ir: Happ can end up running the
// profile on the geo files it already has - while a download is under way, or
// after one failed - and its defaults (Loyalsoldier's v2ray-rules-dat, built
// from v2fly's lists) carry category-ir but no ir, while Chocolate4U's carry
// both. A category missing from the file in use stops Xray from starting at
// all, so the name both files know is the safe one. domain:ir needs no geo
// file.
var happIranDirectProfile = happRoutingProfile{
	Name:           "Iran direct",
	GlobalProxy:    "true",
	DomainStrategy: "IPIfNonMatch",
	Geoipurl:       iranGeoIPDatURL,
	Geositeurl:     iranGeoSiteDatURL,
	DirectSites:    []string{"domain:ir", "geosite:category-ir"},
	DirectIp:       []string{"geoip:ir", "geoip:private"},
	ProxySites:     []string{},
	ProxyIp:        []string{},
	BlockSites:     []string{},
	BlockIp:        []string{},
}

// happIranDirectRouting is computed once: the profile never changes at runtime.
var happIranDirectRouting = func() string {
	raw, err := json.Marshal(happIranDirectProfile)
	if err != nil {
		panic(err) // a fixed struct of strings always marshals
	}
	return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(raw)
}()

// HappIranDirectRouting is the HappRoutingHeader value: an "onadd" link, so
// Happ adds the profile and switches to it. INCY accepts the same link.
func HappIranDirectRouting() string {
	return happIranDirectRouting
}

// addSingBoxIranDirect turns SingBoxConfig's document into one that sends
// Iranian traffic direct: route rules after the private-address bypass, DNS
// rules for Iranian names, the two rule-sets, and the cache file. The cache
// file keeps the rule-sets between starts: without it every start downloads
// them again before any traffic flows, and a first download that fails stops
// sing-box from starting at all.
func addSingBoxIranDirect(doc map[string]any) {
	route := doc["route"].(map[string]any)
	route["rules"] = append(route["rules"].([]map[string]any), singBoxIranRouteRules()...)
	route["rule_set"] = singBoxIranRuleSets()
	doc["dns"].(map[string]any)["rules"] = singBoxIranDNSRules()
	doc["experimental"] = map[string]any{"cache_file": map[string]any{"enabled": true}}
}

// singBoxIranRuleSetTags are the remote rule-sets singBoxIranRuleSets declares.
const (
	singBoxIranGeoSiteTag = "geosite-ir"
	singBoxIranGeoIPTag   = "geoip-ir"
)

// singBoxIranRuleSets declares the two remote rule-sets. They download
// through "proxy", the selector, so a filtered GitHub cannot keep them from
// arriving, and refresh daily like the built-in Clash template's providers.
// download_detour is what sing-box 1.12 and 1.13 know; 1.14 only warns about
// it (http_client replaces it there, but would be refused by older clients).
func singBoxIranRuleSets() []map[string]any {
	remote := func(tag, url string) map[string]any {
		return map[string]any{
			"tag": tag, "type": "remote", "format": "binary", "url": url,
			"download_detour": "proxy", "update_interval": "1d",
		}
	}
	return []map[string]any{
		remote(singBoxIranGeoSiteTag, iranGeoSiteRuleSetURL),
		remote(singBoxIranGeoIPTag, iranGeoIPRuleSetURL),
	}
}

// singBoxIranRouteRules send Iranian traffic to the "direct" outbound. The
// inline .ir suffix needs no download; the rule-sets add Iranian sites on
// other domains and Iranian IP ranges.
func singBoxIranRouteRules() []map[string]any {
	return []map[string]any{
		{"domain_suffix": []string{"ir"}, "outbound": "direct"},
		{"rule_set": []string{singBoxIranGeoSiteTag, singBoxIranGeoIPTag}, "outbound": "direct"},
	}
}

// singBoxIranDNSRules resolve Iranian names with "dns-direct", the device's
// own resolver: the answer is then the one an Iranian visitor gets (a CDN
// edge inside Iran, not abroad), and the query itself stays off the VPN like
// the traffic it is for. They match names only - no address filter, the
// legacy DNS rule shape sing-box 1.14 deprecated - and geosite-ir is a
// domain-only list.
func singBoxIranDNSRules() []map[string]any {
	return []map[string]any{
		{"domain_suffix": []string{"ir"}, "server": "dns-direct"},
		{"rule_set": []string{singBoxIranGeoSiteTag}, "server": "dns-direct"},
	}
}

// v2rayJSONIranRouting is the built-in v2ray-json profile's routing, the
// direct rules operators of this panel already put in their own v2ray
// templates. AsIs, not IPIfNonMatch:
// this profile has no DNS block, so resolving a name to test the IP rules
// would ask the device's own resolver about every site, foreign ones
// included, and a name that resolver blocks comes back as a 10.10.34.x
// address that geoip:private would then send direct, into the block page.
// Under AsIs the IP rules still match every connection made to an IP, which
// is what an app in VPN mode makes. The regexp is anchored: Xray matches a
// regexp anywhere in the name, so an open-ended ".*\.ir" would also catch
// names like www.irs.gov. Only categories the clients' own bundled geo files
// carry: this profile cannot tell them to download others.
func v2rayJSONIranRouting() map[string]any {
	return map[string]any{
		"domainStrategy": "AsIs",
		"rules": []map[string]any{
			{"type": "field", "outboundTag": "direct", "domain": []string{`regexp:.*\.ir$`, "domain:ir", "geosite:private"}},
			{"type": "field", "outboundTag": "direct", "ip": []string{"geoip:ir", "geoip:private"}},
		},
	}
}
