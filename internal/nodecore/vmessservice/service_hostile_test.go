package vmess

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"net"
	"net/netip"
	"testing"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type discardHandler struct{ called chan struct{} }

func (h discardHandler) NewConnectionEx(_ context.Context, conn net.Conn, _ M.Socksaddr, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	close(h.called)
	conn.Close()
}

func (h discardHandler) NewPacketConnectionEx(_ context.Context, conn N.PacketConn, _ M.Socksaddr, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	conn.Close()
}

// sendRequest has a real client send its request header with this security value.
// A client can't build a writer for a value that has none, which it finds out
// just after the header is out, so that panic is expected and stays over here.
func sendRequest(t *testing.T, id string, security byte, conn net.Conn) {
	t.Helper()
	client, err := NewClient(id, "none", 0)
	if err != nil {
		t.Fatal(err)
	}
	client.security = security
	go func() {
		defer func() { _ = recover() }()
		raw := client.dialRaw(conn, CommandTCP, M.Socksaddr{Addr: netip.MustParseAddr("1.1.1.1"), Port: 443})
		_ = raw.writeHandshake(nil)
	}()
}

// Any user could end the whole node by naming a security value CreateReader has
// no reader for. Reaching the error here is the assertion.
func TestServiceRefusesASecurityValueItHasNoReaderFor(t *testing.T) {
	const id = "8f8a4c1e-1e2a-4b8a-9b1a-0000000000a1"
	for _, security := range []byte{0, SecurityTypeAuto, SecurityTypeZero, 7, 15} {
		service := NewService[string](discardHandler{called: make(chan struct{})})
		if err := service.UpdateUsers([]string{"alice"}, []string{id}, []int{0}); err != nil {
			t.Fatal(err)
		}
		if err := service.Start(); err != nil {
			t.Fatal(err)
		}
		server, client := net.Pipe()
		sendRequest(t, id, security, client)
		err := service.NewConnection(context.Background(), server, M.Socksaddr{}, nil)
		if !errors.Is(err, ErrUnsupportedSecurityType) {
			t.Errorf("security %d: got %v, want %v", security, err, ErrUnsupportedSecurityType)
		}
		server.Close()
		client.Close()
	}
}

func TestServiceStillAcceptsEverySecurityAClientSends(t *testing.T) {
	const id = "8f8a4c1e-1e2a-4b8a-9b1a-0000000000a1"
	for _, security := range []byte{SecurityTypeLegacy, SecurityTypeAes128Gcm, SecurityTypeChacha20Poly1305, SecurityTypeNone} {
		handler := discardHandler{called: make(chan struct{})}
		service := NewService[string](handler)
		if err := service.UpdateUsers([]string{"alice"}, []string{id}, []int{0}); err != nil {
			t.Fatal(err)
		}
		if err := service.Start(); err != nil {
			t.Fatal(err)
		}
		server, client := net.Pipe()
		sendRequest(t, id, security, client)
		if err := service.NewConnection(context.Background(), server, M.Socksaddr{}, nil); err != nil {
			t.Errorf("security %d: %v", security, err)
		}
		select {
		case <-handler.called:
		default:
			t.Errorf("security %d: never reached the handler", security)
		}
		server.Close()
		client.Close()
	}
}

// A chunk shorter than the checksum it has to start with used to slice past its
// own end, on a goroutine nothing can recover.
func TestChecksumReaderRefusesAChunkShorterThanItsChecksum(t *testing.T) {
	for _, chunk := range [][]byte{{1}, {1, 2, 3}} {
		reader := NewStreamChecksumReader(bytes.NewReader(chunk))
		if _, err := reader.Read(make([]byte, 64)); !errors.Is(err, ErrInvalidChecksum) {
			t.Errorf("Read of %d bytes: %v", len(chunk), err)
		}
		reader = NewStreamChecksumReader(bytes.NewReader(chunk))
		buffer := buf.NewSize(64)
		if err := reader.ReadBuffer(buffer); !errors.Is(err, ErrInvalidChecksum) {
			t.Errorf("ReadBuffer of %d bytes: %v", len(chunk), err)
		}
		buffer.Release()
	}

	body := []byte("real data")
	hash := fnv.New32a()
	hash.Write(body)
	chunk := append(binary.BigEndian.AppendUint32(nil, hash.Sum32()), body...)
	out := make([]byte, 64)
	n, err := NewStreamChecksumReader(bytes.NewReader(chunk)).Read(out)
	if err != nil || string(out[:n]) != string(body) {
		t.Errorf("a valid chunk: %q, %v", out[:n], err)
	}
}
