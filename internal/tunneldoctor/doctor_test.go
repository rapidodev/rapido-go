package tunneldoctor

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/sshexec"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseEndNodeFixture(t *testing.T) {
	end := parseEnd("node", fixture(t, "node.out"))
	if !end.Reachable || end.Kernel != "6.8.0-31-generic" {
		t.Fatalf("end = %+v", end)
	}
	if end.Ping.V6 == nil || end.Ping.V6.Sent != 100 || end.Ping.V6.Received != 100 || end.Ping.V6.AvgMs != 90.022 || end.Ping.V6.JitterMs != 0.108 {
		t.Errorf("v6 ping = %+v", end.Ping.V6)
	}
	if end.Ping.V4 == nil || end.Ping.V4.MinMs != 80.983 {
		t.Errorf("v4 ping = %+v", end.Ping.V4)
	}
	if len(end.TCP) != 2 || end.TCP[0].Family != "v6" || end.TCP[0].Attempts != 5 || end.TCP[0].Successes != 5 || end.TCP[0].MinMs < 92 || end.TCP[0].MaxMs > 96 {
		t.Errorf("tcp = %+v", end.TCP)
	}
	var v4 MTUResult
	for _, m := range end.MTU {
		if m.Family == "v4" {
			v4 = m
		}
	}
	if v4.LargestOK != 1400 || v4.PathMTU != 1428 || len(v4.Probes) != 5 || v4.Probes[0].Payload != 1472 || v4.Probes[0].OK {
		t.Errorf("v4 mtu = %+v", v4)
	}
	for _, m := range end.MTU {
		if m.Family == "v6" && (m.LargestOK != 1452 || m.PathMTU != 1500) {
			t.Errorf("v6 mtu = %+v", m)
		}
	}
	// Hops: trimmed at the first one the destination answered.
	var h6, h4 HopPath
	for _, hp := range end.Hops {
		if hp.Family == "v6" {
			h6 = hp
		} else {
			h4 = hp
		}
	}
	if len(h6.Hops) != 8 || !h6.Hops[7].Reached || h6.Hops[7].TTL != 8 || h6.Hops[7].AvgMs != 89.968 {
		t.Errorf("v6 hops = %+v", h6.Hops)
	}
	if h6.Hops[0].Addr != "2a12:de40:51::1" || h6.Hops[2].Addr != "" || h6.Hops[2].Replies != 0 {
		t.Errorf("v6 hop parsing: %+v %+v", h6.Hops[0], h6.Hops[2])
	}
	if len(h4.Hops) != 5 || h4.Hops[4].Addr != "203.0.113.82" || !h4.Hops[4].Reached {
		t.Errorf("v4 hops = %+v", h4.Hops)
	}
	// Counters are deltas over the test.
	if end.NIC == nil || end.NIC.Name != "eth0" || end.NIC.TxPackets != 87113 || end.NIC.QdiscDropped != 0 || end.NIC.TotalQdiscDropped != 158017 {
		t.Errorf("nic = %+v", end.NIC)
	}
	if end.TCPStat == nil || end.TCPStat.OutSegs != 284682 || end.TCPStat.RetransSegs != 358 {
		t.Errorf("tcp stats = %+v", end.TCPStat)
	}
	if end.TCPStat.RetransPct < 0.12 || end.TCPStat.RetransPct > 0.13 || end.TCPStat.TotalRetransPct < 1.3 || end.TCPStat.TotalRetransPct > 1.5 {
		t.Errorf("retrans pct = %v / %v", end.TCPStat.RetransPct, end.TCPStat.TotalRetransPct)
	}
	if end.HAProxy == nil || !end.HAProxy.Present || len(end.HAProxy.Servers) != 2 {
		t.Errorf("haproxy = %+v", end.HAProxy)
	}
}

func TestParseEndRelayFixtureSeesCardDrops(t *testing.T) {
	end := parseEnd("relay", fixture(t, "relay.out"))
	if end.NIC == nil || end.NIC.RxDropped != 10 {
		t.Fatalf("nic = %+v", end.NIC)
	}
	if end.Ping.V6 == nil || end.Ping.V6.AvgMs != 87.061 {
		t.Errorf("ping = %+v", end.Ping.V6)
	}
	if len(end.TCP) != 0 {
		t.Errorf("the relay probes no control port: %+v", end.TCP)
	}
	if end.HAProxy != nil {
		t.Error("no HAProxy section was printed")
	}
}

