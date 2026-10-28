// Package controlurl validates the URL a node uses to send its report secret
// and pull its desired configuration from the panel.
package controlurl

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Normalize requires HTTPS for remote control-plane traffic. Cleartext HTTP
// is allowed only for loopback development/test endpoints. Credentials,
// query strings, fragments, and path prefixes are rejected so secrets cannot
// be sent to an unexpected URL or an ambiguous route.
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Hostname() == "" {
		return "", fmt.Errorf("panel URL must be an absolute http(s) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("panel URL must not contain credentials, a path, a query, or a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		host := strings.ToLower(u.Hostname())
		loopback := host == "localhost"
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil && ip.IsLoopback() {
			loopback = true
		}
		if !loopback {
			return "", fmt.Errorf("panel URL must use HTTPS except for loopback development endpoints")
		}
	default:
		return "", fmt.Errorf("panel URL scheme must be http or https")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("panel URL has an invalid port")
		}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	return strings.TrimRight(u.String(), "/"), nil
}
