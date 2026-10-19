package tunneldoctor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// Runner is the part of an SSH connection the doctor uses; *sshexec.Client
// satisfies it, and tests supply a fake.
type Runner interface {
	Run(ctx context.Context, command string) (string, error)
	Close() error
}

// Dialer opens a Runner.
type Dialer func(ctx context.Context, cfg sshexec.Config) (Runner, error)

// DialSSH is the production Dialer.
func DialSSH(ctx context.Context, cfg sshexec.Config) (Runner, error) {
	c, err := sshexec.Dial(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Target is everything the doctor needs to know about a tunnel. The SSH logins
// are used to connect and never copied into the result.
type Target struct {
	ID       int32
	Name     string
	Method   string
	Protocol string

	NodeSSH  sshexec.Config
	RelaySSH sshexec.Config
	// RelayV6/RelayV4 are the relay addresses the node reaches it by (the ones
	// the node pings); either may be empty.
	RelayV6, RelayV4 string
	// ControlPort is the relay port the node's frp client connects to (0 = skip
	// the TCP connect probe).
	ControlPort   int
	NodeNIC       string
	RelayNIC      string
	HAProxySocket string
}

// HistoryFunc loads the stored samples and events between from and to.
type HistoryFunc func(ctx context.Context, from, to time.Time) ([]HistorySample, []HistoryEvent, error)

// HistoryHours is the window of stored data the diagnosis analyses.
const HistoryHours = 24

// ScriptTimeout bounds one end's measurement script: the pings take about 20 s
// and everything else runs beside them.
const ScriptTimeout = 90 * time.Second

// ErrNothingReachable is returned when neither end could be measured.
var ErrNothingReachable = errors.New("neither the node nor the relay could be reached over SSH")

// factsScript asks the node which of its own addresses it uses to reach the
// relay - the addresses the relay must ping to measure the way back.
func factsScript(relayV6, relayV4 string) string {
	var b strings.Builder
	if a := netprobe.CanonicalAddr(relayV6); a != "" && netprobe.AddrFamily(a) == 6 {
		fmt.Fprintf(&b, "echo \"SRC6:$(ip -6 route get '%s' 2>/dev/null | grep -oE ' src [^ ]+' | head -1 | awk '{print $2}')\"\n", a)
	}
	if a := netprobe.CanonicalAddr(relayV4); a != "" && netprobe.AddrFamily(a) == 4 {
		fmt.Fprintf(&b, "echo \"SRC4:$(ip -4 route get '%s' 2>/dev/null | grep -oE ' src [^ ]+' | head -1 | awk '{print $2}')\"\n", a)
	}
	return b.String()
}

// parseFacts reads the SRC6/SRC4 lines.
func parseFacts(out string) (src6, src4 string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "SRC6:"):
			src6 = netprobe.CanonicalAddr(strings.TrimPrefix(line, "SRC6:"))
		case strings.HasPrefix(line, "SRC4:"):
			src4 = netprobe.CanonicalAddr(strings.TrimPrefix(line, "SRC4:"))
		}
	}
	return src6, src4
}

// usableV6 reports whether an IPv6 address is one the relay could be expected
// to reach (not link-local, loopback or unspecified).
func usableV6(s string) string {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is6() || a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsUnspecified() {
		return ""
	}
	return a.String()
}

