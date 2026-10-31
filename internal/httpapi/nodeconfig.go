package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/proxysettings"
)

// The wire shapes below mirror cmd/node/main.go's inboundSpec/userSpec/
// tlsSpec/realitySpec/startRequest byte-for-byte (JSON field names) - the
// two processes communicate over HTTP, not shared Go types, so keeping
// these two definitions in sync by hand is the actual contract. This is
// the same JSON shape POST /start already accepts; the difference here is
// this handler computes it from the DB instead of an admin building it by
// hand.
type nodeConfigUserSpec struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
	Flow     string `json:"flow,omitempty"`
	Method   string `json:"method,omitempty"`
	// UserKey is snell's own per-user secret - see cmd/node/main.go's
	// userSpec (this struct's node-side mirror) for why it isn't folded
	// into Password.
	UserKey string `json:"user_key,omitempty"`
}

type nodeConfigRealitySpec struct {
	PrivateKey string   `json:"private_key"`
	ShortID    []string `json:"short_id"`
	Handshake  struct {
		ServerName string `json:"server_name"`
		ServerPort uint16 `json:"server_port"`
	} `json:"handshake"`
}

type nodeConfigTLSSpec struct {
	ServerName  string                 `json:"server_name"`
	Certificate string                 `json:"certificate"`
	Key         string                 `json:"key"`
	Reality     *nodeConfigRealitySpec `json:"reality,omitempty"`
}

