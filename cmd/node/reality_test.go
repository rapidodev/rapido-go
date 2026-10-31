//go:build with_utls

package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	boxtls "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/sagernet/sing-vmess/vless"
)

const realityTestServerName = "reality-target.test"

// realityTarget is the TLS 1.3 site a REALITY inbound borrows its handshake from.
func realityTarget(t *testing.T) uint16 {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: realityTestServerName},
		DNSNames:     []string{realityTestServerName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				io.Copy(io.Discard, conn)
			}()
		}
	}()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

func realitySpecFor(t *testing.T, targetPort uint16) (*realitySpec, string) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spec := &realitySpec{PrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes()), ShortID: []string{"abcd"}}
	// The panel names the target by host; here it has to be a local port.
	spec.Handshake.ServerName = "127.0.0.1"
	spec.Handshake.ServerPort = targetPort
	return spec, base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
}

func echoThroughReality(ctx context.Context, nodePort uint16, publicKey, flow, serverName, echoHost string, echoPort int) error {
	raw, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(nodePort))), 2*time.Second)
	if err != nil {
		return err
	}
	defer raw.Close()
	config, err := boxtls.NewClient(ctx, logger.NOP(), serverName, option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: serverName,
		UTLS:       &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
		Reality:    &option.OutboundRealityOptions{Enabled: true, PublicKey: publicKey, ShortID: "abcd"},
	})
	if err != nil {
		return err
	}
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	tlsConn, err := boxtls.ClientHandshake(ctx, raw, config)
	if err != nil {
		return err
	}
	client, err := vless.NewClient(testUserA, flow, logger.NOP())
	if err != nil {
		return err
	}
	conn, err := client.DialConn(tlsConn, M.Socksaddr{Addr: netip.MustParseAddr(echoHost), Port: uint16(echoPort)})
	if err != nil {
		return err
	}
	const payload = "ping-through-reality"
	if _, err := conn.Write([]byte(payload)); err != nil {
		return err
	}
	buf := make([]byte, len(payload))
	_, err = io.ReadFull(conn, buf)
	return err
}

// A REALITY inbound, built the way the panel describes it, carrying a real client
// with and without Vision. Nothing used to cover this, which is how the node
// binaries shipped unable to start one.
func TestRealityInboundCarriesARealClient(t *testing.T) {
	echoHost, echoPort := startEcho(t)
	for _, flow := range []string{"", "xtls-rprx-vision"} {
		t.Run("flow="+flow, func(t *testing.T) {
			spec, publicKey := realitySpecFor(t, realityTarget(t))
			port := freeTCPPort(t)
			startPlanned(t, startRequest{Inbounds: []inboundSpec{{
				Tag: "r", Protocol: "vless", ListenPort: port,
				Users: []userSpec{{Name: "alice", UUID: testUserA, Flow: flow}},
				TLS:   &tlsSpec{ServerName: realityTestServerName, Reality: spec},
			}}})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := echoThroughReality(ctx, port, publicKey, flow, realityTestServerName, echoHost, echoPort); err != nil {
				t.Fatalf("through REALITY: %v", err)
			}
		})
	}
}
