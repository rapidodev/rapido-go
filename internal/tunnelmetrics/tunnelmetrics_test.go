package tunnelmetrics

import "testing"

func TestParseOutputReadsEveryField(t *testing.T) {
	out := "CPU:17\nMEM:512:2048\nDISK:12:40\nRX:123456\nTX:654321\n"
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
	if m.CheckedAt.IsZero() {
		t.Error("CheckedAt was not set")
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