func TestParseEndSurvivesGarbageAndTotalLoss(t *testing.T) {
	end := parseEnd("node", "PING6:\nPING6RTT:\nTCP6:-1\nTCP6:-1\nMTU6:1452:\nHOP6:x\nrandom noise\n")
	if end.Ping.V6 == nil || end.Ping.V6.LossPct != 100 {
		t.Errorf("ping = %+v", end.Ping.V6)
	}
	if len(end.TCP) != 1 || end.TCP[0].Attempts != 2 || end.TCP[0].Successes != 0 {
		t.Errorf("tcp = %+v", end.TCP)
	}
	if len(end.MTU) != 1 || end.MTU[0].LargestOK != 0 || end.MTU[0].PathMTU != 0 {
		t.Errorf("mtu = %+v", end.MTU)
	}
	if end.NIC != nil || end.TCPStat != nil {
		t.Error("no counter lines, no counters")
	}
}

func TestBuildScriptOnlyEmbedsCheckedAddresses(t *testing.T) {
	s := buildScript(scriptParams{Peer6: "2001:db8::2", Peer4: "198.51.100.7", ControlPort: 7000, HASocket: "/run/haproxy/admin.sock", NIC: "eth0"})
	for _, want := range []string{
		"ping -6 -n -q -c 100 -i 0.2 -W 1 2001:db8::2", "ping -4 -n -q -c 100 -i 0.2 -W 1 198.51.100.7",
		"-M do -s $s 2001:db8::2", "-t $t 198.51.100.7", "/dev/tcp/$0/$1' '198.51.100.7' 7000", `NIC="eth0"`, "HAP:1",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	bad := buildScript(scriptParams{Peer6: "x; reboot", Peer4: "host.example", ControlPort: 7000, NIC: "eth0; id"})
	for _, no := range []string{"reboot", "host.example", "ping ", "/dev/tcp", "eth0; id"} {
		if strings.Contains(bad, no) {
			t.Errorf("script embeds %q:\n%s", no, bad)
		}
	}
	if strings.Contains(buildScript(scriptParams{Peer4: "198.51.100.7"}), "/dev/tcp") {
		t.Error("no control port, no TCP probe")
	}
}

func TestParseFacts(t *testing.T) {
	s6, s4 := parseFacts("SRC6:2001:db8:51::4aa4\nSRC4:198.51.100.46\n")
	if s6 != "2001:db8:51::4aa4" || s4 != "198.51.100.46" {
		t.Errorf("%q %q", s6, s4)
	}
	if s6, s4 = parseFacts("SRC6:\nSRC4:not-an-ip\n"); s6 != "" || s4 != "" {
		t.Errorf("%q %q", s6, s4)
	}
	if usableV6("fe80::1") != "" || usableV6("::1") != "" || usableV6("2001:db8::1") == "" {
		t.Error("usableV6")
	}
}

type fakeRunner struct {
	mu      sync.Mutex
	node    bool
	closed  bool
	failRun bool
	cmds    []string
}

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.mu.Lock()
	f.cmds = append(f.cmds, cmd)
	f.mu.Unlock()
	if f.failRun {
		return "", errors.New("session failed")
	}
	switch {
	case strings.Contains(cmd, "SRC6:"):
		return "SRC6:2001:db8:51::4aa4\nSRC4:198.51.100.46\n", nil
	case f.node:
		return fixtureString("node.out"), nil
	}
	return fixtureString("relay.out"), nil
}
func (f *fakeRunner) Close() error { f.closed = true; return nil }

func fixtureString(name string) string {
	b, _ := os.ReadFile("testdata/" + name)
	return string(b)
}

