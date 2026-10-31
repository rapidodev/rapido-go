package tunnelprovision

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// fakeServer is a real SSH endpoint with no shell behind it. It records every
// command and answers the one question Provision asks, whether a path exists.
type fakeServer struct {
	mu       sync.Mutex
	commands []string
	existing map[string]bool
}

func (s *fakeServer) ran() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *fakeServer) answer(command string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, command)
	for path := range s.existing {
		if command == "test -e "+path+" && echo present || echo missing" {
			return "present\n"
		}
	}
	if strings.HasPrefix(command, "test -e ") {
		return "missing\n"
	}
	return ""
}

func (s *fakeServer) serve(t *testing.T, conn net.Conn, config *ssh.ServerConfig) {
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for req := range channelRequests {
				if req.Type != "exec" {
					if req.WantReply {
						req.Reply(false, nil)
					}
					continue
				}
				var payload struct{ Command string }
				ssh.Unmarshal(req.Payload, &payload)
				if req.WantReply {
					req.Reply(true, nil)
				}
				channel.Write([]byte(s.answer(payload.Command)))
				channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}()
	}
}

func startFakeServer(t *testing.T, existing ...string) (*fakeServer, *sshexec.Client) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{NoClientAuth: false, PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	server := &fakeServer{existing: map[string]bool{}}
	for _, path := range existing {
		server.existing[path] = true
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(t, conn, config)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	t.Setenv("SSH_KNOWN_HOSTS_FILE", filepath.Join(t.TempDir(), "known_hosts"))
	client, err := sshexec.Dial(context.Background(), sshexec.Config{Host: host, Port: int32(portNumber), User: "root", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client
}

func collisionParams(method string) Params {
	return Params{
		Method: method, InterfaceName: "tn1", NodeAddress: "203.0.113.1", RelayHost: "203.0.113.2",
		TunnelSubnet: netip.MustParsePrefix("10.255.0.0/30"), FRPControlPort: 27000, FRPToken: "token", Ports: []int32{443},
	}
}

func mentions(commands []string, text string) bool {
	for _, command := range commands {
		if strings.Contains(command, text) {
			return true
		}
	}
	return false
}

// A node's second tunnel used to collide with its first one's frpc config. The
// provisioning refused, correctly, and the cleanup after that refusal then removed
// the first tunnel's config and stopped its service.
func TestAPathInUseStopsProvisioningBeforeAnythingIsTouched(t *testing.T) {
	for _, method := range []string{MethodFRPDirect, MethodFRPQUIC, MethodGREFRP} {
		t.Run(method, func(t *testing.T) {
			relayServer, relay := startFakeServer(t)
			nodeServer, node := startFakeServer(t, "/root/frp/client/client-27000.toml")

			err := Provision(context.Background(), relay, node, collisionParams(method))
			if !errors.Is(err, ErrPathInUse) {
				t.Fatalf("Provision: %v, want ErrPathInUse", err)
			}
			for name, commands := range map[string][]string{"relay": relayServer.ran(), "node": nodeServer.ran()} {
				for _, command := range commands {
					if !strings.HasPrefix(command, "test -e ") {
						t.Errorf("%s ran %q before the collision was known", name, command)
					}
				}
			}
		})
	}
}

func TestEveryPathProvisionChecksIsOneItWouldClaim(t *testing.T) {
	for method, want := range map[string][2][]string{
		MethodGREFRP:    {{"/etc/systemd/system/gre-tn1.service", "/root/frp/server/server-27000.toml"}, {"/etc/systemd/system/gre-tn1.service", "/root/frp/client/client-27000.toml"}},
		MethodFRPDirect: {{"/root/frp/server/server-27000.toml"}, {"/root/frp/client/client-27000.toml"}},
		MethodFRPQUIC:   {{"/root/frp/server/server-27000.toml"}, {"/root/frp/client/client-27000.toml"}},
		MethodGREDNAT:   {{"/etc/systemd/system/gre-tn1.service", "/etc/systemd/system/rapido-dnat-tn1.service"}, {"/etc/systemd/system/gre-tn1.service"}},
		MethodWGDNAT:    {{"/etc/wireguard/tn1.conf", "/etc/systemd/system/rapido-dnat-tn1.service"}, {"/etc/wireguard/tn1.conf"}},
	} {
		relay, node := provisionPaths(collisionParams(method))
		if fmt.Sprint(relay) != fmt.Sprint(want[0]) || fmt.Sprint(node) != fmt.Sprint(want[1]) {
			t.Errorf("%s: relay=%v node=%v, want %v", method, relay, node, want)
		}
	}
}

func TestTeardownLeavesAlonePathsAnotherTunnelHolds(t *testing.T) {
	const frpc, frps = "client-27000", "server-27000"
	for _, method := range []string{MethodFRPDirect, MethodFRPQUIC, MethodGREFRP} {
		t.Run(method, func(t *testing.T) {
			run := func(p Params) (relayCommands, nodeCommands []string) {
				relayServer, relay := startFakeServer(t)
				nodeServer, node := startFakeServer(t)
				if err := Teardown(context.Background(), relay, node, p); err != nil {
					t.Fatalf("Teardown: %v", err)
				}
				return relayServer.ran(), nodeServer.ran()
			}

			p := collisionParams(method)
			relayCommands, nodeCommands := run(p)
			if !mentions(relayCommands, frps) || !mentions(nodeCommands, frpc) {
				t.Errorf("a tunnel's own teardown didn't remove its config: relay %v node %v", relayCommands, nodeCommands)
			}

			p.KeepNodeFRP = true
			relayCommands, nodeCommands = run(p)
			if mentions(nodeCommands, frpc) {
				t.Errorf("KeepNodeFRP still touched the node's frpc: %v", nodeCommands)
			}
			if !mentions(relayCommands, frps) {
				t.Errorf("KeepNodeFRP left the relay's frps behind: %v", relayCommands)
			}

			p.KeepNodeFRP, p.KeepRelayFRP = false, true
			relayCommands, nodeCommands = run(p)
			if mentions(relayCommands, frps) {
				t.Errorf("KeepRelayFRP still touched the relay's frps: %v", relayCommands)
			}
			if !mentions(nodeCommands, frpc) {
				t.Errorf("KeepRelayFRP left the node's frpc behind: %v", nodeCommands)
			}
		})
	}
}

func TestUsesFRPIsWhatTeardownTreatsAsFRP(t *testing.T) {
	for method, want := range map[string]bool{
		"": true, MethodGREFRP: true, MethodFRPDirect: true, MethodFRPQUIC: true,
		MethodGREDNAT: false, MethodIP6GREDNAT: false, MethodWGDNAT: false,
	} {
		if got := UsesFRP(method); got != want {
			t.Errorf("UsesFRP(%q) = %v, want %v", method, got, want)
		}
	}
}
