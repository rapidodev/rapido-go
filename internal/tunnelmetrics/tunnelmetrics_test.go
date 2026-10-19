package tunnelmetrics

import (
	"os"
	"strings"
	"testing"
)

func TestParseOutputReadsEveryField(t *testing.T) {
	out := "CPU:17\nMEM:512:2048\nDISK:12:40\nRX:123456\nTX:654321\nRXDROP:3\nTXDROP:1\nRXERR:2\nTXERR:0\nCONN:7\n"
	m, err := parseOutput(out)
	if err != nil {
		t.Fatalf("parseOutput: %v", err)
	}
	if m.CPUPercent != 17 {
		t.Errorf("CPUPercent = %v, want 17", m.CPUPercent)
	}
	if m.MemUsedMB != 512 || m.MemTotalMB != 2048 {
		t.Errorf("Mem = %d/%d, want 512/2048", m.MemUsedMB, m.MemTotalMB)
	}
	if m.DiskUsedGB != 12 || m.DiskTotalGB != 40 {
		t.Errorf("Disk = %v/%v, want 12/40", m.DiskUsedGB, m.DiskTotalGB)
	}
	if m.RxBytes != 123456 || m.TxBytes != 654321 {
		t.Errorf("Rx/Tx = %d/%d, want 123456/654321", m.RxBytes, m.TxBytes)
	}
	if m.RxDropped != 3 || m.TxDropped != 1 || m.RxErrors != 2 || m.TxErrors != 0 {
		t.Errorf("loss counters = rxdrop=%d txdrop=%d rxerr=%d txerr=%d, want 3/1/2/0", m.RxDropped, m.TxDropped, m.RxErrors, m.TxErrors)
	}
	if m.Connections != 7 {
		t.Errorf("Connections = %d, want 7", m.Connections)
	}
	if m.CheckedAt.IsZero() {
		t.Error("CheckedAt was not set")
	}
}

// TestConnCountCommandMatchesEveryForwardedPortByLocalPort proves the ss
// filter counts connections whose LOCAL (listening) port is one of this
// tunnel's own forwarded ports - sport, not dport, since from the relay's
// own point of view an inbound client connection's local port is the one
// it's listening on - and suppresses ss's own header line (-H), which it
// otherwise always prints regardless of filters and would silently
// overcount every result by exactly one.
func TestConnCountCommandMatchesEveryForwardedPortByLocalPort(t *testing.T) {
	cmd := connCountCommand([]int32{20300, 20301})
	if !strings.Contains(cmd, "-tnH") {
		t.Errorf("command must suppress ss's header line: %s", cmd)
	}
	if !strings.Contains(cmd, "sport = :20300") || !strings.Contains(cmd, "sport = :20301") {
		t.Errorf("command must filter by local port for every forwarded port: %s", cmd)
	}
	if !strings.Contains(cmd, "state established") {
		t.Errorf("command must count only established connections: %s", cmd)
	}
}

func TestConnCountCommandWithNoPortsNeedsNoSSH(t *testing.T) {
	cmd := connCountCommand(nil)
	if strings.Contains(cmd, "ss ") {
		t.Errorf("a tunnel with no forwarded ports yet should not shell out to ss at all: %s", cmd)
	}
}

func TestParseOutputToleratesMissingOrOutOfOrderLines(t *testing.T) {
	// Real output from a box whose /sys GRE counters briefly 404 (interface
	// mid-flap) still has to parse everything else - partial data is better
	// than an all-or-nothing failure for a dashboard number.
	out := "MEM:100:1000\nCPU:5\n"
	m, err := parseOutput(out)
	if err != nil {
		t.Fatalf("parseOutput: %v", err)
	}
	if m.CPUPercent != 5 || m.MemUsedMB != 100 {
		t.Errorf("got %+v", m)
	}
	if m.RxBytes != 0 || m.TxBytes != 0 {
		t.Errorf("expected zero-value Rx/Tx when absent, got %d/%d", m.RxBytes, m.TxBytes)
	}
}

