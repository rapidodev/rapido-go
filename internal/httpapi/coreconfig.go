package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// implicitOutboundTags are always available as a routing-rule target even
// with zero custom outbounds configured - cmd/node/main.go's buildOptions
// always emits both, matching the same "direct-out"/"block-out" tags a
// node without any Core Config at all has used since Phase 3.
var implicitOutboundTags = map[string]bool{"direct": true, "block": true}

// validOutboundTypes covers sing-box's real outbound registry (see
// internal/nodecore/registry.go's OutboundRegistry) minus four kinds this
// form's routing-rule model has no honest way to expose: WireGuard is a
// sing-box "Endpoint", a structurally different top-level config section
// (option.Options.Endpoints, not Outbounds, with its own registry this
// codebase doesn't wire in at all yet) rather than another outbound type;
// "dns" is not a destination a user's connection is ever routed to, only a
// target the DNS resolution engine's own rules pick; "bridge" connects to
// one of this same node's other inbounds, a loopback wiring concept nothing
// in Core Config's admin-facing model represents; and "naive" is compiled
// out of sing-box by default behind its own with_naive_outbound build tag,
// because its transport (Chromium's Cronet) drags in large per-platform
// binary blobs this project's build has no reason to carry for one rarely-
// used protocol. Every one of those would need its own future phase, not a
// field bolted onto this one.
var validOutboundTypes = map[string]bool{
	"direct": true, "block": true, "socks": true, "http": true,
	"shadowsocks": true, "vmess": true, "trojan": true, "vless": true,
	"hysteria2": true, "tuic": true, "selector": true, "urltest": true,
	"anytls": true, "hysteria": true, "shadowtls": true,
	"snell": true, "ssh": true, "tor": true,
}
var validDNSTypes = map[string]bool{"local": true, "udp": true, "tcp": true, "tls": true, "https": true}
var validLogLevels = map[string]bool{"trace": true, "debug": true, "info": true, "warn": true, "error": true, "fatal": true, "panic": true}
var validNetworks = map[string]bool{"tcp": true, "udp": true, "icmp": true}
var validProtocols = map[string]bool{"tls": true, "http": true, "quic": true, "dns": true, "stun": true, "bittorrent": true, "dtls": true, "ssh": true, "rdp": true, "ntp": true}
var validShadowsocksMethods = map[string]bool{
	"none": true, "aes-128-gcm": true, "aes-192-gcm": true, "aes-256-gcm": true,
	"chacha20-ietf-poly1305": true, "xchacha20-ietf-poly1305": true,
	"2022-blake3-aes-128-gcm": true, "2022-blake3-aes-256-gcm": true, "2022-blake3-chacha20-poly1305": true,
}
var validVMessSecurity = map[string]bool{"auto": true, "none": true, "zero": true, "aes-128-gcm": true, "chacha20-poly1305": true}
var validCongestionControl = map[string]bool{"cubic": true, "new_reno": true, "bbr": true}
var validSnellObfsModes = map[string]bool{"none": true, "http": true, "tls": true}

