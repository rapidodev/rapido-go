// Package netprobe holds the small, pure pieces shared by everything that
// measures a network path by running standard tools (ping, nstat, ip) on a
// remote box over SSH: building the commands with a checked address, and
// parsing what they print. Nothing here talks to the network itself, so every
// parser is tested against captured output.
package netprobe

import (
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Ping is the summary line pair `ping -q` prints. The round-trip fields are
// zero when no reply came back (Received == 0).
type Ping struct {
	Target   string  `json:"target,omitempty"`
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
	LossPct  float64 `json:"loss_pct"`
	MinMs    float64 `json:"min_ms"`
	AvgMs    float64 `json:"avg_ms"`
	MaxMs    float64 `json:"max_ms"`
	// JitterMs is the standard deviation iputils reports as mdev.
	JitterMs float64 `json:"jitter_ms"`
}

var (
	pingCountRE = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received`)
	pingLossRE  = regexp.MustCompile(`(\d+(?:\.\d+)?)% packet loss`)
	pingRTTRE   = regexp.MustCompile(`(?:rtt|round-trip) min/avg/max(?:/(?:mdev|stddev))? = ([\d.]+)/([\d.]+)/([\d.]+)(?:/([\d.]+))? ms`)
)

// ParsePing reads the statistics out of `ping -q` output (iputils and BusyBox
// both). ok is false when the text carries no statistics line at all, which is
// what a ping that could not even resolve or route prints.
func ParsePing(out string) (p Ping, ok bool) {
	m := pingCountRE.FindStringSubmatch(out)
	if m == nil {
		return Ping{}, false
	}
	p.Sent, _ = strconv.Atoi(m[1])
	p.Received, _ = strconv.Atoi(m[2])
	if l := pingLossRE.FindStringSubmatch(out); l != nil {
		p.LossPct, _ = strconv.ParseFloat(l[1], 64)
	} else if p.Sent > 0 {
		p.LossPct = 100 * float64(p.Sent-p.Received) / float64(p.Sent)
	}
	if r := pingRTTRE.FindStringSubmatch(out); r != nil {
		p.MinMs, _ = strconv.ParseFloat(r[1], 64)
		p.AvgMs, _ = strconv.ParseFloat(r[2], 64)
		p.MaxMs, _ = strconv.ParseFloat(r[3], 64)
		if r[4] != "" {
			p.JitterMs, _ = strconv.ParseFloat(r[4], 64)
		}
	}
	return p, true
}

// Family is 4 or 6.
type Family int

// AddrFamily returns the family of an IP literal, or 0 if s is not a plain IP
// address. A scoped IPv6 address (fe80::1%eth0) is refused: the zone is not
// something this package will let reach a shell.
func AddrFamily(s string) Family {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.Zone() != "" {
		return 0
	}
	if a.Is4() || a.Is4In6() {
		return 4
	}
	return 6
}

// CanonicalAddr returns s normalised (IPv4-mapped unwrapped), or "" if it is
// not a plain IP literal. Safe to embed in a shell command.
func CanonicalAddr(s string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.Zone() != "" {
		return ""
	}
	return a.Unmap().String()
}

// PickAddrs picks the first IPv6 and the first IPv4 literal from candidates
// (in order), ignoring anything that is not an IP address (a hostname, an
// empty string). Either result may be empty.
func PickAddrs(candidates ...string) (v6, v4 string) {
	for _, c := range candidates {
		addr := CanonicalAddr(c)
		if addr == "" {
			continue
		}
		switch AddrFamily(addr) {
		case 6:
			if v6 == "" {
				v6 = addr
			}
		case 4:
			if v4 == "" {
				v4 = addr
			}
		}
	}
	return v6, v4
}

// PingCommand builds a quiet ping of count packets spaced interval seconds
// apart, each waiting at most waitSec for its reply. addr must be an IP
// literal of the given family; anything else yields "" so a caller can never
// splice an unchecked value into a shell line. extra is appended before the
// address and must be a constant supplied by the caller (for example
// "-M do -s 1472").
func PingCommand(fam Family, addr string, count int, interval float64, waitSec int, extra string) string {
	addr = CanonicalAddr(addr)
	if addr == "" || AddrFamily(addr) != fam || (fam != 4 && fam != 6) {
		return ""
	}
	if count < 1 {
		count = 1
	}
	if waitSec < 1 {
		waitSec = 1
	}
	cmd := fmt.Sprintf("ping -%d -n -q -c %d -i %s -W %d", fam, count, strconv.FormatFloat(interval, 'f', -1, 64), waitSec)
	if extra != "" {
		cmd += " " + extra
	}
	return cmd + " " + addr
}

// Stats summarises a series of numbers: the percentiles the Network Doctor
// quotes next to the maximum.
type Stats struct {
	N   int     `json:"n"`
	Min float64 `json:"min"`
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// Summarize computes Stats; an empty series gives the zero value.
func Summarize(values []float64) Stats {
	if len(values) == 0 {
		return Stats{}
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	var sum float64
	for _, x := range v {
		sum += x
	}
	return Stats{
		N: len(v), Min: v[0], Avg: sum / float64(len(v)),
		P50: percentile(v, 50), P90: percentile(v, 90), P99: percentile(v, 99), Max: v[len(v)-1],
	}
}

// percentile uses linear interpolation between closest ranks on an already
// sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(rank-float64(lo))
}
