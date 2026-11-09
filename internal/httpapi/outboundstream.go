package httpapi

import (
	"encoding/base64"
	"encoding/binary"
	"regexp"
	"strings"
)

// The TLS and transport settings an outbound to another provider's server
// needs: ALPN, a uTLS fingerprint, ECH, REALITY and a V2Ray transport. See
// outboundDTO for what each field is; this file decides where they may appear.

// streamOutboundTypes take a fingerprint, ECH, REALITY and a transport: the
// TCP protocols whose TLS layer is sing-box's own TLS client.
var streamOutboundTypes = map[string]bool{"vless": true, "vmess": true, "trojan": true}

// alpnOutboundTypes take an ALPN list: the TCP ones above, and the QUIC ones,
// whose TLS is always on and which have no use for the rest.
var alpnOutboundTypes = map[string]bool{"vless": true, "vmess": true, "trojan": true, "hysteria2": true, "tuic": true}

// validTLSFingerprints are the ClientHellos sing-box's uTLS client imitates.
var validTLSFingerprints = map[string]bool{
	"chrome": true, "firefox": true, "safari": true, "ios": true, "android": true,
	"edge": true, "360": true, "qq": true, "random": true, "randomized": true,
}

// defaultRealityFingerprint is what a REALITY outbound without a fingerprint
// imitates: REALITY rides on uTLS and needs one, and chrome is what Xray's own
// clients and most share links use.
const defaultRealityFingerprint = "chrome"

var validTransportTypes = map[string]bool{"ws": true, "grpc": true, "httpupgrade": true, "http": true}

var (
	// A REALITY public key is an X25519 key: 32 bytes, base64url without padding.
	realityPublicKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	// A short id is up to 8 bytes in hex, so an even number of digits.
	realityShortIDRe = regexp.MustCompile(`^([0-9a-fA-F]{2}){0,8}$`)
	// An ALPN protocol id as share links and Xray write them: h2, http/1.1, h3.
	alpnRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,32}$`)
	// A Host header value: a name or an address, with an optional port.
	transportHostRe = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(:[0-9]{1,5})?$`)
	// A gRPC service name; Xray also takes a path here, "/" included.
	grpcServiceNameRe = regexp.MustCompile(`^[A-Za-z0-9._/|-]{1,256}$`)
)

// outboundUsesStreamFields reports whether an outbound uses a field a node
// agent older than node-config schema 5 doesn't know.
func outboundUsesStreamFields(ob outboundDTO) bool {
	return len(ob.TLSALPN) > 0 || ob.TLSFingerprint != "" || ob.TLSECHConfig != "" ||
		ob.RealityPublicKey != "" || ob.RealityShortID != "" ||
		ob.TransportType != "" || ob.TransportPath != "" || ob.TransportHost != "" || ob.TransportServiceName != ""
}

// validECHConfigList accepts the base64 of an ECHConfigList: a two-byte length
// followed by exactly that many bytes of configs, the form Xray and share links
// carry it in. Xray also takes a DNS server to look it up from in the same
// field; that isn't a config, and is refused.
func validECHConfigList(s string) bool {
	s = strings.TrimRight(s, "=")
	raw, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.RawURLEncoding.DecodeString(s)
	}
	if err != nil || len(raw) < 6 {
		return false
	}
	return int(binary.BigEndian.Uint16(raw)) == len(raw)-2
}

