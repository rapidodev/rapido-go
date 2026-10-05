package shadowtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/netip"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	boxCertificate "github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxService "github.com/sagernet/sing-box/adapter/service"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	dnstransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
	shadowtls "github.com/sagernet/sing-shadowtls"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

// This file proves the one thing worth proving locally about this fork:
// that the outer ShadowTLS handshake and the embedded inner shadowsocks-
// 2022 layer are genuinely wired together end to end (a real client,
// through both layers, really reaches the destination it asked for, and a
// hot user-list swap on the running listener takes effect with no
// restart) - not that third_party/sing-shadowtls's own v3 handshake logic
// is correct (service_patch_test.go already covers the one thing this
// fork patched there) and not that NewInbound's own production
// WildcardSNI=all choice resolves a real client SNI on port 443 (that is
// unmodified upstream library behavior, and by design needs a real public
// HTTPS site to test against - not something a portable, unprivileged
// unit test can do). So every test here builds its own real sing-box Box
// (registries included) but swaps in WildcardSNIOff pointed at a
// throwaway local TLS server via newInbound's own test seam, instead of
// going through NewInbound's hardcoded production path.

// testDisguiseServer starts a real TLS server on an ephemeral port that
// performs a genuine handshake and then discards whatever it receives -
// standing in for the "real public HTTPS site" a WildcardSNI=all
// deployment would relay the handshake against. ShadowTLS v3 only ever
// uses this connection during the handshake itself (see
// third_party/sing-shadowtls's own NewConnection, v3 case): once the
// relay's HMAC check passes, the disguise connection is closed and real
// traffic flows directly between the client and this fork's inner layer.
func testDisguiseServer(t *testing.T) M.Socksaddr {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate disguise key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "disguise.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"disguise.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create disguise cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal disguise key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("listen disguise server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c)
			}(conn)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return M.Socksaddr{Addr: netip.MustParseAddr("127.0.0.1"), Port: uint16(addr.Port)}
}

// startEchoServer starts a plain TCP echo server standing in for the real
// destination a client asks the inner shadowsocks-2022 layer to reach.
func startEchoServer(t *testing.T) M.Socksaddr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return M.Socksaddr{Addr: netip.MustParseAddr("127.0.0.1"), Port: uint16(addr.Port)}
}

// freePort hands back a currently-unused TCP port on 127.0.0.1.
func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