// nodeConfigTransportSpec is the V2Ray transport a vless/vmess/trojan
// inbound's clients wrap the protocol in - absent for plain TCP. Its path
// is the inbound's primary host's path, the same one every client link of
// that inbound carries, so the node answers exactly where clients knock.
type nodeConfigTransportSpec struct {
	Type string `json:"type"` // "ws" | "httpupgrade"
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

// flowApplies is subscription.vlessLink's own rule for when a link carries a
// flow at all: XTLS Vision exists only on raw TCP (or kcp) under TLS/REALITY
// without HTTP header obfuscation. On any other inbound a user's stored flow
// is meaningless - their link never carries it - so the node must not demand
// it either, or every such user fails with "flow mismatch". It matters
// because the write path stores Vision for every VLESS user it touches.
func flowApplies(network, security, headerType string) bool {
	return (security == "tls" || security == "reality") &&
		(network == "tcp" || network == "raw" || network == "kcp") && headerType != "http"
}

// noFlowUsersKey names a protocol's user list with every flow cleared, in
// nodeConfigSnapshot.usersJSON, for inbounds where flowApplies is false.
func noFlowUsersKey(protocol string) string { return protocol + "/no-flow" }

// nodeInboundTransport is the transport a node serves for an inbound of
// this network, or nil when it serves the protocol straight over TCP.
// Only the transports sing-box has a server for are mapped; anything else
// keeps the old plain-TCP behaviour.
func nodeInboundTransport(protocol, network, path, host string) *nodeConfigTransportSpec {
	switch protocol {
	case "vless", "vmess", "trojan":
	default:
		return nil
	}
	switch network {
	case "ws", "httpupgrade":
		if path == "" {
			path = "/"
		}
		return &nodeConfigTransportSpec{Type: network, Path: path, Host: host}
	}
	return nil
}

type nodeConfigInboundSpec struct {
	Tag        string `json:"tag"`
	Protocol   string `json:"protocol"`
	ListenPort uint16 `json:"listen_port"`
	// ListenPorts is every distinct port of the inbound's enabled hosts,
	// ascending, and is only set when there is MORE than one - a single-port
	// inbound serializes exactly as it did before this field existed, and a
	// node that predates it keeps reading ListenPort.
	ListenPorts []uint16                 `json:"listen_ports,omitempty"`
	Users       []nodeConfigUserSpec     `json:"users"`
	TLS         *nodeConfigTLSSpec       `json:"tls,omitempty"`
	Transport   *nodeConfigTransportSpec `json:"transport,omitempty"`

	// Hysteria2/TUIC only apply to a protocol="hysteria2"/"tuic" inbound -
	// see inbounds.go's inboundDetailDTO for what each field means. Present
	// here (rather than folded into the two structs above) because these are
	// per-inbound settings, not per-user or TLS ones.
	Hysteria2ObfsPassword string `json:"hysteria2_obfs_password,omitempty"`
	UpMbps                int32  `json:"up_mbps,omitempty"`
	DownMbps              int32  `json:"down_mbps,omitempty"`
	CongestionControl     string `json:"congestion_control,omitempty"`
	ZeroRTTHandshake      bool   `json:"zero_rtt_handshake,omitempty"`

	// SnellPSK/SnellV6Mode only apply to protocol="snell" - see
	// inbounds.go's inboundDetailDTO for what each means.
	SnellPSK    string `json:"snell_psk,omitempty"`
	SnellV6Mode string `json:"snell_v6_mode,omitempty"`

	// HysteriaObfsPassword only applies to protocol="hysteria" (v1) - UpMbps/
	// DownMbps above are shared with hysteria2 but required for this one
	// instead of optional, see inbounds.go's own validation.
	HysteriaObfsPassword string `json:"hysteria_obfs_password,omitempty"`

	// ShadowTLSInnerMethod/ShadowTLSInnerPassword only apply to
	// protocol="shadowtls" - see inbounds.go's inboundDetailDTO for what
	// each means.
	ShadowTLSInnerMethod   string `json:"shadowtls_inner_method,omitempty"`
	ShadowTLSInnerPassword string `json:"shadowtls_inner_password,omitempty"`
	// ShadowTLSHandshakeServer is the site a client that fails ShadowTLS's
	// authentication gets relayed to, so that a prober sees a real TLS server:
	// the primary host's SNI. See plainHostName for when there isn't one.
	ShadowTLSHandshakeServer string `json:"shadowtls_handshake_server,omitempty"`

	// usersKey is which nodeConfigSnapshot.usersJSON entry holds this
	// inbound's already-encoded Users - the protocol, or its no-flow variant
	// (see flowApplies). Not part of the wire format.
	usersKey string
}

// nodeConfigInboundWire is nodeConfigInboundSpec with the user list already
// encoded. Same fields in the same order, so it marshals to byte-identical
// JSON - the only difference is where the bytes come from.
//
// It exists because every inbound of a protocol carries that protocol's
// ENTIRE user list, and production runs 15 vless inbounds over ~9,900
// users: encoding the spec form re-serialises the same 10k-element array
// fifteen times, which profiling showed to be ~80% of this endpoint's CPU.
// Encoding that array once per protocol and splicing the result into each
// inbound turns fourteen of those fifteen passes into a memmove.
// TestNodeConfigBodyIsByteIdenticalToMarshallingThePayload is what keeps
// the two forms honest.
type nodeConfigInboundWire struct {
	Tag         string                   `json:"tag"`
	Protocol    string                   `json:"protocol"`
	ListenPort  uint16                   `json:"listen_port"`
	ListenPorts []uint16                 `json:"listen_ports,omitempty"`
	Users       json.RawMessage          `json:"users,omitempty"`
	UsersRef    string                   `json:"users_ref,omitempty"`
	TLS         *nodeConfigTLSSpec       `json:"tls,omitempty"`
	Transport   *nodeConfigTransportSpec `json:"transport,omitempty"`

	// Same fields, same order, as nodeConfigInboundSpec above - required for
	// the two to stay byte-identical, see this type's own doc comment.
	Hysteria2ObfsPassword  string `json:"hysteria2_obfs_password,omitempty"`
	UpMbps                 int32  `json:"up_mbps,omitempty"`
	DownMbps               int32  `json:"down_mbps,omitempty"`
	CongestionControl      string `json:"congestion_control,omitempty"`
	ZeroRTTHandshake       bool   `json:"zero_rtt_handshake,omitempty"`
	SnellPSK               string `json:"snell_psk,omitempty"`
	SnellV6Mode            string `json:"snell_v6_mode,omitempty"`
	HysteriaObfsPassword   string `json:"hysteria_obfs_password,omitempty"`
	ShadowTLSInnerMethod   string `json:"shadowtls_inner_method,omitempty"`
	ShadowTLSInnerPassword string `json:"shadowtls_inner_password,omitempty"`

	ShadowTLSHandshakeServer string `json:"shadowtls_handshake_server,omitempty"`
}

// plainHostName keeps what can be dialled as it stands: a host's SNI can also be
// a {VARIABLE} template, a comma-separated list or a wildcard from a migrated
// panel, or an address. Anything else leaves a ShadowTLS inbound with no site to
// relay to, and a client failing its authentication is then closed instead.
func plainHostName(s string) string {
	if !plainHostNameRe.MatchString(s) || !strings.Contains(s, ".") {
		return ""
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return ""
	}
	return s
}

var plainHostNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// nodeConfigResponse is the full payload a node self-applies - see
// cmd/node/main.go's pull loop. Version is a hash of the payload's own
// content, so nodes whose profiles produce the same payload share it.
type nodeConfigResponse struct {
	Version  string                  `json:"version"`
	Inbounds []nodeConfigInboundSpec `json:"inbounds"`
	Core     coreConfigDTO           `json:"core"`
}

// validHostPorts keeps only real TCP/UDP port numbers. hosts.port is a bare
// INTEGER nothing range-checks on write, and the uint16 conversions below
// would otherwise wrap 65536 to 0 and silently collide two hosts' ports.
func validHostPorts(ports []int32) []int {
	out := make([]int, 0, len(ports))
	for _, p := range ports {
		if p >= 1 && p <= 65535 {
			out = append(out, int(p))
		}
	}
	return out
}

// multiListenPorts is the value of an inbound's `listen_ports`: nil unless
// the inbound really has more than one distinct port (see the field's doc).
func multiListenPorts(ports []int32) []uint16 {
	valid := validHostPorts(ports)
	if len(valid) < 2 {
		return nil
	}
	out := make([]uint16, len(valid))
	for i, p := range valid {
		out[i] = uint16(p)
	}
	return out
}

// nodeConfigSnapshot is everything in the payload that is the same whichever
// node asks: the fleet's inbounds with their user lists, each protocol's user
// list already encoded, and the fleet core config. It is built once per data
// version and rendered once per node profile, so several distinct profiles do
// not each re-read and re-marshal ~10k users.
type nodeConfigSnapshot struct {
	version   int64 // the data version read BEFORE the queries below ran
	builtAt   time.Time
	inbounds  []nodeConfigInboundSpec
	usersJSON map[string]json.RawMessage
	core      coreConfigDTO
}

// loadNodeConfigSnapshot is the Go equivalent of
// XRayConfig.include_db_users(): groups every active/on_hold user's proxy
// credentials by protocol, and stuffs them into every auto-sync-eligible
// inbound of that protocol. An inbound is one row per tag however many
// ports its hosts use - those go out as listen_ports.
//
// The core config is read straight from the table, not through its Redis
// cache: this snapshot is stored under a data version that changes whenever
// core_config does, and a stale cached row would defeat exactly that.
func (h *Handler) loadNodeConfigSnapshot(ctx context.Context, version int64) (*nodeConfigSnapshot, error) {
	h.nodeConfig.snapshotLoads.Add(1)
	inboundRows, err := h.store.Queries.ListAutoSyncInbounds(ctx)
	if err != nil {
		return nil, err
	}
	proxyRows, err := h.store.Queries.ListActiveUserProxiesForNodeConfig(ctx)
	if err != nil {
		return nil, err
	}
	coreRow, err := h.store.Queries.GetCoreConfig(ctx)
	if err != nil {
		return nil, err
	}

	usersByProtocol := make(map[string][]nodeConfigUserSpec)
	excludedByUser := map[string]map[string]bool{}
	credentials := make(map[string]bool, len(proxyRows))
	var unservable []string
	for _, p := range proxyRows {
		settings, err := proxysettings.FromStored(proxysettings.ProxyType(p.Type), p.Settings)
		if err != nil {
			continue // a malformed row shouldn't take down every other user's config
		}
		// Nor should one a core can't serve: one bad entry costs a node the
		// whole list, so every later add, revoke and limit stops reaching it.
		if err := settings.Validate(); err != nil {
			unservable = append(unservable, p.Username+" ("+p.Type+"): "+err.Error())
			continue
		}
		// Two users sharing a credential are one account to a core. Ordered by
		// user id, so the older one keeps it; the other's client still connects,
		// and its traffic counts against the older user until this is resolved.
		if field, value := settings.Credential(); field != "" {
			key := p.Type + "/" + value
			if credentials[key] {
				unservable = append(unservable, p.Username+" ("+p.Type+"): shares a credential with an older user")
				continue
			}
			credentials[key] = true
		}
		denied := map[string]bool{}
		for _, tag := range p.ExcludedTags {
			denied[tag] = true
		}
		excludedByUser[p.Type+"/"+p.Username] = denied
		spec := nodeConfigUserSpec{Name: p.Username}
		switch p.Type {
		case "vmess":
			spec.UUID = settings.VMess.ID
		case "vless":
			spec.UUID = settings.VLESS.ID
			spec.Flow = string(settings.VLESS.Flow)
		case "trojan":
			spec.Password = settings.Trojan.Password
			spec.Flow = string(settings.Trojan.Flow)
		case "shadowsocks":
			spec.Password = settings.Shadowsocks.Password
			spec.Method = string(settings.Shadowsocks.Method)
		case "hysteria2":
			spec.Password = settings.Hysteria2.Password
		case "tuic":
			spec.UUID = settings.TUIC.ID
			spec.Password = settings.TUIC.Password
		case "snell":
			spec.UserKey = settings.Snell.UserKey
		case "anytls":
			spec.Password = settings.AnyTLS.Password
		case "hysteria":
			spec.Password = settings.Hysteria.AuthString
		case "naive":
			spec.Password = settings.Naive.Password
		case "shadowtls":
			spec.Password = settings.ShadowTLS.Password
		default:
			continue
		}
		usersByProtocol[p.Type] = append(usersByProtocol[p.Type], spec)
	}

	// A protocol's users with every flow cleared, built only when some inbound
	// of that protocol can't carry one (see flowApplies).
	for _, in := range inboundRows {
		if (in.Protocol != "vless" && in.Protocol != "trojan") || flowApplies(in.Network, in.Security, in.HeaderType.String) {
			continue
		}
		key := noFlowUsersKey(in.Protocol)
		src, ok := usersByProtocol[in.Protocol]
		if _, done := usersByProtocol[key]; done || !ok {
			continue
		}
		stripped := make([]nodeConfigUserSpec, len(src))
		for i, u := range src {
			u.Flow = ""
			stripped[i] = u
		}
		usersByProtocol[key] = stripped
	}

	inbounds := make([]nodeConfigInboundSpec, 0, len(inboundRows))
	for _, in := range inboundRows {
		// Rows written before this was checked: a node would refuse the whole
		// config over one, which is every inbound on it, not just this one.
		if in.Security == "reality" {
			if problem := realityProblem(in.RealityPrivateKey.String, in.RealityServerName.String, in.RealityServerPort.Int32, in.RealityShortIds); problem != "" {
				unservable = append(unservable, "inbound "+in.Tag+": "+problem)
				continue
			}
		}
		usersKey := in.Protocol
		if (in.Protocol == "vless" || in.Protocol == "trojan") && !flowApplies(in.Network, in.Security, in.HeaderType.String) {
			usersKey = noFlowUsersKey(in.Protocol)
		}
		base := usersByProtocol[usersKey]
		filtered := make([]nodeConfigUserSpec, 0, len(base))
		for _, u := range base {
			if !excludedByUser[in.Protocol+"/"+u.Name][in.Tag] {
				filtered = append(filtered, u)
			}
		}
		if len(filtered) != len(base) {
			usersKey += "/" + in.Tag
			usersByProtocol[usersKey] = filtered
		}
		spec := nodeConfigInboundSpec{
			Tag: in.Tag, Protocol: in.Protocol, ListenPort: uint16(in.Port.Int32),
			ListenPorts: multiListenPorts(in.Ports),
			Users:       usersByProtocol[usersKey],
			Transport:   nodeInboundTransport(in.Protocol, in.Network, in.Path.String, in.Host.String),
			usersKey:    usersKey,
		}
		switch in.Security {
		case "reality":
			tls := &nodeConfigTLSSpec{
				ServerName: in.RealityServerName.String,
				Reality: &nodeConfigRealitySpec{
					PrivateKey: in.RealityPrivateKey.String,
					ShortID:    in.RealityShortIds,
				},
			}
			tls.Reality.Handshake.ServerName = in.RealityServerName.String
			tls.Reality.Handshake.ServerPort = uint16(in.RealityServerPort.Int32)
			spec.TLS = tls
		case "tls":
			// ListAutoSyncInbounds only returns a 'tls' row once both are
			// non-empty (see that query's own doc comment) - Certificate/Key
			// are never blank here.
			spec.TLS = &nodeConfigTLSSpec{
				ServerName:  in.TlsServerName.String,
				Certificate: in.TlsCertificate.String,
				Key:         in.TlsKey.String,
			}
		}
		switch in.Protocol {
		case "hysteria2":
			spec.Hysteria2ObfsPassword = in.Hysteria2ObfsPassword.String
			spec.UpMbps = in.UpMbps.Int32
			spec.DownMbps = in.DownMbps.Int32
		case "tuic":
			spec.CongestionControl = in.CongestionControl.String
			spec.ZeroRTTHandshake = in.ZeroRttHandshake
		case "snell":
			spec.SnellPSK = in.SnellPsk.String
			spec.SnellV6Mode = in.SnellV6Mode.String
		case "hysteria":
			spec.UpMbps = in.UpMbps.Int32
			spec.DownMbps = in.DownMbps.Int32
			spec.HysteriaObfsPassword = in.HysteriaObfsPassword.String
		case "shadowtls":
			spec.ShadowTLSInnerMethod = in.ShadowtlsInnerMethod.String
			spec.ShadowTLSInnerPassword = in.ShadowtlsInnerPassword.String
			spec.ShadowTLSHandshakeServer = plainHostName(in.Sni.String)
		}
		if spec.Users == nil {
			spec.Users = []nodeConfigUserSpec{}
		}
		inbounds = append(inbounds, spec)
	}

	h.nodeConfig.noteUnservable(h.logger, unservable)

	// One encode per protocol, reused by every inbound of that protocol - see
	// nodeConfigInboundWire for why.
	usersJSON := make(map[string]json.RawMessage, len(usersByProtocol))
	for proto, list := range usersByProtocol {
		raw, mErr := json.Marshal(list)
		if mErr != nil {
			return nil, mErr
		}
		usersJSON[proto] = raw
	}

	return &nodeConfigSnapshot{
		version: version, builtAt: time.Now(),
		inbounds: inbounds, usersJSON: usersJSON, core: toCoreConfigDTO(coreRow),
	}, nil
}

// render produces one node profile's payload from the snapshot, together with
// the canonical bytes it hashed to produce Version - those bytes are the
// response body bar its first field, see nodeConfigBodyBytes. Marshalling this
// structure is expensive enough (~14 MB in production) that handing the
// result back is worth the slightly wider signature; the alternative was
// marshalling it a second time purely to reproduce what this already had in
// hand.
func (s *nodeConfigSnapshot) render(p nodeProfile) (nodeConfigResponse, []byte, error) {
	return s.renderSchema(p, nodeConfigSchema{})
}

// nodeConfigSchema is what a pulling agent says it understands, as ?schema=N.
type nodeConfigSchema struct {
	// compact: user lists as shared sets (2+), and deltas on top of them (3+).
	compact bool
	delta   bool
	// extendedRules: the routing-rule matchers ruleUsesExtendedMatchers names (4+).
	extendedRules bool
}

func parseNodeConfigSchema(raw string) nodeConfigSchema {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nodeConfigSchema{}
	}
	return nodeConfigSchema{compact: n >= 2, delta: n >= 3, extendedRules: n >= 4}
}