func testTarget() Target {
	return Target{
		ID: 3, Name: "node2", Method: "external", Protocol: "frp over TCP via HAProxy",
		NodeSSH: sshexec.Config{Host: "198.51.100.46", User: "root", Password: "node-secret-pw"}, RelaySSH: sshexec.Config{Host: "203.0.113.44", User: "root", Password: "relay-secret-pw"},
		RelayV6: "2001:db8:3::fe4", RelayV4: "203.0.113.82", ControlPort: 7000, HAProxySocket: "/run/haproxy/admin.sock",
	}
}

func TestRunProducesAStructuredResultFromBothEnds(t *testing.T) {
	node, relay := &fakeRunner{node: true}, &fakeRunner{}
	dial := func(_ context.Context, cfg sshexec.Config) (Runner, error) {
		if cfg.Host == "198.51.100.46" {
			return node, nil
		}
		return relay, nil
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	hist := func(_ context.Context, from, to time.Time) ([]HistorySample, []HistoryEvent, error) {
		if to.Sub(from) != 24*time.Hour {
			t.Errorf("history window = %v", to.Sub(from))
		}
		return nil, nil, nil
	}
	res, err := Run(context.Background(), testTarget(), dial, hist, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if !node.closed || !relay.closed {
		t.Error("connections must be closed")
	}
	if res.Environment.NodeV6 != "2001:db8:51::4aa4" || res.Environment.NodeV4 != "198.51.100.46" || res.Environment.RelayV6 != "2001:db8:3::fe4" {
		t.Errorf("environment = %+v", res.Environment)
	}
	// the relay is told to ping the node's own addresses
	var relayScript string
	for _, c := range relay.cmds {
		if strings.Contains(c, "ping -6") {
			relayScript = c
		}
	}
	if !strings.Contains(relayScript, "2001:db8:51::4aa4") || !strings.Contains(relayScript, "198.51.100.46") {
		t.Errorf("relay script targets: %s", relayScript)
	}
	if res.Node.Ping.V6.Target != "2001:db8:3::fe4" || res.Relay.Ping.V4.Target != "198.51.100.46" {
		t.Errorf("targets: %q %q", res.Node.Ping.V6.Target, res.Relay.Ping.V4.Target)
	}
	if res.Node.TCP[0].Port != 7000 || res.Node.TCP[0].Address != "2001:db8:3::fe4" {
		t.Errorf("tcp = %+v", res.Node.TCP[0])
	}
	if res.Environment.NodeNIC != "eth0" || res.Environment.NodeKernel == "" {
		t.Errorf("environment = %+v", res.Environment)
	}
	codes := map[string]bool{}
	for _, f := range res.Findings {
		codes[f.Code] = true
	}
	// Real behaviour of the fixture: a v4 path MTU of 1428 in both directions,
	// and card rx drops on the relay.
	for _, want := range []string{"mtu_node_v4", "mtu_relay_v4", "nic_relay", "retrans_history_node"} {
		if !codes[want] {
			t.Errorf("missing finding %s (have %v)", want, codes)
		}
	}
	for _, no := range []string{"loss_node_ipv6", "mtu_node_v6", "ha_on_backup"} {
		if codes[no] {
			t.Errorf("unexpected finding %s", no)
		}
	}
}

func TestRunWithOneEndDownIsPartial(t *testing.T) {
	relay := &fakeRunner{}
	dial := func(_ context.Context, cfg sshexec.Config) (Runner, error) {
		if cfg.Host == "198.51.100.46" {
			return nil, errors.New("connection refused")
		}
		return relay, nil
	}
	res, err := Run(context.Background(), testTarget(), dial, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Node.Reachable || res.Node.Error == "" || !res.Relay.Reachable {
		t.Fatalf("node=%+v relay=%v", res.Node, res.Relay.Reachable)
	}
	// With the node down the relay still pings the node's SSH address.
	if res.Environment.NodeV4 != "198.51.100.46" {
		t.Errorf("node v4 = %q", res.Environment.NodeV4)
	}
	found := false
	for _, f := range res.Findings {
		if f.Code == "unreachable_node" && f.Severity == SevCritical {
			found = true
		}
	}
	if !found {
		t.Error("an unreachable end is a critical finding")
	}
}

func TestRunFailsWhenNothingIsReachable(t *testing.T) {
	dial := func(context.Context, sshexec.Config) (Runner, error) { return nil, errors.New("no route") }
	if _, err := Run(context.Background(), testTarget(), dial, nil, nil); !errors.Is(err, ErrNothingReachable) {
		t.Fatalf("err = %v", err)
	}
	failing := &fakeRunner{failRun: true}
	dial = func(context.Context, sshexec.Config) (Runner, error) { return failing, nil }
	if _, err := Run(context.Background(), testTarget(), dial, nil, nil); !errors.Is(err, ErrNothingReachable) {
		t.Fatalf("both scripts failing: err = %v", err)
	}
}

func TestReportsNeverContainCredentialsAndCarryTheMeasurements(t *testing.T) {
	node, relay := &fakeRunner{node: true}, &fakeRunner{}
	dial := func(_ context.Context, cfg sshexec.Config) (Runner, error) {
		if cfg.Host == "198.51.100.46" {
			return node, nil
		}
		return relay, nil
	}
	res, err := Run(context.Background(), testTarget(), dial, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "fa"} {
		md := BuildReport(res, lang)
		for _, secret := range []string{"node-secret-pw", "relay-secret-pw", "Password", "root@"} {
			if strings.Contains(md, secret) {
				t.Errorf("%s report leaks %q", lang, secret)
			}
		}
		for _, want := range []string{"2001:db8:3::fe4", "203.0.113.82", "90.02", "1428", "```"} {
			if !strings.Contains(md, want) {
				t.Errorf("%s report lacks %q", lang, want)
			}
		}
		if strings.Count(md, "## ") < 8 {
			t.Errorf("%s report has too few sections", lang)
		}
	}
	en, fa := BuildReport(res, "en"), BuildReport(res, "fa")
	if !strings.Contains(en, "## 1. Summary") || !strings.Contains(en, "What we ask the network team to check") {
		t.Error("english section titles")
	}
	if !strings.Contains(fa, "خلاصه") || !strings.Contains(fa, `dir="rtl"`) {
		t.Error("persian report must be right-to-left and translated")
	}
	if NormalizeLang("FA") != "fa" || NormalizeLang("") != "en" || NormalizeLang("de") != "en" {
		t.Error("NormalizeLang")
	}
}

func TestQuestionsFollowTheData(t *testing.T) {
	res := &Result{Environment: Environment{RelayV6: "2001:db8::1", NodeV6: "2001:db8::2"}}
	base := Questions(res, "en")
	if len(base) != 3 {
		t.Fatalf("with clean data only the generic questions remain, got %d: %v", len(base), base)
	}
	res.History = History{WindowHours: 24, Samples: 100, Plateau: PlateauSummary{Detected: true, LevelMbps: 300}, OutageCount: 2}
	res.Node.Ping.V6 = nil
	lossy := 4.0
	res.History.V6LossPct = &lossy
	qs := Questions(res, "en")
	joined := strings.Join(qs, "\n")
	for _, want := range []string{"flat at about 300 Mbit/s", "Packet loss was measured", "stopped answering 2 time(s)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("questions lack %q:\n%s", want, joined)
		}
	}
	if len(Questions(res, "fa")) != len(qs) {
		t.Error("both languages ask the same questions")
	}
}

// Developer tool, skipped unless DOCTOR_REPORT_OUT is set: writes the reports
// built from the fixtures (<prefix>.en.md / <prefix>.fa.md) for proofreading.
func TestDumpReports(t *testing.T) {
	prefix := os.Getenv("DOCTOR_REPORT_OUT")
	if prefix == "" {
		t.Skip("DOCTOR_REPORT_OUT not set")
	}
	node, relay := &fakeRunner{node: true}, &fakeRunner{}
	dial := func(_ context.Context, cfg sshexec.Config) (Runner, error) {
		if cfg.Host == "198.51.100.46" {
			return node, nil
		}
		return relay, nil
	}
	hist := func(_ context.Context, from, to time.Time) ([]HistorySample, []HistoryEvent, error) {
		return syntheticHistory(to)
	}
	res, err := Run(context.Background(), testTarget(), dial, hist, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "fa"} {
		if err := os.WriteFile(prefix+"."+lang+".md", []byte(BuildReport(res, lang)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
