package main

import (
	"crypto/tls"
	"io"
	"net"
	"testing"

	"github.com/sagernet/sing/common/logger"

	"github.com/sagernet/sing-vmess/vless"
)

// Vision looks into the first packets of a connection for a TLS ServerHello and reads its cipher suite at
// an offset given by the peer's session ID length byte. A packet shaped like a ServerHello whose length
// byte points past its end used to panic in that filter and end the whole node, dropping every user.
func TestVisionSurvivesAServerHelloWhoseSessionIDRunsPastThePacket(t *testing.T) {
	for _, size := range []int{79, 128, 256, 298} {
		local, remote := net.Pipe()
		drained := make(chan struct{})
		go func() {
			io.Copy(io.Discard, remote)
			close(drained)
		}()
		conn, err := vless.NewVisionConn(local, tls.Client(local, &tls.Config{}), [16]byte{}, logger.NOP())
		if err != nil {
			t.Fatalf("NewVisionConn: %v", err)
		}
		packet := make([]byte, size)
		// Handshake record, TLS 1.2, record length 288, handshake type ServerHello.
		copy(packet, []byte{22, 3, 3, 0x01, 0x20, 2})
		packet[43] = 255 // session ID length; a real one is at most 32
		if _, err := conn.Write(packet); err != nil {
			t.Errorf("%d-byte packet: Write: %v", size, err)
		}
		local.Close()
		<-drained
	}
}