// cacheKey files a rendered payload by everything that shapes its bytes.
func (s nodeConfigSchema) cacheKey(p nodeProfile) string {
	key := p.key()
	if s.compact {
		key += "/v2"
	}
	if s.extendedRules {
		key += "/rules2"
	}
	return key
}

// rulesFor leaves out the rules an agent can't honour. An older agent ignores a
// matcher it doesn't know, so it would apply what is left of such a rule to far
// more traffic than the rule says; not sending the rule fails towards the routing
// around it. A config using none of these is returned untouched, so its bytes and
// version stay the same for every agent and nothing restarts over this.
func (s nodeConfigSchema) rulesFor(core coreConfigDTO) coreConfigDTO {
	if s.extendedRules {
		return core
	}
	for i, rule := range core.RoutingRules {
		if !ruleUsesExtendedMatchers(rule) {
			continue
		}
		kept := append(make([]routingRuleDTO, 0, len(core.RoutingRules)-1), core.RoutingRules[:i]...)
		for _, later := range core.RoutingRules[i+1:] {
			if !ruleUsesExtendedMatchers(later) {
				kept = append(kept, later)
			}
		}
		core.RoutingRules = kept
		break
	}
	return core
}

func (s *nodeConfigSnapshot) renderSchema(p nodeProfile, schema nodeConfigSchema) (nodeConfigResponse, []byte, error) {
	compact := schema.compact
	inbounds := p.filterInbounds(s.inbounds)
	core := schema.rulesFor(p.CoreOverrides.apply(s.core))
	payload := nodeConfigResponse{Inbounds: inbounds, Core: core}

	// emptyUsers matches what the spec form emits for an inbound with no users.
	emptyUsers := json.RawMessage("[]")
	wire := make([]nodeConfigInboundWire, 0, len(inbounds))
	var sets map[string]json.RawMessage
	if compact {
		sets = map[string]json.RawMessage{}
	}
	for i := range inbounds {
		in := &inbounds[i]
		key := in.usersKey
		if key == "" {
			key = in.Protocol
		}
		users, ok := s.usersJSON[key]
		if !ok {
			users = emptyUsers
		}
		ref := ""
		if compact {
			sum := sha256.Sum256(users)
			ref = hex.EncodeToString(sum[:12])
			sets[ref] = users
			users = nil
		}
		wire = append(wire, nodeConfigInboundWire{
			Tag: in.Tag, Protocol: in.Protocol, ListenPort: in.ListenPort, ListenPorts: in.ListenPorts,
			Users: users, UsersRef: ref, TLS: in.TLS, Transport: in.Transport,
			Hysteria2ObfsPassword: in.Hysteria2ObfsPassword, UpMbps: in.UpMbps, DownMbps: in.DownMbps,
			CongestionControl: in.CongestionControl, ZeroRTTHandshake: in.ZeroRTTHandshake,
			SnellPSK: in.SnellPSK, SnellV6Mode: in.SnellV6Mode,
			HysteriaObfsPassword: in.HysteriaObfsPassword,
			ShadowTLSInnerMethod: in.ShadowTLSInnerMethod, ShadowTLSInnerPassword: in.ShadowTLSInnerPassword,
			ShadowTLSHandshakeServer: in.ShadowTLSHandshakeServer,
		})
	}

	canonical, err := json.Marshal(struct {
		Inbounds []nodeConfigInboundWire    `json:"inbounds"`
		Core     coreConfigDTO              `json:"core"`
		UserSets map[string]json.RawMessage `json:"user_sets,omitempty"`
	}{wire, core, sets})
	if err != nil {
		return nodeConfigResponse{}, nil, err
	}
	sum := sha256.Sum256(canonical)
	payload.Version = hex.EncodeToString(sum[:])
	return payload, canonical, nil
}

