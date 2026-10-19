package netprobe

import (
	"math"
	"strings"
	"testing"
)

const pingOK = `
--- 2001:db8:3::fe4 ping statistics ---
5 packets transmitted, 5 received, 0% packet loss, time 800ms
rtt min/avg/max/mdev = 89.995/90.123/90.340/0.149 ms
`

const pingLost = `
--- 192.0.2.1 ping statistics ---
3 packets transmitted, 0 received, 100% packet loss, time 408ms
`

const pingPartial = `--- x ping statistics ---
10 packets transmitted, 9 received, +1 errors, 10% packet loss, time 1810ms
rtt min/avg/max/mdev = 1.0/2.5/9.0/1.25 ms`

func TestParsePing(t *testing.T) {
	p, ok := ParsePing(pingOK)
	if !ok || p.Sent != 5 || p.Received != 5 || p.LossPct != 0 || p.MinMs != 89.995 || p.AvgMs != 90.123 || p.MaxMs != 90.340 || p.JitterMs != 0.149 {
		t.Fatalf("ok case: %+v ok=%v", p, ok)
	}
	p, ok = ParsePing(pingLost)
	if !ok || p.Sent != 3 || p.Received != 0 || p.LossPct != 100 || p.AvgMs != 0 {
		t.Fatalf("lost case: %+v ok=%v", p, ok)
	}
	p, ok = ParsePing(pingPartial)
	if !ok || p.Received != 9 || p.LossPct != 10 || p.JitterMs != 1.25 {
		t.Fatalf("partial case: %+v ok=%v", p, ok)
	}
	if _, ok := ParsePing("ping: connect: Network is unreachable"); ok {
		t.Fatal("error text must not parse as statistics")
	}
	// BusyBox prints no deviation.
	p, ok = ParsePing("3 packets transmitted, 3 packets received, 0% packet loss\nround-trip min/avg/max = 1.0/2.0/3.0 ms")
	if !ok || p.AvgMs != 2 || p.JitterMs != 0 {
		t.Fatalf("busybox: %+v ok=%v", p, ok)
	}
}

func TestPickAddrs(t *testing.T) {
	v6, v4 := PickAddrs("relay.example.com", "", "2001:db8:3::fe4", "203.0.113.44", "203.0.113.82", "fe80::1%eth0")
	if v6 != "2001:db8:3::fe4" || v4 != "203.0.113.44" {
		t.Fatalf("got %q %q", v6, v4)
	}
	v6, v4 = PickAddrs("only.name")
	if v6 != "" || v4 != "" {
		t.Fatalf("hostname must be ignored: %q %q", v6, v4)
	}
}

func TestPingCommandRefusesUnsafeInput(t *testing.T) {
	if c := PingCommand(6, "2001:db8:3::fe4", 10, 0.2, 1, ""); c != "ping -6 -n -q -c 10 -i 0.2 -W 1 2001:db8:3::fe4" {
		t.Fatalf("got %q", c)
	}
	for _, bad := range []string{"1.2.3.4; reboot", "$(id)", "host.name", "", "fe80::1%eth0"} {
		if c := PingCommand(4, bad, 3, 0.2, 1, ""); c != "" {
			t.Fatalf("%q must be refused, got %q", bad, c)
		}
	}
	if PingCommand(4, "2001:db8:3::fe4", 3, 0.2, 1, "") != "" {
		t.Fatal("family mismatch must be refused")
	}
	if c := PingCommand(4, "10.0.0.1", 1, 0.2, 1, "-M do -s 1472"); !strings.Contains(c, "-M do -s 1472 10.0.0.1") {
		t.Fatalf("extra flags lost: %q", c)
	}
}

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{5, 1, 3, 2, 4})
	if s.N != 5 || s.Min != 1 || s.Max != 5 || s.Avg != 3 || s.P50 != 3 {
		t.Fatalf("%+v", s)
	}
	if math.Abs(s.P90-4.6) > 1e-9 {
		t.Fatalf("p90 = %v", s.P90)
	}
	if (Summarize(nil) != Stats{}) {
		t.Fatal("empty series must give zero stats")
	}
	if Summarize([]float64{7}).P99 != 7 {
		t.Fatal("single value percentile")
	}
}