type outboundDTO struct {
	Tag        string   `json:"tag" binding:"required"`
	Type       string   `json:"type" binding:"required"`
	Server     string   `json:"server,omitempty"`
	ServerPort int      `json:"server_port,omitempty"`
	Username   string   `json:"username,omitempty"`  // socks/http/ssh (ssh's own field is "user", not "username")
	Password   string   `json:"password,omitempty"`  // socks/http/shadowsocks/trojan/hysteria2/tuic/anytls/shadowtls/ssh; hysteria (v1)'s own auth_str
	Outbounds  []string `json:"outbounds,omitempty"` // selector/urltest member tags

	UUID              string `json:"uuid,omitempty"`               // vmess/vless/tuic
	Flow              string `json:"flow,omitempty"`               // vless (optional - e.g. "xtls-rprx-vision")
	Method            string `json:"method,omitempty"`             // shadowsocks
	Security          string `json:"security,omitempty"`           // vmess encryption
	CongestionControl string `json:"congestion_control,omitempty"` // tuic

	// Version distinguishes wire-incompatible ShadowTLS generations (1-3).
	// Snell has its own such split (4's plain obfuscation vs. 6's PSK
	// derivation scheme) but this form only ever builds a version-4 Snell
	// outbound - 6 has no field here to configure yet - so Version is not
	// reused for it.
	Version int `json:"version,omitempty"` // shadowtls

	// PSK is Snell's pre-shared key - unlike every Password field above,
	// Snell's own wire format keeps this separate from its optional
	// obfuscation settings below, so it gets its own field rather than
	// reusing Password.
	PSK      string `json:"psk,omitempty"`       // snell
	ObfsMode string `json:"obfs_mode,omitempty"` // snell ("none", "http" or "tls")
	ObfsHost string `json:"obfs_host,omitempty"` // snell (only meaningful when obfs_mode is "http" or "tls")
	// Obfs is Hysteria v1's own single obfuscation string - a different,
	// older mechanism from Snell's obfs_mode/obfs_host pair above, despite
	// the similar name.
	Obfs string `json:"obfs,omitempty"` // hysteria (v1)
	// UpMbps/DownMbps are not optional bandwidth hints here: Hysteria v1's
	// client derives its congestion-control ceiling from them (unlike every
	// QUIC-based type above, which just uses BBR) and refuses to start
	// without both set.
	UpMbps   int `json:"up_mbps,omitempty"`   // hysteria (v1)
	DownMbps int `json:"down_mbps,omitempty"` // hysteria (v1)

	// ExecutablePath/DataDirectory are Tor's own fields: this outbound talks
	// to a local `tor` process it spawns itself, not a remote server, so it
	// has no Server/ServerPort at all. The node finds tor on PATH - it does not
	// ship one, so this type only works on a node an admin has installed Tor on
	// by hand. ExecutablePath is refused rather than used: the node runs it as
	// root, which would give every sudo admin a root shell on every node.
	// DataDirectory is a folder name under the node's state directory.
	ExecutablePath string `json:"executable_path,omitempty"` // tor
	DataDirectory  string `json:"data_directory,omitempty"`  // tor

	// TLS* is the common subset every TLS-capable outbound type here
	// shares (vmess/trojan/vless/hysteria2/tuic/anytls/shadowtls/hysteria)
	// - a deliberately small slice of sing-box's full
	// OutboundTLSOptions (no ALPN/cert-pinning/client-cert fields), matching
	// this form's "commonly-needed fields only" scope everywhere else.
	TLSEnabled    bool   `json:"tls_enabled,omitempty"`
	TLSServerName string `json:"tls_server_name,omitempty"`
	TLSInsecure   bool   `json:"tls_insecure,omitempty"`

	// BindInterface binds this outbound's dialer to a specific network
	// interface (e.g. a WireGuard tunnel device name) - the sing-box
	// equivalent of Xray's streamSettings.sockopt.interface, which the
	// real production fleet's per-location exit selection depends on
	// entirely. Meaningless for selector/urltest (group types have no
	// dialer of their own); harmless if set on one, just never read.
	BindInterface string `json:"bind_interface,omitempty"`

	// DirectFallback makes a bind_interface outbound fail open: while that
	// interface is missing or unhealthy on a node, connections dial without
	// the bind (a plain direct route) instead of failing. Only meaningful
	// with BindInterface, and only on a leaf type.
	DirectFallback bool `json:"direct_fallback,omitempty"`

	// URL, Interval and Tolerance tune how a urltest group measures its
	// members, and Default names the member a selector group starts on. A node
	// agent that predates them ignores them and uses sing-box's own defaults,
	// which is harmless, so unlike the rule matchers below they need no gating.
	URL       string `json:"url,omitempty"`       // urltest
	Interval  string `json:"interval,omitempty"`  // urltest, a Go duration such as "3m"
	Tolerance int    `json:"tolerance,omitempty"` // urltest, milliseconds
	Default   string `json:"default,omitempty"`   // selector
}

