package subscription

import (
	"bytes"
	"crypto/ecdh"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// EffectiveInbound is the per-host, per-inbound merged view subscription
// generation actually consumes - the Go equivalent of the "host_inbound"
// dict app/subscription/share.py's process_inbounds_and_tags builds by
// layering a ProxyHost's overrides onto its ProxyInbound's own settings.
// Field-by-field precedence (host wins where it has an opinion, else the
// inbound's own value) matches that function exactly - see its comment
// block for which fields have no inbound-level fallback at all
// (MuxEnable, FragmentSetting, NoiseSetting, RandomUserAgent: host-only).
// JSON tags exist for one reason: internal/httpapi/gateway_status.go
// sends this exact struct as-is to a peer (see its own doc comment) - the
// Gateway feature's whole point is exposing a public-safe, already-merged
// view of a host, which is precisely what this type already is (in
// particular: RealityPublicKey, never the private key BuildEffectiveInbound
// derives it from). No other caller marshals this type.
type EffectiveInbound struct {
	Tag        string `json:"tag"`
	Protocol   string `json:"protocol"`
	Network    string `json:"network"`
	HeaderType string `json:"header_type"`
	Port       int    `json:"port"`
	Address    string `json:"address"`
	SNI        string `json:"sni"`
	HostHeader string `json:"host_header"`
	Path       string `json:"path"`
	Security   string `json:"security"` // none | tls | reality - resolved, never "inbound_default"

	ALPN          string `json:"alpn"`
	Fingerprint   string `json:"fingerprint"`
	AllowInsecure bool   `json:"allow_insecure"`

	RealityPublicKey string `json:"reality_public_key"`
	RealityShortID   string `json:"reality_short_id"`

	MuxEnable       bool   `json:"mux_enable"`
	FragmentSetting string `json:"fragment_setting"`
	NoiseSetting    string `json:"noise_setting"`
	RandomUserAgent bool   `json:"random_user_agent"`

	// Hysteria2ObfsPassword/UpMbps/DownMbps only apply when Protocol is
	// "hysteria2"; CongestionControl/ZeroRTTHandshake only when it's "tuic" -
	// see internal/httpapi/inbounds.go's inboundDetailDTO for what each
	// means. Inbound-level settings, so (unlike everything above) they have
	// no host-level override to layer on top of.
	Hysteria2ObfsPassword string `json:"hysteria2_obfs_password"`
	UpMbps                int    `json:"up_mbps"`
	DownMbps              int    `json:"down_mbps"`
	CongestionControl     string `json:"congestion_control"`
	ZeroRTTHandshake      bool   `json:"zero_rtt_handshake"`

	// SnellPSK/SnellV6Mode only apply when Protocol is "snell" - also
	// inbound-level, no host-level override. See
	// internal/nodecore/snell's own doc comment for why SnellPSK is a
	// separate, inbound-level secret from any one user's own credential.
	SnellPSK    string `json:"snell_psk"`
	SnellV6Mode string `json:"snell_v6_mode"`

	// HysteriaObfsPassword only applies when Protocol is "hysteria" (v1,
	// distinct from hysteria2 above) - also inbound-level, no host-level
	// override. UpMbps/DownMbps above are shared with hysteria2.
	HysteriaObfsPassword string `json:"hysteria_obfs_password"`

	// ShadowTLSInnerMethod/ShadowTLSInnerPassword only apply when Protocol
	// is "shadowtls" - also inbound-level, no host-level override. See
	// internal/nodecore/shadowtls's own doc comment for why this protocol
	// needs an inbound-level secret in addition to each user's own
	// ShadowTLS password.
	ShadowTLSInnerMethod   string `json:"shadowtls_inner_method"`
	ShadowTLSInnerPassword string `json:"shadowtls_inner_password"`
}

// dialsOwnCertificate reports whether a host reaches, by bare address, an inbound
// holding the self-signed certificate the panel generates when none is uploaded. No
// client can verify that one, and nothing names a domain that could have a real one,
// so such a host only works without verification. A host that names a domain, or
// says either way itself, is left alone: a CDN in front of it has a real certificate.
func dialsOwnCertificate(inbound generated.Inbound, host generated.Host, security string) bool {
	if security != "tls" || host.Sni.String != "" {
		return false
	}
	if !strings.Contains(host.Address, "{SERVER_IP}") {
		if _, err := netip.ParseAddr(host.Address); err != nil {
			return false
		}
	}
	block, _ := pem.Decode([]byte(inbound.TlsCertificate.String))
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	return err == nil && bytes.Equal(cert.RawIssuer, cert.RawSubject)
}

// BuildEffectiveInbound merges one Host row onto its parent Inbound row.
func BuildEffectiveInbound(inbound generated.Inbound, host generated.Host) EffectiveInbound {
	security := inbound.Security
	if host.Security != "inbound_default" {
		security = host.Security
	}

	// A host with no port set has nothing to fall back to - the inbounds
	// table doesn't carry a default listen port (see the migration notes
	// for why: this metadata is an interim sync stand-in, not a full
	// mirror of the live proxy config). In practice every real host has
	// its own port; this only matters for a misconfigured one.
	port := 0
	if host.Port.Valid {
		port = int(host.Port.Int32)
	}

	alpn := host.Alpn
	if alpn == "none" {
		alpn = ""
	}
	fingerprint := host.Fingerprint
	if fingerprint == "none" {
		fingerprint = ""
	}

	e := EffectiveInbound{
		Tag: inbound.Tag, Protocol: inbound.Protocol, Network: inbound.Network, HeaderType: inbound.HeaderType.String,
		Port: port, Address: host.Address, Security: security,
		SNI: valueOr(host.Sni, host.Address), HostHeader: valueOr(host.Host, ""),
		Path:          host.Path.String,
		ALPN:          alpn,
		Fingerprint:   fingerprint,
		AllowInsecure: host.Allowinsecure.Bool || (!host.Allowinsecure.Valid && dialsOwnCertificate(inbound, host, security)),
		MuxEnable:     host.MuxEnable, FragmentSetting: host.FragmentSetting.String, NoiseSetting: host.NoiseSetting.String,
		RandomUserAgent:        host.RandomUserAgent,
		Hysteria2ObfsPassword:  inbound.Hysteria2ObfsPassword.String,
		UpMbps:                 int(inbound.UpMbps.Int32),
		DownMbps:               int(inbound.DownMbps.Int32),
		CongestionControl:      inbound.CongestionControl.String,
		ZeroRTTHandshake:       inbound.ZeroRttHandshake,
		SnellPSK:               inbound.SnellPsk.String,
		SnellV6Mode:            inbound.SnellV6Mode.String,
		HysteriaObfsPassword:   inbound.HysteriaObfsPassword.String,
		ShadowTLSInnerMethod:   inbound.ShadowtlsInnerMethod.String,
		ShadowTLSInnerPassword: inbound.ShadowtlsInnerPassword.String,
	}
	if security == "reality" {
		e.RealityPublicKey = derivePublicKey(inbound.RealityPrivateKey.String)
		if len(inbound.RealityShortIds) > 0 {
			e.RealityShortID = inbound.RealityShortIds[0]
		}
		// The server accepts only the name it impersonates, so a host that doesn't
		// set one uses that name rather than its own address.
		e.SNI = valueOr(host.Sni, valueOr(inbound.RealityServerName, host.Address))
		// REALITY is built on uTLS: mihomo refuses it with no fingerprint, and
		// removed the global default that used to cover this.
		if e.Fingerprint == "" {
			e.Fingerprint = "chrome"
		}
	}
	if host.UseSniAsHost {
		e.HostHeader = e.SNI
	}
	return e
}

func valueOr(t pgtype.Text, fallback string) string {
	if t.Valid && t.String != "" {
		return t.String
	}
	return fallback
}

// derivePublicKey computes a REALITY public key from its base64url(no
// padding)-encoded X25519 private key - the same "pbk" value `xray x25519
// -i <private_key>` would print, needed since only the private key is
// stored (the public key is deterministic from it, no reason to store both).
func derivePublicKey(privateKeyB64 string) string {
	raw, err := base64.RawURLEncoding.DecodeString(privateKeyB64)
	if err != nil || len(raw) != 32 {
		return ""
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
}
