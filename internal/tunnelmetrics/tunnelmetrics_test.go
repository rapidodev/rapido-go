package tunnelmetrics

import (
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