type routingRuleDTO struct {
	// Inbound matches by which inbound tag a connection arrived through -
	// the mechanism that lets one rule route only e.g. "node1"'s traffic
	// to a specific outbound, matching Xray's inboundTag routing-rule
	// field. Unlike Domain/IPCIDR/etc (free-form data an admin types),
	// entries here must be real inbound tags - validated against the
	// live inbounds table in handleUpdateCoreConfig, not here (this DTO
	// has no DB access).
	Inbound []string `json:"inbound,omitempty"`
	// InboundPort narrows Inbound to the LOCAL listen ports the connection
	// arrived on - what lets one multi-port inbound route each port to its
	// own exit. Not checked against the inbound's hosts: a port that has no
	// host yet simply never matches.
	InboundPort   []int    `json:"inbound_port,omitempty"`
	Domain        []string `json:"domain,omitempty"`
	DomainSuffix  []string `json:"domain_suffix,omitempty"`
	DomainKeyword []string `json:"domain_keyword,omitempty"`
	IPCIDR        []string `json:"ip_cidr,omitempty"`
	IPIsPrivate   bool     `json:"ip_is_private,omitempty"`
	Port          []int    `json:"port,omitempty"`
	PortRange     []string `json:"port_range,omitempty"`
	Network       []string `json:"network,omitempty"`
	Protocol      []string `json:"protocol,omitempty"`

	// Matchers an older node agent doesn't know. It would ignore them and
	// match far more than the rule says, so a rule using any of these only
	// goes to agents that ask for schema 4 - see ruleUsesExtendedMatchers.
	DomainRegex       []string `json:"domain_regex,omitempty"`
	SourceIPCIDR      []string `json:"source_ip_cidr,omitempty"`
	SourceIPIsPrivate bool     `json:"source_ip_is_private,omitempty"`
	SourcePort        []int    `json:"source_port,omitempty"`
	SourcePortRange   []string `json:"source_port_range,omitempty"`
	AuthUser          []string `json:"auth_user,omitempty"`

	OutboundTag string `json:"outbound_tag" binding:"required"`
}

// ruleUsesExtendedMatchers reports whether a rule needs an agent of schema 4.
func ruleUsesExtendedMatchers(r routingRuleDTO) bool {
	return len(r.DomainRegex) > 0 || len(r.SourceIPCIDR) > 0 || r.SourceIPIsPrivate ||
		len(r.SourcePort) > 0 || len(r.SourcePortRange) > 0 || len(r.AuthUser) > 0
}

// validIPOrCIDR accepts the two forms sing-box's ip_cidr matchers take. Anything
// else would pass the panel and then make every node refuse the whole config.
func validIPOrCIDR(s string) bool {
	if _, err := netip.ParsePrefix(s); err == nil {
		return true
	}
	_, err := netip.ParseAddr(s)
	return err == nil
}

