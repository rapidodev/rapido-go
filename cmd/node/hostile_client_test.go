package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	shadowsocks "github.com/sagernet/sing-shadowsocks"
	M "github.com/sagernet/sing/common/metadata"
)

// A panic in any of these would end this test process, as it ended the node:
// getting to the end of a test is the assertion, and each one then proves the
// listener still serves a real client.

// freeListenPort is a port free on both TCP and UDP, which a Shadowsocks inbound
// listens on: Windows reserves whole ranges of UDP ports.
func freeListenPort(t *testing.T) uint16 {
	t.Helper()
	for range 50 {
		port := freeTCPPort(t)
		if conn, err := net.ListenPacket("udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(int(port)))); err == nil {
			conn.Close()
			return port
		}
	}
	t.Fatal("no port free on both TCP and UDP")
	return 0
}

func dialNode(t *testing.T, port uint16) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), 2*time.Second)
	if err != nil {
		t.Fatalf("dial node: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// readUntilClosed waits for the node to drop a connection it refused.
func readUntilClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	for {
		if _, err := conn.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("the node kept a connection it should have refused")
			}
			return
		}
	}
}

// Sent before any UUID is checked: 2^64-1 panicked make, and 2^47 was the
// unrecoverable out-of-memory failure.
func TestVLESSAddonLengthFromAStrangerCannotEndTheNode(t *testing.T) {
	echoHost, echoPort := startEcho(t)
	port := freeTCPPort(t)
	startPlanned(t, startRequest{Inbounds: []inboundSpec{{Tag: "v", Protocol: "vless", ListenPort: port,
		Users: []userSpec{{Name: "alice", UUID: testUserA}}}}})

	for name, length := range map[string][]byte{
		"max":      binary.AppendUvarint(nil, 1<<64-1),
		"huge":     binary.AppendUvarint(nil, 1<<47),
		"overruns": binary.AppendUvarint(nil, 200),
	} {
		for _, field := range []byte{0x0a, 0x12} { // flow, then seed
			t.Run(name, func(t *testing.T) {
				addons := append([]byte{field}, length...)
				payload := append([]byte{0}, make([]byte, 16)...) // version, unknown UUID
				payload = append(payload, byte(len(addons)))
				payload = append(payload, addons...)
				conn := dialNode(t, port)
				if _, err := conn.Write(payload); err != nil {
					t.Fatal(err)
				}
				readUntilClosed(t, conn)
			})
		}
	}

	if err := echoVia(port, testUserA, echoHost, echoPort); err != nil {
		t.Fatalf("a real client after the hostile ones: %v", err)
	}
}

// ssChunkWriter speaks just enough of AEAD Shadowsocks to send lengths no real
// client would, encrypted with the user's real key.
type ssChunkWriter struct {
	aead  cipher.AEAD
	nonce []byte
}

func newSSChunkWriter(t *testing.T, password string) (*ssChunkWriter, []byte) {
	t.Helper()
	key := shadowsocks.Key([]byte(password), 32)
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	subkey, err := hkdf.Key(sha1.New, key, salt, "ss-subkey", 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(subkey)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return &ssChunkWriter{aead: aead, nonce: make([]byte, aead.NonceSize())}, salt
}

func (w *ssChunkWriter) seal(plain []byte) []byte {
	out := w.aead.Seal(nil, w.nonce, plain, nil)
	for i := range w.nonce {
		w.nonce[i]++
		if w.nonce[i] != 0 {
			break
		}
	}
	return out
}

func (w *ssChunkWriter) length(n int) []byte {
	return w.seal(binary.BigEndian.AppendUint16(nil, uint16(n)))
}

// Both places the length is read: the first chunk on the listener's goroutine,
// and a later one on the connection's copy goroutine, where no recover reaches.
func TestShadowsocksOversizedChunkFromAUserCannotEndTheNode(t *testing.T) {
	const password = "pw-hostile"
	echoHost, echoPort := startEcho(t)
	port := freeListenPort(t)
	user := userSpec{Name: "alice", Password: password, Method: "aes-256-gcm"}
	startPlanned(t, startRequest{Inbounds: []inboundSpec{{Tag: "s", Protocol: "shadowsocks", ListenPort: port, Users: []userSpec{user}}}})

	t.Run("first chunk", func(t *testing.T) {
		w, salt := newSSChunkWriter(t, password)
		conn := dialNode(t, port)
		if _, err := conn.Write(append(salt, w.length(0xFFFF)...)); err != nil {
			t.Fatal(err)
		}
		readUntilClosed(t, conn)
	})

	t.Run("later chunk", func(t *testing.T) {
		w, salt := newSSChunkWriter(t, password)
		dest := M.Socksaddr{Addr: netip.MustParseAddr(echoHost), Port: uint16(echoPort)}
		address := make([]byte, 0, 32)
		address = append(address, 0x01) // SOCKS IPv4
		ip := dest.Addr.As4()
		address = append(address, ip[:]...)
		address = binary.BigEndian.AppendUint16(address, dest.Port)
		first := append(salt, w.length(len(address))...)
		first = append(first, w.seal(address)...)
		first = append(first, w.length(0xFFFF)...)

		conn := dialNode(t, port)
		if _, err := conn.Write(first); err != nil {
			t.Fatal(err)
		}
		readUntilClosed(t, conn)
	})

	f := protoFixture{"shadowsocks", "shadowsocks", "aes-256-gcm"}
	if err := f.echoProxied(port, user, echoHost, echoPort); err != nil {
		t.Fatalf("a real client after the hostile ones: %v", err)
	}
}