func TestParseOutputReadsTheMainNICRatesAndSpeed(t *testing.T) {
	out := "CPU:4\nNIC:eth0\nNICRX:1000000\nNICTX:2000000\nNICRXBPS:250000000\nNICTXBPS:260000000\nNICSPEED:10000\n"
	m, _ := parseOutput(out)
	if m.NIC != "eth0" || m.NICRxBps != 250e6 || m.NICTxBps != 260e6 || m.NICRxBytes != 1000000 || m.NICTxBytes != 2000000 {
		t.Errorf("NIC fields = %+v", m)
	}
	if m.NICSpeedMbps != 10000 {
		t.Errorf("speed = %d, want 10000", m.NICSpeedMbps)
	}
	// virtio NICs report -1 (or nothing): that is "unknown", not a speed.
	if m, _ := parseOutput("NICSPEED:-1\n"); m.NICSpeedMbps != 0 {
		t.Errorf("a negative speed must be dropped, got %d", m.NICSpeedMbps)
	}
}

func TestParseHAProxyBuildsTheFailoverStatus(t *testing.T) {
	out := "HAP:1\n" +
		"HAPSRV:frp_relay,relay_v6,UP,826,1125,0,3600\n" +
		"HAPSRV:frp_relay,relay_v4,UP,0,0,1,3600\n" +
		"HAPADDR:frp_relay,relay_v6,2a05:9080:3::fe4:7000\n" +
		"HAPADDR:frp_relay,relay_v4,5.159.54.82:7000\n"
	st := parseHAProxy(out)
	if st == nil || !st.Present || len(st.Servers) != 2 {
		t.Fatalf("status = %+v", st)
	}
	if st.Active != "relay_v6" {
		t.Errorf("active = %q, want the server carrying the sessions", st.Active)
	}
	v4 := st.Servers[1]
	if !v4.Backup || v4.Addr != "5.159.54.82:7000" || v4.Status != "UP" || v4.SinceSeconds != 3600 {
		t.Errorf("backup server = %+v", v4)
	}
}

func TestParseHAProxyDuringFailoverNamesTheBackupAsActive(t *testing.T) {
	out := "HAP:1\nHAPSRV:frp_relay,relay_v6,DOWN,0,1125,0,12\nHAPSRV:frp_relay,relay_v4,UP,139,139,1,12\n"
	st := parseHAProxy(out)
	if st.Active != "relay_v4" || st.Servers[0].Status != "DOWN" {
		t.Errorf("status = %+v", st)
	}
}

func TestParseHAProxyIsNilWhenTheScriptDidNotLook(t *testing.T) {
	if st := parseHAProxy("CPU:3\n"); st != nil {
		t.Errorf("expected nil without a HAP: line, got %+v", st)
	}
	if st := parseHAProxy("HAP:0\n"); st == nil || st.Present {
		t.Errorf("HAP:0 must mean present=false, got %+v", st)
	}
}

func TestBuildScriptRejectsUnsafeInterfaceAndSocketNames(t *testing.T) {
	s := buildScript("eth0; rm -rf /", "bad name$(x)", nil, "/run/h aproxy; x", "", "")
	for _, bad := range []string{"rm -rf", "$(x)", "aproxy; x"} {
		if strings.Contains(s, bad) {
			t.Errorf("script embeds unsafe input %q", bad)
		}
	}
	if good := buildScript("t1a2b3c", "eth1", []int32{20001}, "/run/haproxy/admin.sock", "", ""); !strings.Contains(good, `NIC="eth1"`) || !strings.Contains(good, "/run/haproxy/admin.sock") || !strings.Contains(good, "t1a2b3c") {
		t.Errorf("valid names were dropped:\n%s", good)
	}
}

// Developer tools, skipped unless their env var is set: DUMP_METRICS_SCRIPT
// writes the node-side script to a file so it can be run on a real server,
// and LIVE_METRICS_OUT feeds what it printed back through the parsers.
func TestDumpNodeMetricsScript(t *testing.T) {
	p := os.Getenv("DUMP_METRICS_SCRIPT")
	if p == "" {
		t.Skip("DUMP_METRICS_SCRIPT not set")
	}
	_ = os.WriteFile(p, []byte(buildScript("none", "", nil, "/run/haproxy/admin.sock", os.Getenv("DUMP_PATH_V6"), os.Getenv("DUMP_PATH_V4"))), 0o644)
}

