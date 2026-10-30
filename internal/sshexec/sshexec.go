// Package sshexec is a thin, provisioning-shaped wrapper around
// golang.org/x/crypto/ssh: dial a box with a password, run a shell command
// on it, or write a file to it (via a heredoc over Run, not SFTP - avoids
// a second dependency for what this package only ever uses for a handful
// of small config files). Built for internal/tunnelprovision, which uses
// it against two kinds of box a running panel has never reached before:
// the relay (always external, never part of this fleet) and, for now, the
// node itself (see migration 00023's own doc comment on why the node side
// is SSH'd into too, rather than going through the node agent's API).
//
// Host keys use trust on first use: the first key is recorded in the shared
// known_hosts file, and later connections fail if the key changes. This
// prevents credentials from being sent to an untrusted replacement host
// after the initial connection.
package sshexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh/knownhosts"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Config struct {
	Host     string
	Port     int32
	User     string
	Password string
}

type Client struct {
	conn *ssh.Client
}

const dialTimeout = 15 * time.Second

func Dial(ctx context.Context, cfg Config) (*Client, error) {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            []ssh.AuthMethod{ssh.Password(cfg.Password)},
		HostKeyCallback: verifyHostKey,
		Timeout:         dialTimeout,
		// The default list leads with the post-quantum hybrid, whose key
		// exchange packet is far larger than the classic ones. On several
		// of the filtered paths this panel reaches servers over, the large
		// packet is dropped and the login hangs until it times out, while
		// the classic exchange completes in about a second.
		Config: ssh.Config{KeyExchanges: []string{
			"curve25519-sha256", "curve25519-sha256@libssh.org",
			"ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
			"diffie-hellman-group14-sha256",
		}},
	}
	addr := cfg.Host + ":" + strconv.Itoa(int(port))

	raw, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	deadline := time.Now().Add(dialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = raw.SetDeadline(deadline)
	conn, channels, requests, err := ssh.NewClientConn(raw, addr, sshCfg)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("sshexec: SSH authentication/host verification failed: %w", err)
	}
	if !stop() && ctx.Err() != nil {
		conn.Close()
		return nil, ctx.Err()
	}
	_ = raw.SetDeadline(time.Time{})
	return &Client{conn: ssh.NewClient(conn, channels, requests)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Run executes one command via a fresh SSH session (ssh.Client multiplexes
// any number of sessions over one connection, so a session per command -
// matching how an interactive ssh/plink call would run a multi-line script
// - is normal here, not wasteful) and returns its combined stdout+stderr.
// A nonzero exit status is reported as an error whose message includes that
// output, since for a provisioning script the two together are almost
// always what explains the failure.
func (c *Client) Run(ctx context.Context, command string) (string, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("sshexec: new session: %w", err)
	}
	defer session.Close()

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := cappedCombinedOutput(session, command)
		ch <- result{out, err}
	}()
	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return string(r.out), fmt.Errorf("sshexec: command failed: %w: %s", r.err, strings.TrimSpace(string(r.out)))
		}
		return string(r.out), nil
	}
}

// A remote host is not trusted to keep its output small: read uncapped, a command
// writing forever would run the panel out of memory.
const (
	maxCommandOutput = 4 << 20
	maxFetchedFile   = 64 << 20
)

// cappedBuffer keeps the first limit bytes and quietly drops the rest. It must
// keep reporting success: a writer that fails would kill the session's copying.
type cappedBuffer struct {
	buf        bytes.Buffer
	limit      int
	overflowed bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room < len(p) {
		b.overflowed = true
		b.buf.Write(p[:max(room, 0)])
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// cappedCombinedOutput is session.CombinedOutput with a limit.
func cappedCombinedOutput(session *ssh.Session, command string) ([]byte, error) {
	var mu sync.Mutex
	out := cappedBuffer{limit: maxCommandOutput}
	writer := writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return out.Write(p)
	})
	session.Stdout, session.Stderr = writer, writer
	err := session.Run(command)
	mu.Lock()
	defer mu.Unlock()
	return out.buf.Bytes(), err
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// RunOut runs command and returns its stdout only (stderr is dropped), for
// fetching a file's raw bytes - Run's combined output would mix any warning
// into the data.
func (c *Client) RunOut(ctx context.Context, command string) ([]byte, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("sshexec: new session: %w", err)
	}
	defer session.Close()

	stdout := cappedBuffer{limit: maxFetchedFile}
	session.Stdout = &stdout

	ch := make(chan error, 1)
	go func() { ch <- session.Run(command) }()
	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return nil, ctx.Err()
	case err := <-ch:
		if err != nil {
			return nil, fmt.Errorf("sshexec: command failed: %w", err)
		}
		if stdout.overflowed {
			return nil, fmt.Errorf("sshexec: output is larger than %d bytes", maxFetchedFile)
		}
		return stdout.buf.Bytes(), nil
	}
}

// RunStdin runs command with stdin as its standard input - the way to put a
// large file (a binary, too big for a command line) on the remote host.
func (c *Client) RunStdin(ctx context.Context, command string, stdin []byte) (string, error) {
	session, err := c.conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("sshexec: new session: %w", err)
	}
	defer session.Close()

	session.Stdin = bytes.NewReader(stdin)

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := cappedCombinedOutput(session, command)
		ch <- result{out, err}
	}()
	select {
	case <-ctx.Done():
		session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return string(r.out), fmt.Errorf("sshexec: command failed: %w: %s", r.err, strings.TrimSpace(string(r.out)))
		}
		return string(r.out), nil
	}
}

// WriteFile writes content to remotePath via a single-quoted heredoc
// (nothing in content is ever shell-interpolated, regardless of what
// characters it contains) followed by chmod. mode is a chmod argument,
// e.g. "600".
func (c *Client) WriteFile(ctx context.Context, remotePath string, content []byte, mode string) error {
	_, err := c.Run(ctx, buildWriteFileScript(remotePath, content, mode))
	return err
}

// buildWriteFileScript is WriteFile's command-construction, pulled out so
// it can be tested directly against tricky content (a single quote, no
// trailing newline, binary-ish bytes) without needing a real SSH session
// for every case - only the end-to-end "does the server actually run
// this and write the file" path needs one.
func buildWriteFileScript(remotePath string, content []byte, mode string) string {
	marker := "SSHEXEC_EOF_8f2a1c"
	for strings.Contains("\n"+string(content)+"\n", "\n"+marker+"\n") {
		marker += "_X"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cat > %s <<'%s'\n", shellQuote(remotePath), marker)
	b.Write(content)
	if len(content) == 0 || content[len(content)-1] != '\n' {
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "%s\nchmod %s %s\n", marker, mode, shellQuote(remotePath))
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var hostKeysMu sync.Mutex

func verifyHostKey(host string, remote net.Addr, key ssh.PublicKey) error {
	hostKeysMu.Lock()
	defer hostKeysMu.Unlock()
	path := os.Getenv("SSH_KNOWN_HOSTS_FILE")
	if path == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		path = filepath.Join(base, "rapido-go", "known_hosts")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	f.Close()
	callback, err := knownhosts.New(path)
	if err != nil {
		return err
	}
	err = callback(host, remote, key)
	if err == nil {
		return nil
	}
	var mismatch *knownhosts.KeyError
	if !errors.As(err, &mismatch) || len(mismatch.Want) > 0 {
		return err
	}
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, knownhosts.Line([]string{knownhosts.Normalize(host)}, key))
	if err == nil {
		err = f.Sync()
	}
	return err
}
