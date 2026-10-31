package main

import "testing"

func TestRealityOptionsAreWhatSingBoxAccepts(t *testing.T) {
	spec := &realitySpec{PrivateKey: "k", ShortID: []string{"abcd"}}
	spec.Handshake.ServerName = "www.example.com"

	opts := buildInboundTLS(inboundSpec{TLS: &tlsSpec{Reality: spec}})
	if len(opts.Certificate) != 0 || len(opts.Key) != 0 {
		t.Errorf("certificate and key are refused next to REALITY, got %q / %q", opts.Certificate, opts.Key)
	}
	if opts.ServerName != "www.example.com" {
		t.Errorf("server_name = %q: it is the only client SNI REALITY accepts, so it falls back to the target", opts.ServerName)
	}
	if got := opts.Reality.Handshake.ServerPort; got != 443 {
		t.Errorf("handshake port = %d, want the HTTPS default for an unset one", got)
	}

	opts = buildInboundTLS(inboundSpec{TLS: &tlsSpec{ServerName: "other.example.com", Reality: spec}})
	if opts.ServerName != "other.example.com" {
		t.Errorf("server_name = %q, want the one the panel sent", opts.ServerName)
	}
}

func TestPlainTLSOptionsKeepTheirCertificate(t *testing.T) {
	opts := buildInboundTLS(inboundSpec{TLS: &tlsSpec{ServerName: "a.example.com", Certificate: "CERT", Key: "KEY"}})
	if opts.Reality != nil || len(opts.Certificate) != 1 || opts.Certificate[0] != "CERT" || len(opts.Key) != 1 || opts.Key[0] != "KEY" {
		t.Errorf("plain TLS options changed: %+v", opts)
	}
}

func TestTUICOffersTheALPNEveryClientAsksFor(t *testing.T) {
	spec := &tlsSpec{Certificate: "CERT", Key: "KEY"}
	opts := buildInboundTLS(inboundSpec{Protocol: "tuic", TLS: spec})
	if len(opts.ALPN) != 1 || opts.ALPN[0] != "h3" {
		t.Errorf("tuic ALPN = %v, want [h3]", opts.ALPN)
	}
	if opts := buildInboundTLS(inboundSpec{Protocol: "vless", TLS: spec}); len(opts.ALPN) != 0 {
		t.Errorf("vless ALPN = %v, want none", opts.ALPN)
	}
}

func TestCheckRealityRefusesWhatWouldPanicOrFailEveryClient(t *testing.T) {
	valid := func() *realitySpec {
		s := &realitySpec{ShortID: []string{"", "ab", "0123456789abcdef"}}
		s.Handshake.ServerName = "www.example.com"
		return s
	}
	if err := checkReality(inboundSpec{TLS: &tlsSpec{Reality: valid()}}); err != nil {
		t.Errorf("valid spec refused: %v", err)
	}
	if err := checkReality(inboundSpec{}); err != nil {
		t.Errorf("no TLS at all refused: %v", err)
	}
	for name, mutate := range map[string]func(*realitySpec){
		"too long":  func(s *realitySpec) { s.ShortID = []string{"0123456789abcdef00"} },
		"odd":       func(s *realitySpec) { s.ShortID = []string{"abc"} },
		"not hex":   func(s *realitySpec) { s.ShortID = []string{"zz"} },
		"no target": func(s *realitySpec) { s.Handshake.ServerName = "" },
	} {
		s := valid()
		mutate(s)
		if err := checkReality(inboundSpec{Tag: "r", TLS: &tlsSpec{Reality: s}}); err == nil {
			t.Errorf("%s: not refused", name)
		}
	}
}
