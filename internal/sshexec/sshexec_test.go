package sshexec

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testHostKey generates a throwaway RSA host key for one test server - a
// real signer the handshake needs, with nothing persisted or reused
// between tests (the client ignores host keys entirely, see sshexec.go's
// own doc comment, so this key's only job is to let the handshake
// complete).
func testHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	return signer
}

func TestShellQuoteEscapesASingleQuote(t *testing.T) {
	got := shellQuote(`it's/a/path`)
	want := `'it'\''s/a/path'`
	if got != want {
		t.Errorf("shellQuote = %q, want %q", got, want)
	}
}

func TestBuildWriteFileScriptAddsATrailingNewlineOnlyWhenMissing(t *testing.T) {
	withNL := buildWriteFileScript("/etc/x.conf", []byte("line1\n"), "600")
	withoutNL := buildWriteFileScript("/etc/x.conf", []byte("line1"), "600")
	if strings.Count(withNL, "line1\n") != 1 {
		t.Errorf("content with its own trailing newline got a second one: %q", withNL)
	}
	if !strings.Contains(withoutNL, "line1\n") {
		t.Errorf("content with no trailing newline did not get one added: %q", withoutNL)
	}
}

func TestBuildWriteFileScriptNeverLetsContentBreakOutOfTheHeredoc(t *testing.T) {
	// Content containing the literal heredoc marker text must not be able
	// to terminate the heredoc early - a quoted ('MARKER') heredoc takes
	// the body completely literally (no $-expansion, no escape processing)
	// and bash only ends it on a line that is exactly the marker, which
	// "SSHEXEC_EOF_8f2a1c and more" is not.
	tricky := []byte("some $(rm -rf /) `backticks` and a lone\nSSHEXEC_EOF_8f2a1c and more\ntext")
	script := buildWriteFileScript("/etc/x.conf", tricky, "600")
	if !strings.Contains(script, string(tricky)) {
		t.Errorf("content was not embedded byte-for-byte: %q", script)
	}
	// Exactly one line that is JUST the marker (the real terminator) - not
	// the "...and more" line, which must stay inert heredoc body.
	count := 0
	for _, line := range strings.Split(script, "\n") {
		if line == "SSHEXEC_EOF_8f2a1c" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 bare marker line (the real terminator), got %d in: %q", count, script)
	}
}

func TestBuildWriteFileScriptQuotesThePathAndChmodsIt(t *testing.T) {
	script := buildWriteFileScript("/etc/wireguard/node2.conf", []byte("x"), "600")
	if !strings.HasPrefix(script, "cat > '/etc/wireguard/node2.conf' <<'SSHEXEC_EOF_8f2a1c'\n") {
		t.Errorf("script does not start with the expected cat/heredoc line: %q", script)
	}
	if !strings.Contains(script, "chmod 600 '/etc/wireguard/node2.conf'\n") {
		t.Errorf("script does not chmod the right path: %q", script)
	}
}

// --- a real, minimal in-process SSH server, for the end-to-end path ---

// testSSHServer accepts exactly one connection and runs whatever "exec"
// command it's given through a real shell, so Dial/Run/WriteFile are
// exercised against the real golang.org/x/crypto/ssh wire protocol, not a
// stand-in for it - only the shell on the other end is substituted (sh, a
// real one, just not a remote box).
func testSSHServer(t *testing.T) (addr string, user, pass string) {
	t.Helper()
	user, pass = "root", "test-password"

	signer := testHostKey(t)
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(p) == pass {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		serveOneSSHConn(t, nc, cfg)
	}()
	t.Cleanup(wg.Wait)

	return ln.Addr().String(), user, pass
}

func serveOneSSHConn(t *testing.T, nc net.Conn, cfg *ssh.ServerConfig) {
	t.Helper()
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, requests, err := newCh.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
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
				cmd := exec.Command("sh", "-c", payload.Command)
				cmd.Stdout = ch
				cmd.Stderr = ch
				runErr := cmd.Run()
				code := 0
				if runErr != nil {
					code = 1
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)}))
				return
			}
		}()
	}
}

func TestDialRunAndWriteFileOverARealSSHConnection(t *testing.T) {
	addr, user, pass := testSSHServer(t)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, Config{Host: host, Port: int32(port), User: user, Password: pass})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	out, err := client.Run(ctx, "echo hello-from-the-other-side")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out) != "hello-from-the-other-side" {
		t.Errorf("Run output = %q, want hello-from-the-other-side", out)
	}

	dir := t.TempDir()
	path := dir + "/written.conf"
	content := []byte("line one\nwith a ' quote\n")
	if err := client.WriteFile(ctx, path, content, "600"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	readBack, err := client.Run(ctx, "cat "+shellQuote(path))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if readBack != string(content) {
		t.Errorf("read back = %q, want %q", readBack, string(content))
	}
}

func TestDialFailsWithTheWrongPassword(t *testing.T) {
	addr, user, _ := testSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, Config{Host: host, Port: int32(port), User: user, Password: "wrong"}); err == nil {
		t.Error("Dial with the wrong password succeeded, want an error")
	}
}
