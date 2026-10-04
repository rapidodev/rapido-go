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
// Host key verification is deliberately skipped (InsecureIgnoreHostKey):
// the credentials themselves are the trust decision an admin already made
// by typing them into the create-tunnel form, and pinning a host key would
// mean this package (or its caller) persisting and managing fingerprints
// for boxes that are not part of this fleet - a real feature, not a small
// addition, and out of scope for a first version of panel-driven
// provisioning. The same tradeoff most infra-automation tools (Ansible,
// Terraform's ssh provisioner) make by default.
package sshexec

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         dialTimeout,
	}
	addr := cfg.Host + ":" + strconv.Itoa(int(port))

	type result struct {
		conn *ssh.Client
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := ssh.Dial("tcp", addr, sshCfg)
		ch <- result{conn, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("sshexec: dial %s: %w", addr, r.err)
		}
		return &Client{conn: r.conn}, nil
	}
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
		out, err := session.CombinedOutput(command)
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
