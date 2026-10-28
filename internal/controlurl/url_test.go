package controlurl

import "testing"

func TestNormalizeControlPlaneURL(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		invalid           bool
	}{
		{"disabled", "", "", false},
		{"https", " https://panel.example.com/ ", "https://panel.example.com", false},
		{"local IPv4", "http://127.0.0.1:8000/", "http://127.0.0.1:8000", false},
		{"local IPv6", "http://[::1]:8000", "http://[::1]:8000", false},
		{"localhost", "http://localhost:8000", "http://localhost:8000", false},
		{"cleartext remote", "http://panel.example.com", "", true},
		{"misleading localhost", "http://localhost.example.com", "", true},
		{"credentials", "https://operator:test-only@panel.example.com", "", true},
		{"query", "https://panel.example.com/?route=other", "", true},
		{"empty query", "https://panel.example.com/?", "", true},
		{"fragment", "https://panel.example.com/#other", "", true},
		{"path prefix", "https://panel.example.com/prefix", "", true},
		{"relative", "/panel", "", true},
		{"unsupported scheme", "file:///etc/config", "", true},
		{"zero port", "https://panel.example.com:0", "", true},
		{"overflow port", "https://panel.example.com:65536", "", true},
		{"malformed port", "https://panel.example.com:bad", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Normalize(tc.input)
			if tc.invalid {
				if err == nil {
					t.Fatal("unsafe or ambiguous control URL was accepted")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Normalize returned %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