// validPortRange accepts "start:end" with either side open, not both.
func validPortRange(s string) bool {
	start, end, ok := strings.Cut(s, ":")
	if !ok || (start == "" && end == "") {
		return false
	}
	lo, hi := uint64(0), uint64(65535)
	var err error
	if start != "" {
		if lo, err = strconv.ParseUint(start, 10, 16); err != nil {
			return false
		}
	}
	if end != "" {
		if hi, err = strconv.ParseUint(end, 10, 16); err != nil {
			return false
		}
	}
	return lo <= hi
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// validateRuleMatchers checks the values the node would otherwise be the first
// to reject: a port the uint16 conversion would wrap, an address or a range
// sing-box can't parse, a regex that doesn't compile.
// ruleHasCondition reports whether a rule narrows what it matches at all.
func ruleHasCondition(rule routingRuleDTO) bool {
	return len(rule.Inbound) > 0 || len(rule.InboundPort) > 0 || len(rule.Domain) > 0 || len(rule.DomainSuffix) > 0 ||
		len(rule.DomainKeyword) > 0 || len(rule.DomainRegex) > 0 || len(rule.IPCIDR) > 0 || rule.IPIsPrivate ||
		len(rule.Port) > 0 || len(rule.PortRange) > 0 || len(rule.Network) > 0 || len(rule.Protocol) > 0 ||
		len(rule.SourceIPCIDR) > 0 || rule.SourceIPIsPrivate || len(rule.SourcePort) > 0 ||
		len(rule.SourcePortRange) > 0 || len(rule.AuthUser) > 0
}

func validateRuleMatchers(rule routingRuleDTO) string {
	for _, list := range [][]int{rule.Port, rule.SourcePort} {
		for _, p := range list {
			if !validPort(p) {
				return fmt.Sprintf("routing rule: invalid port %d (must be 1-65535)", p)
			}
		}
	}
	for _, list := range [][]string{rule.IPCIDR, rule.SourceIPCIDR} {
		for _, ip := range list {
			if !validIPOrCIDR(ip) {
				return "routing rule: invalid IP or CIDR " + ip
			}
		}
	}
	for _, list := range [][]string{rule.PortRange, rule.SourcePortRange} {
		for _, r := range list {
			if !validPortRange(r) {
				return "routing rule: invalid port range " + r + " (use start:end)"
			}
		}
	}
	for _, expr := range rule.DomainRegex {
		if _, err := regexp.Compile(expr); err != nil {
			return "routing rule: invalid domain_regex " + expr
		}
	}
	for _, user := range rule.AuthUser {
		if user == "" {
			return "routing rule: auth_user entries must not be empty"
		}
	}
	return ""
}

// validateGroupOptions checks a balancer's own settings.
func validateGroupOptions(ob outboundDTO) string {
	switch ob.Type {
	case "urltest":
		if ob.URL != "" {
			u, err := url.Parse(ob.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return "outbound " + ob.Tag + ": url must be an http(s) address"
			}
		}
		if ob.Interval != "" {
			if d, err := time.ParseDuration(ob.Interval); err != nil || d <= 0 {
				return "outbound " + ob.Tag + ": interval must be a duration such as 3m"
			}
		}
		if ob.Tolerance < 0 || ob.Tolerance > 65535 {
			return "outbound " + ob.Tag + ": tolerance must be 0-65535"
		}
	case "selector":
		if ob.Default != "" {
			for _, member := range ob.Outbounds {
				if member == ob.Default {
					return ""
				}
			}
			return "outbound " + ob.Tag + ": default must be one of its members"
		}
	}
	return ""
}

type dnsServerDTO struct {
	Tag     string `json:"tag" binding:"required"`
	Type    string `json:"type" binding:"required"`
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
	Path    string `json:"path,omitempty"`
}

type coreConfigDTO struct {
	LogLevel     string           `json:"log_level"`
	SniffEnabled bool             `json:"sniff_enabled"`
	Outbounds    []outboundDTO    `json:"outbounds"`
	RoutingRules []routingRuleDTO `json:"routing_rules"`
	DNSServers   []dnsServerDTO   `json:"dns_servers"`
	UpdatedAt    *time.Time       `json:"updated_at,omitempty"`
}

func toCoreConfigDTO(row generated.CoreConfig) coreConfigDTO {
	dto := coreConfigDTO{
		LogLevel: row.LogLevel, SniffEnabled: row.SniffEnabled,
		Outbounds: []outboundDTO{}, RoutingRules: []routingRuleDTO{}, DNSServers: []dnsServerDTO{},
	}
	_ = json.Unmarshal(row.Outbounds, &dto.Outbounds)
	_ = json.Unmarshal(row.RoutingRules, &dto.RoutingRules)
	_ = json.Unmarshal(row.DnsServers, &dto.DNSServers)
	if row.UpdatedAt.Valid {
		t := row.UpdatedAt.Time
		dto.UpdatedAt = &t
	}
	return dto
}

// handleGetCoreConfig implements GET /api/settings/core-config (sudo only).
func (h *Handler) handleGetCoreConfig(c *gin.Context) {
	row, err := h.store.CachedGetCoreConfig(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read core config"})
		return
	}
	c.JSON(http.StatusOK, toCoreConfigDTO(row))
}

// validateCoreConfig mirrors the current Python system's server-side
// structural validation (XRayConfig's own tag-uniqueness/non-empty checks)
// adapted to this DTO shape - full-object replace, so everything is
// re-checked on every PUT, same as PUT /hosts's own convention.
// validTorDataDirectory is one folder name, kept under the node's state directory:
// tor runs as root there, and a path could point it at anything on the machine.
var validTorDataDirectory = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}$`)

func validateCoreConfig(dto coreConfigDTO) string {
	if dto.LogLevel != "" && !validLogLevels[dto.LogLevel] {
		return "invalid log_level"
	}
	tags := map[string]bool{}
	for _, ob := range dto.Outbounds {
		if ob.ServerPort < 0 || ob.ServerPort > 65535 {
			return "outbound server_port must be 1-65535"
		}
		if ob.Tag == "" || implicitOutboundTags[ob.Tag] {
			return "outbound tag must be non-empty and not 'direct'/'block' (reserved)"
		}
		if tags[ob.Tag] {
			return "duplicate outbound tag: " + ob.Tag
		}
		tags[ob.Tag] = true
		if !validOutboundTypes[ob.Type] {
			return "invalid outbound type: " + ob.Type
		}
		if ob.ExecutablePath != "" {
			return "outbound " + ob.Tag + ": executable_path can't be set from the panel; a node finds tor on its own PATH"
		}
		if ob.DataDirectory != "" && !validTorDataDirectory.MatchString(ob.DataDirectory) {
			return "outbound " + ob.Tag + ": data_directory must be a plain folder name"
		}
		needsServer := ob.Type == "socks" || ob.Type == "http" || ob.Type == "shadowsocks" ||
			ob.Type == "vmess" || ob.Type == "trojan" || ob.Type == "vless" || ob.Type == "hysteria2" || ob.Type == "tuic" ||
			ob.Type == "anytls" || ob.Type == "hysteria" || ob.Type == "shadowtls" ||
			ob.Type == "snell" || ob.Type == "ssh"
		// tor is deliberately absent above: it dials no remote server at all
		// (see ExecutablePath's own doc comment on outboundDTO).
		if needsServer && (ob.Server == "" || ob.ServerPort == 0) {
			return "outbound " + ob.Tag + ": server and server_port are required for type " + ob.Type
		}
		if (ob.Type == "selector" || ob.Type == "urltest") && len(ob.Outbounds) == 0 {
			return "outbound " + ob.Tag + ": at least one member outbound is required for type " + ob.Type
		}
		if msg := validateGroupOptions(ob); msg != "" {
			return msg
		}
		if ob.DirectFallback {
			if ob.BindInterface == "" {
				return "outbound " + ob.Tag + ": direct_fallback requires bind_interface"
			}
			if ob.Type == "selector" || ob.Type == "urltest" || ob.Type == "block" {
				return "outbound " + ob.Tag + ": direct_fallback is not allowed for type " + ob.Type
			}
		}
		switch ob.Type {
		case "shadowsocks":
			if !validShadowsocksMethods[ob.Method] {
				return "outbound " + ob.Tag + ": invalid shadowsocks method " + ob.Method
			}
			if ob.Password == "" {
				return "outbound " + ob.Tag + ": password is required for type shadowsocks"
			}
		case "vmess":
			if ob.UUID == "" {
				return "outbound " + ob.Tag + ": uuid is required for type vmess"
			}
			if !validVMessSecurity[ob.Security] {
				return "outbound " + ob.Tag + ": invalid vmess security " + ob.Security
			}
		case "trojan":
			if ob.Password == "" {
				return "outbound " + ob.Tag + ": password is required for type trojan"
			}
		case "vless":
			if ob.UUID == "" {
				return "outbound " + ob.Tag + ": uuid is required for type vless"
			}
			if ob.Flow != "" && ob.Flow != "xtls-rprx-vision" {
				return "outbound " + ob.Tag + ": unsupported flow " + ob.Flow
			}
		case "hysteria2":
			if ob.Password == "" {
				return "outbound " + ob.Tag + ": password is required for type hysteria2"
			}
		case "tuic":
			if ob.UUID == "" || ob.Password == "" {
				return "outbound " + ob.Tag + ": uuid and password are required for type tuic"
			}
			if ob.CongestionControl != "" && !validCongestionControl[ob.CongestionControl] {
				return "outbound " + ob.Tag + ": invalid congestion_control " + ob.CongestionControl
			}
		case "anytls":
			if ob.Password == "" {
				return "outbound " + ob.Tag + ": password is required for type anytls"
			}
		case "hysteria": // see UpMbps/DownMbps's own doc comment on outboundDTO.
			if ob.UpMbps <= 0 || ob.DownMbps <= 0 {
				return "outbound " + ob.Tag + ": up_mbps and down_mbps (both > 0) are required for type hysteria"
			}
		case "shadowtls": // ShadowTLS v1 predates the password scheme v2/v3 added.
			if ob.Version < 1 || ob.Version > 3 {
				return "outbound " + ob.Tag + ": version must be 1, 2 or 3 for type shadowtls"
			}
			if ob.Version != 1 && ob.Password == "" {
				return "outbound " + ob.Tag + ": password is required for shadowtls version " + strconv.Itoa(ob.Version)
			}
		case "snell":
			if ob.PSK == "" {
				return "outbound " + ob.Tag + ": psk is required for type snell"
			}
			if ob.ObfsMode != "" && !validSnellObfsModes[ob.ObfsMode] {
				return "outbound " + ob.Tag + ": invalid snell obfs_mode " + ob.ObfsMode
			}
		case "ssh":
			// Only password auth is exposed here (see outboundDTO's own doc
			// comment on ExecutablePath/PSK for this form's "commonly-needed
			// fields only" scope) - without it there is nothing for this
			// outbound to authenticate with.
			if ob.Username == "" || ob.Password == "" {
				return "outbound " + ob.Tag + ": username and password are required for type ssh"
			}
		}
	}
	// Member references (selector/urltest) and routing-rule targets both
	// resolve against the same tag universe: custom tags just defined,
	// plus the two implicit built-ins every node always has.
	validTarget := func(tag string) bool { return implicitOutboundTags[tag] || tags[tag] }
	for _, ob := range dto.Outbounds {
		for _, member := range ob.Outbounds {
			if !validTarget(member) {
				return "outbound " + ob.Tag + ": unknown member outbound " + member
			}
		}
	}
	edges := make(map[string][]string, len(dto.Outbounds))
	for _, ob := range dto.Outbounds {
		edges[ob.Tag] = ob.Outbounds
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var cycle func(string) bool
	cycle = func(tag string) bool {
		if visiting[tag] {
			return true
		}
		if done[tag] {
			return false
		}
		visiting[tag] = true
		for _, member := range edges[tag] {
			if cycle(member) {
				return true
			}
		}
		delete(visiting, tag)
		done[tag] = true
		return false
	}
	for tag := range edges {
		if cycle(tag) {
			return "outbound dependency cycle: " + tag
		}
	}
	blockOutbounds := make(map[string]bool)
	for _, ob := range dto.Outbounds {
		if ob.Type == "block" {
			blockOutbounds[ob.Tag] = true
		}
	}
	for _, rule := range dto.RoutingRules {
		if !validTarget(rule.OutboundTag) {
			return "routing rule targets unknown outbound: " + rule.OutboundTag
		}
		for _, n := range rule.Network {
			if !validNetworks[n] {
				return "routing rule: invalid network " + n
			}
		}
		for _, p := range rule.Protocol {
			if !validProtocols[p] {
				return "routing rule: invalid protocol " + p
			}
		}
		if msg := validateRuleMatchers(rule); msg != "" {
			return msg
		}
		// With no condition a rule matches every connection, which can be what an
		// admin wants for an exit. To a block outbound it is every user of every
		// node cut off, and nothing in the save would look wrong.
		if !ruleHasCondition(rule) && (rule.OutboundTag == "block" || blockOutbounds[rule.OutboundTag]) {
			return "routing rule to " + rule.OutboundTag + " has no condition and would block all traffic: add what it should match"
		}
		if len(rule.InboundPort) > 0 {
			if len(rule.Inbound) == 0 {
				return "routing rule: inbound_port requires a non-empty inbound"
			}
			seen := make(map[int]bool, len(rule.InboundPort))
			for _, p := range rule.InboundPort {
				if p < 1 || p > 65535 {
					return fmt.Sprintf("routing rule: invalid inbound_port %d (must be 1-65535)", p)
				}
				if seen[p] {
					return fmt.Sprintf("routing rule: duplicate inbound_port %d", p)
				}
				seen[p] = true
			}
		}
	}
	dnsTags := map[string]bool{}
	for _, srv := range dto.DNSServers {
		if srv.Tag == "" {
			return "dns server tag is required"
		}
		if dnsTags[srv.Tag] {
			return "duplicate dns server tag: " + srv.Tag
		}
		dnsTags[srv.Tag] = true
		if !validDNSTypes[srv.Type] {
			return "invalid dns server type: " + srv.Type
		}
		if srv.Type != "local" && srv.Address == "" {
			return "dns server " + srv.Tag + ": address is required for type " + srv.Type
		}
		if srv.Port != 0 && !validPort(srv.Port) {
			return fmt.Sprintf("dns server %s: invalid port %d (must be 1-65535)", srv.Tag, srv.Port)
		}
	}
	return ""
}

// coreConfigValidationError marks an error as a 400-shaped admin mistake
// (bad field value, unknown reference) rather than an internal failure -
// applyCoreConfig's callers (the plain PUT handler and the Xray-config
// importer) both need to tell the two apart to pick the right HTTP status/
// response shape, without applyCoreConfig itself knowing about gin.
type coreConfigValidationError struct{ msg string }

func (e *coreConfigValidationError) Error() string { return e.msg }

// applyCoreConfig is the real work behind PUT /api/settings/core-config:
// validate, persist, invalidate the caches a stale read would otherwise
// serve from. Extracted out of handleUpdateCoreConfig so the Xray-config
// importer (internal/httpapi/xrayimport.go) can reuse the exact same
// validation and write path instead of duplicating it.
func (h *Handler) applyCoreConfig(ctx context.Context, dto coreConfigDTO) (coreConfigDTO, error) {
	if dto.LogLevel == "" {
		dto.LogLevel = "warn"
	}
	if msg := validateCoreConfig(dto); msg != "" {
		return coreConfigDTO{}, &coreConfigValidationError{msg}
	}

	// A routing rule's `inbound` entries reference a separate resource
	// (the inbounds table) that isn't part of this request body at all,
	// unlike outbound_tag (validated inside validateCoreConfig against
	// this same DTO's own outbound list) - so this check needs a real
	// query and lives here instead of in that pure function.
	needsInboundTags := false
	for _, rule := range dto.RoutingRules {
		if len(rule.Inbound) > 0 {
			needsInboundTags = true
			break
		}
	}
	if needsInboundTags {
		tags, err := h.store.Queries.ListInboundTags(ctx)
		if err != nil {
			return coreConfigDTO{}, fmt.Errorf("could not validate routing rules: %w", err)
		}
		known := make(map[string]bool, len(tags))
		for _, t := range tags {
			known[t] = true
		}
		for _, rule := range dto.RoutingRules {
			for _, tag := range rule.Inbound {
				if !known[tag] {
					return coreConfigDTO{}, &coreConfigValidationError{"routing rule targets unknown inbound: " + tag}
				}
			}
		}
	}

	if msg, err := h.validateNodeOverridesAgainstFleet(ctx, dto); err != nil {
		return coreConfigDTO{}, fmt.Errorf("could not validate node overrides: %w", err)
	} else if msg != "" {
		return coreConfigDTO{}, &coreConfigValidationError{msg}
	}

	outboundsJSON, _ := json.Marshal(dto.Outbounds)
	rulesJSON, _ := json.Marshal(dto.RoutingRules)
	dnsJSON, _ := json.Marshal(dto.DNSServers)

	row, err := h.store.Queries.UpdateCoreConfig(ctx, generated.UpdateCoreConfigParams{
		LogLevel: dto.LogLevel, SniffEnabled: dto.SniffEnabled,
		Outbounds: outboundsJSON, RoutingRules: rulesJSON, DnsServers: dnsJSON,
	})
	if err != nil {
		return coreConfigDTO{}, fmt.Errorf("could not save core config: %w", err)
	}
	if err := h.store.InvalidateCoreConfig(ctx); err != nil {
		h.logger.Warn("invalidate core config cache", "error", err)
	}
	if err := h.store.InvalidateNodeConfigPayload(ctx); err != nil {
		h.logger.Warn("invalidate node config cache", "error", err)
	}
	return toCoreConfigDTO(row), nil
}

// handleUpdateCoreConfig implements PUT /api/settings/core-config (sudo
// only) - a full-object replace, matching PUT /hosts's own convention.
func (h *Handler) handleUpdateCoreConfig(c *gin.Context) {
	var dto coreConfigDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	saved, err := h.applyCoreConfig(c.Request.Context(), dto)
	if err != nil {
		var verr *coreConfigValidationError
		if errors.As(err, &verr) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": verr.msg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, saved)
}