func TestLiveMetricsOutput(t *testing.T) {
	p := os.Getenv("LIVE_METRICS_OUT")
	if p == "" {
		t.Skip("LIVE_METRICS_OUT not set")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := parseOutput(string(raw))
	t.Logf("nic=%s speed=%d rx=%.1f Mbit/s tx=%.1f Mbit/s cpu=%v", m.NIC, m.NICSpeedMbps, m.NICRxBps/1e6, m.NICTxBps/1e6, m.CPUPercent)
	st := parseHAProxy(string(raw))
	if st == nil {
		t.Log("haproxy: not looked at")
		return
	}
	t.Logf("haproxy present=%v active=%q", st.Present, st.Active)
	for _, s := range st.Servers {
		t.Logf("  %s/%s %s backup=%v sessions=%d total=%d since=%ds addr=%s", s.Backend, s.Name, s.Status, s.Backup, s.Sessions, s.TotalSession, s.SinceSeconds, s.Addr)
	}
}

func TestPathScriptPingsBothFamiliesAndRefusesBadAddresses(t *testing.T) {
	s := buildScript("none", "", nil, "", "2a05:9080:3::fe4", "5.159.54.82")
	for _, want := range []string{
		"ping -6 -n -q -c 10 -i 0.2 -W 1 2a05:9080:3::fe4",
		"ping -4 -n -q -c 10 -i 0.2 -W 1 5.159.54.82",
		"wait", `"PING6:`, `"PING4RTT:`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	// The bursts must start before the one-second sample and be collected after it.
	if strings.Index(s, "ping -6") > strings.Index(s, "sleep 1") || strings.Index(s, "wait") < strings.Index(s, "sleep 1") {
		t.Error("ping bursts must overlap the CPU sample's sleep")
	}
	bad := buildScript("none", "", nil, "", "x; reboot", "relay.example.com")
	if strings.Contains(bad, "ping ") || strings.Contains(bad, "reboot") || strings.Contains(bad, "PT=") {
		t.Errorf("an address that is not an IP literal must not reach the script:\n%s", bad)
	}
	if strings.Contains(buildScript("none", "", nil, "", "", ""), "wait") {
		t.Error("no target, no ping section")
	}
}

func TestParsePathReadsBothFamilies(t *testing.T) {
	out := "CPU:3\n" +
		"PING6:10 packets transmitted, 10 received, 0% packet loss, time 1800ms\n" +
		"PING6RTT:rtt min/avg/max/mdev = 89.9/90.1/90.9/0.3 ms\n" +
		"PING4:10 packets transmitted, 9 received, 10% packet loss, time 1810ms\n" +
		"PING4RTT:rtt min/avg/max/mdev = 80.0/81.5/95.0/4.1 ms\n"
	pm := parsePath(out, "2a05:9080:3::fe4", "5.159.54.82")
	if pm == nil || pm.V6 == nil || pm.V4 == nil {
		t.Fatalf("path = %+v", pm)
	}
	if pm.V6.AvgMs != 90.1 || pm.V6.LossPct != 0 || pm.V6.Target != "2a05:9080:3::fe4" {
		t.Errorf("v6 = %+v", pm.V6)
	}
	if pm.V4.LossPct != 10 || pm.V4.JitterMs != 4.1 || pm.V4.Received != 9 {
		t.Errorf("v4 = %+v", pm.V4)
	}
}

func TestParsePathTreatsAnUnroutableFamilyAsTotalLoss(t *testing.T) {
	out := "PING6:\nPING6RTT:\n"
	pm := parsePath(out, "2a05:9080:3::fe4", "")
	if pm == nil || pm.V6 == nil || pm.V6.LossPct != 100 || pm.V4 != nil {
		t.Fatalf("path = %+v", pm)
	}
	if parsePath("CPU:1\n", "", "") != nil || parsePath("CPU:1\n", "2a05:9080:3::fe4", "") != nil {
		t.Error("no ping lines (older script, or none asked) must give nil")
	}
}
