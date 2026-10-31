package shadowtls

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	box "github.com/legendary1205/rapido-go/internal/nodecore/box"
	boxCertificate "github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	shadowtls "github.com/sagernet/sing-shadowtls"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
)

// recordingDialer remembers where the inbound tried to go, and never gets there.
type recordingDialer struct {
	mu   sync.Mutex
	seen []M.Socksaddr
}

func (d *recordingDialer) DialContext(_ context.Context, _ string, destination M.Socksaddr) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, destination)
	return nil, net.ErrClosed
}

func (d *recordingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

func (d *recordingDialer) destinations() []M.Socksaddr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]M.Socksaddr(nil), d.seen...)
}

// strangerConnects has the service handle a TLS client with no password at all,
// asking for this SNI. That client must not get to choose where the node dials.
func strangerConnects(t *testing.T, service *shadowtls.Service, sni string) {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		tlsClient := tls.Client(client, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
		_ = tlsClient.Handshake()
	}()
	server.SetDeadline(time.Now().Add(5 * time.Second))
	err := service.NewConnection(context.Background(), server, M.Socksaddr{}, M.Socksaddr{}, nil)
	if err == nil {
		t.Fatal("a stranger's connection was served")
	}
	server.Close()
}

func testService(t *testing.T, wildcard shadowtls.WildcardSNI, handshake shadowtls.HandshakeConfig) *shadowtls.Service {
	t.Helper()
	service, err := shadowtls.NewService(shadowtls.ServiceConfig{
		Version:     3,
		Users:       []shadowtls.User{{Name: "alice", Password: "pw"}},
		Handshake:   handshake,
		WildcardSNI: wildcard,
		Handler:     nopHandler{},
		Logger:      logger.NOP(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type nopHandler struct{}

func (nopHandler) NewConnectionEx(context.Context, net.Conn, M.Socksaddr, M.Socksaddr, N.CloseHandlerFunc) {
}

// With the old WildcardSNIAll this dialled the SNI itself, which let anyone on the
// internet use a ShadowTLS port as a free relay to port 443 of any host.
func TestAStrangerCannotChooseWhereTheNodeDials(t *testing.T) {
	t.Run("with a handshake server, it is the only place they reach", func(t *testing.T) {
		dialer := &recordingDialer{}
		fixed := M.Socksaddr{Fqdn: "www.example.com", Port: 443}
		strangerConnects(t, testService(t, shadowtls.WildcardSNIAuthed, shadowtls.HandshakeConfig{Server: fixed, Dialer: publicOnlyDialer{dialer}}), "attacker-target.example")
		seen := dialer.destinations()
		if len(seen) != 1 || seen[0] != fixed {
			t.Errorf("dialled %v, want only %v", seen, fixed)
		}
	})
	t.Run("with none, they reach nothing", func(t *testing.T) {
		dialer := &recordingDialer{}
		strangerConnects(t, testService(t, shadowtls.WildcardSNIAuthed, shadowtls.HandshakeConfig{Dialer: publicOnlyDialer{dialer}}), "attacker-target.example")
		if seen := dialer.destinations(); len(seen) != 0 {
			t.Errorf("dialled %v", seen)
		}
	})
}

// NewInbound is what production builds, so this is what has to hold for it.
func TestProductionInboundIsNotAnOpenRelay(t *testing.T) {
	port := freePort(t)
	b := testBoxProduction(t, port, "")
	defer b.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- struct{}{}
			conn.Close()
		}
	}()

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoaPort(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	tlsClient := tls.Client(conn, &tls.Config{ServerName: "localhost", InsecureSkipVerify: true})
	if err := tlsClient.Handshake(); err == nil {
		t.Error("a stranger completed a TLS handshake with no handshake server configured")
	}
	select {
	case <-accepted:
		t.Error("the inbound relayed a stranger")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestPublicOnlyDialerRefusesWhatResolvesToTheNodesOwnNetwork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	dialer := publicOnlyDialer{N.SystemDialer}
	address := M.SocksaddrFromNet(listener.Addr())
	if conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, address); err == nil {
		conn.Close()
		t.Error("connected to loopback")
	}
	if conn, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{Fqdn: "localhost", Port: address.Port}); err == nil {
		conn.Close()
		t.Error("connected to a name resolving to loopback")
	}
	if _, err := dialer.DialContext(context.Background(), N.NetworkTCP, M.Socksaddr{}); err == nil {
		t.Error("dialled no address at all")
	}
}

func itoaPort(port uint16) string { return strconv.Itoa(int(port)) }

// testBoxProduction is testBox through NewInbound itself, the path production takes.
func testBoxProduction(t *testing.T, port uint16, handshakeServer string) *box.Box {
	t.Helper()
	inboundRegistry := inbound.NewRegistry()
	RegisterInbound(inboundRegistry)
	outboundRegistry := outbound.NewRegistry()
	direct.RegisterOutbound(outboundRegistry)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	listen := badoption.Addr(netip.MustParseAddr("127.0.0.1"))
	opts := option.Options{
		Inbounds: []option.Inbound{{
			Type: "shadowtls", Tag: "shadowtls-in",
			Options: &InboundOptions{
				ListenOptions:   option.ListenOptions{Listen: &listen, ListenPort: port},
				Users:           []option.ShadowTLSUser{{Name: "alice", Password: "pw"}},
				InnerMethod:     "2022-blake3-aes-128-gcm",
				InnerPassword:   randomBase64Key(t, 16),
				HandshakeServer: handshakeServer,
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
	return b
}