// buildNodeConfigPayload reads the database and renders one profile's
// payload without touching the cache - the reference the cached path is
// tested against.
func (h *Handler) buildNodeConfigPayload(ctx context.Context, p nodeProfile) (nodeConfigResponse, []byte, error) {
	snap, err := h.loadNodeConfigSnapshot(ctx, 0)
	if err != nil {
		return nodeConfigResponse{}, nil, err
	}
	return snap.render(p)
}

// nodeConfigBodyBytes returns the exact bytes GET /api/internal/node-config
// answers with - identical JSON to marshalling the payload, produced with ONE
// marshal instead of three.
//
// Why that matters: this body is ~14 MB on the production fleet (every
// active user's credentials, repeated once per inbound of their protocol),
// and four nodes re-pull it every few seconds. Marshalling it once for the
// version hash, once for the cache entry and once for the response made this
// single endpoint 662 ms per call and the panel's largest CPU consumer by
// far (79 s of CPU per 5 minutes, ~4x the entire user-list traffic of every
// reseller bot combined).
//
// The splice is exact rather than clever: the canonical struct always
// marshals both fields, so it always begins `{"inbounds":` - putting
// `"version":"..."` directly after the opening brace yields byte-for-byte
// what marshalling nodeConfigResponse produces, since Version is its first
// field. The guard covers the impossible case rather than trusting it.
func nodeConfigBodyBytes(payload nodeConfigResponse, canonical []byte) ([]byte, error) {
	if len(canonical) < 2 || canonical[0] != '{' || canonical[1] != '"' {
		return json.Marshal(payload)
	}
	b := make([]byte, 0, len(canonical)+len(payload.Version)+14)
	b = append(b, `{"version":"`...)
	b = append(b, payload.Version...)
	b = append(b, `",`...)
	b = append(b, canonical[1:]...)
	return b, nil
}