// testBox builds a real, minimal sing-box instance with exactly one
// inbound - this fork's own combined ShadowTLS+shadowsocks-2022 Inbound,
// constructed via newInbound's own test seam (WildcardSNIOff against
// disguise, not NewInbound's production WildcardSNI=all) - and one
// outbound ("direct"), routing every connection there. This gives the
// test a REAL sing-box router (box.New's own), not a hand-rolled stand-in,
// so a round trip through it genuinely proves the fork's routing/traffic
// wiring.
func testBox(t *testing.T, port uint16, users []option.ShadowTLSUser, innerMethod, innerPassword string, disguise M.Socksaddr) *box.Box {
	t.Helper()
	inboundRegistry := inbound.NewRegistry()
	inbound.Register[InboundOptions](inboundRegistry, C.TypeShadowTLS, func(ctx context.Context, router adapter.Router, lg log.ContextLogger, tag string, options InboundOptions) (adapter.Inbound, error) {
		return newInbound(ctx, router, lg, tag, options, shadowtls.WildcardSNIOff, shadowtls.HandshakeConfig{
			Server: disguise, Dialer: N.SystemDialer,
		})
	})
	outboundRegistry := outbound.NewRegistry()
	direct.RegisterOutbound(outboundRegistry)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	dnstransport.RegisterUDP(dnsRegistry)
	dnstransport.RegisterTCP(dnsRegistry)
	dnstransport.RegisterTLS(dnsRegistry)
	dnstransport.RegisterHTTPS(dnsRegistry)

	listen := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	opts := option.Options{
		Inbounds: []option.Inbound{{
			Type: "shadowtls", Tag: "shadowtls-in",
			Options: &InboundOptions{
				ListenOptions: option.ListenOptions{Listen: &listen, ListenPort: port},
				Users:         users,
				InnerMethod:   innerMethod,
				InnerPassword: innerPassword,
			},
		}},
		Outbounds: []option.Outbound{{Type: "direct", Tag: "direct-out", Options: &option.DirectOutboundOptions{}}},
		Route:     &option.RouteOptions{Final: "direct-out"},
	}
	ctx := traffic.NewContext(context.Background(), traffic.NewManager())
	ctx = box.Context(ctx, inboundRegistry, outboundRegistry, endpoint.NewRegistry(), dnsRegistry, boxService.NewRegistry(), boxCertificate.NewRegistry())
	b, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatalf("box.New: %v", err)
	}
	if err := b.Start(); err != nil {
		t.Fatalf("box.Start: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// dialAndEcho opens a real ShadowTLS v3 connection (outerPassword) to the
// running inbound, wraps it with a real shadowsocks-2022 client
// (innerMethod/innerPassword) targeting dest, writes payload and expects
// it echoed back exactly - a genuine round trip through both layers.
func dialAndEcho(t *testing.T, port uint16, outerPassword, innerMethod, innerPassword string, dest M.Socksaddr, payload string) error {
	t.Helper()
	client, err := shadowtls.NewClient(shadowtls.ClientConfig{
		Version:  3,
		Password: outerPassword,
		Server:   M.Socksaddr{Addr: netip.MustParseAddr("127.0.0.1"), Port: port},
		Dialer:   N.SystemDialer,
		TLSHandshake: shadowtls.DefaultTLSHandshakeFunc(outerPassword, &tls.Config{
			ServerName: "disguise.test", InsecureSkipVerify: true,
		}),
		Logger: logger.NOP(),
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outerConn, err := client.DialContext(ctx)
	if err != nil {
		return err
	}
	defer outerConn.Close()

	method, err := shadowaead_2022.NewWithPassword(innerMethod, innerPassword, time.Now)
	if err != nil {
		return err
	}
	ssConn, err := method.DialConn(outerConn, dest)
	if err != nil {
		return err
	}
	outerConn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := ssConn.Write([]byte(payload)); err != nil {
		return err
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(ssConn, buf); err != nil {
		return err
	}
	if string(buf) != payload {
		return &echoMismatchError{want: payload, got: string(buf)}
	}
	return nil
}

type echoMismatchError struct{ want, got string }

func (e *echoMismatchError) Error() string {
	return "echo mismatch: want " + e.want + ", got " + e.got
}

func randomBase64Key(t *testing.T, n int) string {
	t.Helper()
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestCombinedRoundTrip(t *testing.T) {
	disguise := testDisguiseServer(t)
	dest := startEchoServer(t)
	port := freePort(t)
	innerKey := randomBase64Key(t, 16)

	testBox(t, port, []option.ShadowTLSUser{{Name: "alice", Password: "outer-pw-v1"}},
		"2022-blake3-aes-128-gcm", innerKey, disguise)

	if err := dialAndEcho(t, port, "outer-pw-v1", "2022-blake3-aes-128-gcm", innerKey, dest, "ping-through-both-layers"); err != nil {
		t.Fatalf("round trip through outer ShadowTLS + inner shadowsocks-2022 failed: %v", err)
	}
}

// TestCombinedHotUpdate is the real reason third_party/sing-shadowtls was
// patched at all: proves a password change on the outer layer takes
// effect on the already-running listener - the old password stops
// working and the new one works immediately, with no restart.
func TestCombinedHotUpdate(t *testing.T) {
	disguise := testDisguiseServer(t)
	dest := startEchoServer(t)
	port := freePort(t)
	innerKey := randomBase64Key(t, 16)

	b := testBox(t, port, []option.ShadowTLSUser{{Name: "alice", Password: "outer-pw-v1"}},
		"2022-blake3-aes-128-gcm", innerKey, disguise)

	if err := dialAndEcho(t, port, "outer-pw-v1", "2022-blake3-aes-128-gcm", innerKey, dest, "before-swap"); err != nil {
		t.Fatalf("round trip before swap failed: %v", err)
	}

	raw, loaded := b.Inbound().Get("shadowtls-in")
	if !loaded {
		t.Fatal("inbound not found")
	}
	in, ok := raw.(*Inbound)
	if !ok {
		t.Fatalf("inbound is not *Inbound: %T", raw)
	}
	in.UpdateUsers([]option.ShadowTLSUser{{Name: "alice", Password: "outer-pw-v2"}})

	if err := dialAndEcho(t, port, "outer-pw-v2", "2022-blake3-aes-128-gcm", innerKey, dest, "after-swap"); err != nil {
		t.Fatalf("round trip with the NEW password after a hot swap failed: %v", err)
	}
	if err := dialAndEcho(t, port, "outer-pw-v1", "2022-blake3-aes-128-gcm", innerKey, dest, "should-not-arrive"); err == nil {
		t.Fatal("expected the OLD password to be rejected after a hot swap, but the round trip succeeded")
	}
}