// Run performs a full diagnosis. It returns an error only when nothing at all
// could be measured; a partial result (one end unreachable, no history) is
// returned with its findings.
func Run(ctx context.Context, t Target, dial Dialer, hist HistoryFunc, clock func() time.Time) (*Result, error) {
	if clock == nil {
		clock = time.Now
	}
	started := clock().UTC()
	res := &Result{
		Tunnel:    TunnelInfo{ID: t.ID, Name: t.Name, Method: t.Method, Protocol: t.Protocol},
		StartedAt: started,
		Environment: Environment{
			NodeAddress: t.NodeSSH.Host, RelayAddress: t.RelaySSH.Host, ControlPort: t.ControlPort,
			PingCount: PingCount, PingIntervalSec: PingIntervalSec,
		},
	}
	relayV6, relayV4 := netprobe.CanonicalAddr(t.RelayV6), netprobe.CanonicalAddr(t.RelayV4)
	if netprobe.AddrFamily(relayV6) != 6 {
		relayV6 = ""
	}
	if netprobe.AddrFamily(relayV4) != 4 {
		relayV4 = ""
	}
	res.Environment.RelayV6, res.Environment.RelayV4 = relayV6, relayV4

	// Connect to both ends at once.
	var node, relay Runner
	var nodeErr, relayErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); node, nodeErr = dial(ctx, t.NodeSSH) }()
	go func() { defer wg.Done(); relay, relayErr = dial(ctx, t.RelaySSH) }()
	wg.Wait()
	if node != nil {
		defer node.Close()
	}
	if relay != nil {
		defer relay.Close()
	}
	res.Node = EndResult{Role: "node", Reachable: nodeErr == nil, Error: errText(nodeErr)}
	res.Relay = EndResult{Role: "relay", Reachable: relayErr == nil, Error: errText(relayErr)}
	if nodeErr != nil && relayErr != nil {
		return nil, ErrNothingReachable
	}

	// The relay measures towards the node: its IPv4 is the address it is
	// managed at when that is a literal, else the one the node uses towards
	// the relay; its IPv6 is the node's source address towards the relay.
	nodeV4, nodeV6 := "", ""
	if a := netprobe.CanonicalAddr(t.NodeSSH.Host); netprobe.AddrFamily(a) == 4 {
		nodeV4 = a
	} else if netprobe.AddrFamily(a) == 6 {
		nodeV6 = usableV6(a)
	}
	if node != nil {
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, err := node.Run(fctx, factsScript(relayV6, relayV4))
		cancel()
		if err == nil {
			src6, src4 := parseFacts(out)
			if nodeV6 == "" {
				nodeV6 = usableV6(src6)
			}
			if nodeV4 == "" {
				nodeV4 = src4
			}
		}
	}
	res.Environment.NodeV6, res.Environment.NodeV4 = nodeV6, nodeV4

	// Measure on both ends at the same time.
	var nodeOut, relayOut string
	var nodeRunErr, relayRunErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		if node == nil {
			return
		}
		sctx, cancel := context.WithTimeout(ctx, ScriptTimeout)
		defer cancel()
		nodeOut, nodeRunErr = node.Run(sctx, buildScript(scriptParams{
			Peer6: relayV6, Peer4: relayV4, ControlPort: t.ControlPort, NIC: t.NodeNIC, HASocket: t.HAProxySocket,
		}))
	}()
	go func() {
		defer wg.Done()
		if relay == nil {
			return
		}
		sctx, cancel := context.WithTimeout(ctx, ScriptTimeout)
		defer cancel()
		relayOut, relayRunErr = relay.Run(sctx, buildScript(scriptParams{Peer6: nodeV6, Peer4: nodeV4, NIC: t.RelayNIC}))
	}()
	wg.Wait()

	if node != nil {
		if nodeRunErr != nil {
			res.Node.Reachable, res.Node.Error = false, nodeRunErr.Error()
		} else {
			res.Node = parseEnd("node", nodeOut)
			for i := range res.Node.TCP {
				res.Node.TCP[i].Port = t.ControlPort
				if res.Node.TCP[i].Family == "v6" {
					res.Node.TCP[i].Address = relayV6
				} else {
					res.Node.TCP[i].Address = relayV4
				}
			}
			setTargets(&res.Node, relayV6, relayV4)
		}
	}
	if relay != nil {
		if relayRunErr != nil {
			res.Relay.Reachable, res.Relay.Error = false, relayRunErr.Error()
		} else {
			res.Relay = parseEnd("relay", relayOut)
			setTargets(&res.Relay, nodeV6, nodeV4)
		}
	}
	if nodeErr == nil && nodeRunErr != nil && relayErr == nil && relayRunErr != nil {
		return nil, ErrNothingReachable
	}
	res.Environment.NodeKernel, res.Environment.RelayKernel = res.Node.Kernel, res.Relay.Kernel
	if n := res.Node.NIC; n != nil {
		res.Environment.NodeNIC, res.Environment.NodeNICSpeed = n.Name, n.SpeedMbps
	}
	if n := res.Relay.NIC; n != nil {
		res.Environment.RelayNIC, res.Environment.RelayNICSpeed = n.Name, n.SpeedMbps
	}

	to := clock().UTC()
	from := to.Add(-HistoryHours * time.Hour)
	var samples []HistorySample
	var events []HistoryEvent
	if hist != nil {
		if s, e, err := hist(ctx, from, to); err == nil {
			samples, events = s, e
		}
	}
	res.History = AnalyzeHistory(samples, events, from, to)
	res.FinishedAt = to
	res.Findings = BuildFindings(res)
	if res.Findings == nil {
		res.Findings = []Finding{}
	}
	return res, nil
}

// setTargets records which address each ping family was aimed at.
func setTargets(end *EndResult, v6, v4 string) {
	if end.Ping.V6 != nil {
		end.Ping.V6.Target = v6
	}
	if end.Ping.V4 != nil {
		end.Ping.V4.Target = v4
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