// buildNodeConfigBody is buildNodeConfigPayload plus nodeConfigBodyBytes.
func (h *Handler) buildNodeConfigBody(ctx context.Context, p nodeProfile) (string, error) {
	payload, canonical, err := h.buildNodeConfigPayload(ctx, p)
	if err != nil {
		return "", err
	}
	body, err := nodeConfigBodyBytes(payload, canonical)
	return string(body), err
}

const nodeRowContextKey = "node.row"

// nodeFromContext is the calling node's row: requireNodeSecret already read
// it to authenticate, so the profile costs no second query.
func (h *Handler) nodeFromContext(c *gin.Context) (generated.Node, error) {
	if v, ok := c.Get(nodeRowContextKey); ok {
		if n, ok := v.(generated.Node); ok {
			return n, nil
		}
	}
	return h.store.Queries.GetNodeByID(c.Request.Context(), c.MustGet(nodeIDContextKey).(int32))
}

// etagMatches implements If-None-Match's list, weak-tag and "*" forms.
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}

// handleGetNodeConfig implements GET /api/internal/node-config - a node's
// pull half of the config-sync mechanism (see cmd/node/main.go's pull
// loop). Authenticated the same way as POST /api/internal/node-report
// (requireNodeSecret), which also tells it WHICH node is asking: each node
// is served the payload of its own profile (see nodeProfile), and nodes with
// equal profiles share one cached body.
//
// A poll whose data version is unchanged is answered from memory - see
// cachedNodeConfig - and a node that echoes the ETag back in If-None-Match
// gets an empty 304 instead of the whole body again.
func (h *Handler) handleGetNodeConfig(c *gin.Context) {
	ctx := c.Request.Context()
	node, err := h.nodeFromContext(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not load the node"})
		return
	}
	profile, err := profileFromNode(node)
	if err != nil {
		// Failing closed keeps the node on the config it already runs;
		// falling back to the default would make it serve every inbound.
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Node profile is invalid: " + err.Error()})
		return
	}
	if node.Status == "disabled" {
		c.JSON(http.StatusOK, nodeConfigResponse{Version: "disabled", Inbounds: []nodeConfigInboundSpec{}, Core: coreConfigDTO{LogLevel: "warn"}})
		return
	}
	schema := parseNodeConfigSchema(c.Query("schema"))
	entry, err := h.cachedNodeConfigSchema(ctx, profile, schema)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not build node config"})
		return
	}
	c.Header("ETag", entry.etag)
	if etagMatches(c.GetHeader("If-None-Match"), entry.etag) {
		c.AbortWithStatus(http.StatusNotModified)
		return
	}
	if schema.delta {
		if delta := h.nodeConfig.delta(schema.cacheKey(profile), c.GetHeader("If-None-Match"), entry); delta != nil {
			c.Data(200, "application/json; charset=utf-8", delta)
			return
		}
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", entry.body)
}