// validateOutboundStream checks the TLS and transport fields of one outbound:
// that its type uses them, that each value is one the node can build, and that
// they fit together. "" means there is nothing wrong.
func validateOutboundStream(ob outboundDTO) string {
	prefix := "outbound " + ob.Tag + ": "
	stream := streamOutboundTypes[ob.Type]
	notFor := func(field string) string {
		return prefix + field + " is not used by type " + ob.Type
	}
	if len(ob.TLSALPN) > 0 && !alpnOutboundTypes[ob.Type] {
		return notFor("tls_alpn")
	}
	if !stream {
		switch {
		case ob.TLSFingerprint != "":
			return notFor("tls_fingerprint")
		case ob.TLSECHConfig != "":
			return notFor("tls_ech_config")
		case ob.RealityPublicKey != "" || ob.RealityShortID != "":
			return notFor("reality_public_key")
		case ob.TransportType != "" || ob.TransportPath != "" || ob.TransportHost != "" || ob.TransportServiceName != "":
			return notFor("transport_type")
		}
	}

	reality := ob.RealityPublicKey != ""
	// The QUIC types, the only others left here, always run TLS; the TCP ones
	// when asked, or under REALITY.
	tls := ob.TLSEnabled || reality || !stream

	for _, proto := range ob.TLSALPN {
		if !alpnRe.MatchString(proto) {
			return prefix + "invalid tls_alpn entry " + proto + " (use names such as h2 or http/1.1)"
		}
	}
	if len(ob.TLSALPN) > 8 {
		return prefix + "tls_alpn takes at most 8 entries"
	}
	if len(ob.TLSALPN) > 0 && !tls {
		return prefix + "tls_alpn needs TLS (tls_enabled)"
	}
	if ob.TLSFingerprint != "" {
		if !validTLSFingerprints[ob.TLSFingerprint] {
			return prefix + "invalid tls_fingerprint " + ob.TLSFingerprint
		}
		if !tls {
			return prefix + "tls_fingerprint needs TLS (tls_enabled) or REALITY"
		}
	}
	if ob.TLSECHConfig != "" {
		if !validECHConfigList(ob.TLSECHConfig) {
			return prefix + "tls_ech_config must be an ECHConfigList in base64"
		}
		if !ob.TLSEnabled {
			return prefix + "tls_ech_config needs TLS (tls_enabled)"
		}
	}

	if ob.RealityShortID != "" && !reality {
		return prefix + "reality_short_id needs reality_public_key"
	}
	if reality {
		if !realityPublicKeyRe.MatchString(ob.RealityPublicKey) {
			return prefix + "reality_public_key must be 43 characters of base64url (an X25519 public key)"
		}
		if !realityShortIDRe.MatchString(ob.RealityShortID) {
			return prefix + "reality_short_id must be hex, an even number of digits, at most 16"
		}
		if ob.TLSServerName == "" {
			return prefix + "reality needs tls_server_name, one of the names the server's REALITY settings list"
		}
		if ob.TLSECHConfig != "" {
			return prefix + "tls_ech_config can't be combined with reality"
		}
		if len(ob.TLSALPN) > 0 {
			return prefix + "tls_alpn can't be combined with reality: REALITY settings carry no ALPN, and the transport sets what it needs"
		}
		if ob.TLSInsecure {
			return prefix + "tls_insecure means nothing with reality, which checks the server by its key: remove it"
		}
	}

	if msg := validateOutboundTransport(ob, prefix); msg != "" {
		return msg
	}
	// Vision splices the TLS records of what it carries, so it needs a TLS layer
	// of its own right under VLESS: none, or a transport in between, and the
	// node can't start the outbound.
	if ob.Type == "vless" && ob.Flow != "" && (ob.TransportType != "" || !tls) {
		return prefix + "flow " + ob.Flow + " needs TLS or REALITY and no transport"
	}
	return ""
}

func validateOutboundTransport(ob outboundDTO, prefix string) string {
	if ob.TransportType == "" {
		if ob.TransportPath != "" || ob.TransportHost != "" || ob.TransportServiceName != "" {
			return prefix + "transport_path, transport_host and transport_service_name need transport_type"
		}
		return ""
	}
	if !validTransportTypes[ob.TransportType] {
		return prefix + "invalid transport_type " + ob.TransportType + " (use ws, grpc, httpupgrade or http)"
	}
	if ob.RealityPublicKey != "" && (ob.TransportType == "ws" || ob.TransportType == "httpupgrade") {
		return prefix + "reality works over plain TCP, grpc or http, not " + ob.TransportType
	}
	if ob.TransportType == "grpc" {
		if ob.TransportPath != "" || ob.TransportHost != "" {
			return prefix + "a grpc transport takes transport_service_name, not a path or a host"
		}
		if ob.TransportServiceName != "" && !grpcServiceNameRe.MatchString(ob.TransportServiceName) {
			return prefix + "invalid transport_service_name " + ob.TransportServiceName
		}
		return ""
	}
	if ob.TransportServiceName != "" {
		return prefix + "transport_service_name is only for a grpc transport"
	}
	if ob.TransportPath != "" {
		if !strings.HasPrefix(ob.TransportPath, "/") || len(ob.TransportPath) > 512 || strings.ContainsFunc(ob.TransportPath, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
			return prefix + "transport_path must start with / and hold no spaces"
		}
	}
	if ob.TransportHost != "" {
		hosts := []string{ob.TransportHost}
		if ob.TransportType == "http" {
			// HTTP/2 picks one of several hosts per request, as Xray's h2 does.
			hosts = strings.Split(ob.TransportHost, ",")
		}
		for _, host := range hosts {
			if !transportHostRe.MatchString(host) {
				return prefix + "invalid transport_host " + ob.TransportHost
			}
		}
	}
	return ""
}
