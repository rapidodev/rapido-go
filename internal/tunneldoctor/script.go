package tunneldoctor

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/legendary1205/rapido-go/internal/netprobe"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

// Measurement settings. The pings are the heart of the test: a hundred, 0.2 s
// apart, is twenty seconds of one probe every 200 ms, enough to see a loss
// rate down to 1% and to catch short bursts.
const (
	PingCount       = 100
	PingIntervalSec = 0.2
	tcpAttempts     = 5
	maxHops         = 16
	hopProbes       = 4
)

// MTU sizes tried, largest first (ICMP payload bytes). 1472 / 1452 are the
// largest payloads of a standard 1500-byte IPv4 / IPv6 packet.
var (
	mtuSizes4 = []int{1472, 1452, 1400, 1372, 1272}
	mtuSizes6 = []int{1452, 1432, 1392, 1352, 1232}
)

const (
	ipv4Overhead = 28 // IP 20 + ICMP 8
	ipv6Overhead = 48 // IPv6 40 + ICMP 8
)

type scriptParams struct {
	Peer6, Peer4 string
	ControlPort  int
	NIC          string
	HASocket     string
}

// measureScript is run on one end. It takes counter snapshots, runs every
// probe in parallel (so the whole script takes about as long as the ping
// burst), takes the snapshots again and prints one prefixed line per result.
// The addresses are IP literals checked by netprobe and substituted as
// quoted constants; nothing from outside is ever executed.
const measureScript = `
{{NICSETUP}}
PT=$(mktemp -d 2>/dev/null); [ -n "$PT" ] || PT=/tmp/.tdoctor$$; mkdir -p "$PT"
snap() {
  awk -v L="$1" '/^Tcp:/ {if(!h){for(i=1;i<=NF;i++)n[i]=$i;h=1}else{for(i=1;i<=NF;i++) if(n[i]=="OutSegs"||n[i]=="RetransSegs") print "NS" L ":" n[i] "=" $i}}' /proc/net/snmp
  for f in rx_packets tx_packets rx_errors rx_dropped rx_missed_errors tx_errors tx_dropped tx_carrier_errors; do
    echo "IF$1:$f=$(cat /sys/class/net/$NIC/statistics/$f 2>/dev/null || echo 0)"
  done
  echo "QD$1:$(tc -s qdisc show dev $NIC 2>/dev/null | awk '/^qdisc/ {root = ($0 ~ / root /)} root && /dropped/ {gsub(/[(),]/, ""); for(i=1;i<=NF;i++) if($i=="dropped") d+=$(i+1)} END {print d+0}')"
}
echo "KERNEL:$(uname -r)"
echo "NIC:$NIC"
echo "NICSPEED:$(cat /sys/class/net/$NIC/speed 2>/dev/null || echo 0)"
snap 0
{{PROBES}}
wait
{{COLLECT}}
snap 1
rm -rf "$PT"
{{HAPROXY}}
`

// probesFor returns the background jobs for one address family.
func probesFor(fam netprobe.Family, addr string, controlPort int) (probes, collect string) {
	addr = netprobe.CanonicalAddr(addr)
	if addr == "" || netprobe.AddrFamily(addr) != fam {
		return "", ""
	}
	t := strconv.Itoa(int(fam))
	var p, c []string
	p = append(p, fmt.Sprintf(`( %s > "$PT/ping%s" 2>&1 ) &`, netprobe.PingCommand(fam, addr, PingCount, PingIntervalSec, 1, ""), t))
	c = append(c,
		fmt.Sprintf(`echo "PING%s:$(grep -E 'packets transmitted' "$PT/ping%s" | head -1)"`, t, t),
		fmt.Sprintf(`echo "PING%sRTT:$(grep -E '^(rtt|round-trip)' "$PT/ping%s" | head -1)"`, t, t),
	)

	if controlPort > 0 && controlPort < 65536 {
		p = append(p, fmt.Sprintf(`( for i in $(seq 1 %d); do s=$(date +%%s%%N); if timeout 3 bash -c 'exec 3<>/dev/tcp/$0/$1' '%s' %d 2>/dev/null; then e=$(date +%%s%%N); echo "TCP%s:$(( (e-s)/1000 ))"; else echo "TCP%s:-1"; fi; sleep 0.2; done > "$PT/tcp%s" ) &`,
			tcpAttempts, addr, controlPort, t, t, t))
		c = append(c, fmt.Sprintf(`cat "$PT/tcp%s" 2>/dev/null`, t))
	}

	sizes := mtuSizes4
	if fam == 6 {
		sizes = mtuSizes6
	}
	var list []string
	for _, s := range sizes {
		list = append(list, strconv.Itoa(s))
	}
	mtuCmd := netprobe.PingCommand(fam, addr, 2, 0.2, 1, "-M do -s SIZE")
	mtuCmd = strings.Replace(mtuCmd, "-q", "", 1)
	p = append(p, fmt.Sprintf(`( for s in %s; do r=$(%s 2>&1 | grep -oE '[0-9]+ received' | head -1); echo "MTU%s:$s:${r%%%% *}"; done > "$PT/mtu%s" ) &`,
		strings.Join(list, " "), strings.Replace(mtuCmd, "SIZE", "$s", 1), t, t))
	c = append(c, fmt.Sprintf(`cat "$PT/mtu%s" 2>/dev/null`, t))

	hopCmd := netprobe.PingCommand(fam, addr, hopProbes, 0.2, 1, "-t $t")
	hopCmd = strings.Replace(hopCmd, "-q", "", 1)
	p = append(p, fmt.Sprintf(`for t in $(seq 1 %d); do ( %s > "$PT/hop%s_$t" 2>&1 ) & done`, maxHops, hopCmd, t))
	c = append(c, fmt.Sprintf(`for t in $(seq 1 %d); do [ -f "$PT/hop%s_$t" ] && awk -v t=$t 'BEGIN{r=0;a="";d=0;rtt="";s=0} /^From / {r++; a=$2} /bytes from/ {d=1; r++; a=$4; sub(/:$/,"",a)} /^(rtt|round-trip)/ {split($4,x,"/"); rtt=x[2]} /packets transmitted/ {s=$1} END {printf "HOP%s:%%d:%%s:%%d:%%d:%%d:%%s\n", t, a, r, s, d, rtt}' "$PT/hop%s_$t"; done`,
		maxHops, t, t, t))
	return strings.Join(p, "\n"), strings.Join(c, "\n")
}

// buildScript fills the template; an unusable address simply drops that
// family.
func buildScript(p scriptParams) string {
	p6, c6 := probesFor(6, p.Peer6, p.ControlPort)
	p4, c4 := probesFor(4, p.Peer4, p.ControlPort)
	return strings.NewReplacer(
		"{{NICSETUP}}", tunnelmetrics.NICSetupScript(p.NIC),
		"{{PROBES}}", p6+"\n"+p4,
		"{{COLLECT}}", c6+"\n"+c4,
		"{{HAPROXY}}", tunnelmetrics.HAProxyScript(p.HASocket),
	).Replace(measureScript)
}

// parseEnd turns the script's output into an EndResult.
func parseEnd(role, out string) EndResult {
	end := EndResult{Role: role, Reachable: true}
	counters := map[string]map[string]int64{"NS0": {}, "NS1": {}, "IF0": {}, "IF1": {}, "QD0": {}, "QD1": {}}
	nic := &NICStats{}
	tcp := map[string]*TCPProbe{}
	mtu := map[string]*MTUResult{}
	hops := map[string]*HopPath{}
	pingStat, pingRTT := map[string]string{}, map[string]string{}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		tag, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch {
		case tag == "KERNEL":
			end.Kernel = val
		case tag == "NIC":
			nic.Name = val
		case tag == "NICSPEED":
			if v, err := strconv.Atoi(val); err == nil && v > 0 {
				nic.SpeedMbps = v
			}
		case tag == "PING6" || tag == "PING4":
			pingStat[tag[4:]] = val
		case tag == "PING6RTT" || tag == "PING4RTT":
			pingRTT[tag[4:5]] = val
		case tag == "TCP6" || tag == "TCP4":
			fam := "v" + tag[3:]
			pr := tcp[fam]
			if pr == nil {
				pr = &TCPProbe{Family: fam}
				tcp[fam] = pr
			}
			pr.Attempts++
			if us, err := strconv.ParseInt(val, 10, 64); err == nil && us >= 0 {
				ms := float64(us) / 1000
				pr.Successes++
				pr.AvgMs += ms
				if pr.Successes == 1 || ms < pr.MinMs {
					pr.MinMs = ms
				}
				if ms > pr.MaxMs {
					pr.MaxMs = ms
				}
			}
		case tag == "MTU6" || tag == "MTU4":
			fam := "v" + tag[3:]
			size, rest, _ := strings.Cut(val, ":")
			sz, err := strconv.Atoi(size)
			if err != nil {
				continue
			}
			r := mtu[fam]
			if r == nil {
				r = &MTUResult{Family: fam}
				mtu[fam] = r
			}
			got, _ := strconv.Atoi(strings.TrimSpace(rest))
			r.Probes = append(r.Probes, MTUProbe{Payload: sz, OK: got > 0})
		case tag == "HOP6" || tag == "HOP4":
			fam := "v" + tag[3:]
			f := strings.Split(val, ":")
			// The address may itself contain colons (IPv6): the numbers are the
			// last four fields, the first is the hop limit.
			if len(f) < 6 {
				continue
			}
			n := len(f)
			ttl, err1 := strconv.Atoi(f[0])
			replies, err2 := strconv.Atoi(f[n-4])
			sent, err3 := strconv.Atoi(f[n-3])
			reached, err4 := strconv.Atoi(f[n-2])
			if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
				continue
			}
			avg, _ := strconv.ParseFloat(f[n-1], 64)
			hp := hops[fam]
			if hp == nil {
				hp = &HopPath{Family: fam}
				hops[fam] = hp
			}
			hp.Hops = append(hp.Hops, Hop{TTL: ttl, Addr: strings.Join(f[1:n-4], ":"), Replies: replies, Sent: sent, Reached: reached == 1, AvgMs: avg})
		default:
			if m, ok := counters[tag]; ok {
				if k, v, ok := strings.Cut(val, "="); ok {
					m[k], _ = strconv.ParseInt(v, 10, 64)
				} else if tag == "QD0" || tag == "QD1" {
					m["dropped"], _ = strconv.ParseInt(val, 10, 64)
				}
			}
		}
	}

	for _, fam := range []string{"6", "4"} {
		stat, asked := pingStat[fam]
		if !asked {
			continue
		}
		p, ok := netprobe.ParsePing(stat + "\n" + pingRTT[fam])
		if !ok {
			p = netprobe.Ping{Sent: PingCount, LossPct: 100}
		}
		if fam == "6" {
			end.Ping.V6 = &p
		} else {
			end.Ping.V4 = &p
		}
	}
	for _, fam := range []string{"v6", "v4"} {
		if pr := tcp[fam]; pr != nil {
			if pr.Successes > 0 {
				pr.AvgMs /= float64(pr.Successes)
			}
			end.TCP = append(end.TCP, *pr)
		}
		if r := mtu[fam]; r != nil {
			finishMTU(r)
			end.MTU = append(end.MTU, *r)
		}
		if hp := hops[fam]; hp != nil {
			hp.Hops = trimHops(hp.Hops)
			end.Hops = append(end.Hops, *hp)
		}
	}

	d := func(group string, key string) int64 { return counters[group+"1"][key] - counters[group+"0"][key] }
	if len(counters["IF1"]) > 0 {
		nic.RxPackets, nic.TxPackets = d("IF", "rx_packets"), d("IF", "tx_packets")
		nic.RxErrors, nic.RxDropped, nic.RxMissed = d("IF", "rx_errors"), d("IF", "rx_dropped"), d("IF", "rx_missed_errors")
		nic.TxErrors, nic.TxDropped, nic.TxCarrier = d("IF", "tx_errors"), d("IF", "tx_dropped"), d("IF", "tx_carrier_errors")
		nic.QdiscDropped = d("QD", "dropped")
		nic.TotalQdiscDropped, nic.TotalTxPackets = counters["QD1"]["dropped"], counters["IF1"]["tx_packets"]
		end.NIC = nic
	}
	if len(counters["NS1"]) > 0 {
		ts := &TCPStats{
			OutSegs: d("NS", "OutSegs"), RetransSegs: d("NS", "RetransSegs"),
			TotalOutSegs: counters["NS1"]["OutSegs"], TotalRetrans: counters["NS1"]["RetransSegs"],
		}
		ts.RetransPct = pct(ts.RetransSegs, ts.OutSegs)
		ts.TotalRetransPct = pct(ts.TotalRetrans, ts.TotalOutSegs)
		end.TCPStat = ts
	}
	end.HAProxy = tunnelmetrics.ParseHAProxy(out)
	return end
}

func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

// finishMTU orders the probes largest first and works out the largest answered
// size and the path MTU it implies.
func finishMTU(r *MTUResult) {
	for i := 0; i < len(r.Probes); i++ {
		for j := i + 1; j < len(r.Probes); j++ {
			if r.Probes[j].Payload > r.Probes[i].Payload {
				r.Probes[i], r.Probes[j] = r.Probes[j], r.Probes[i]
			}
		}
	}
	for _, p := range r.Probes {
		if p.OK {
			r.LargestOK = p.Payload
			break
		}
	}
	if r.LargestOK > 0 {
		overhead := ipv4Overhead
		if r.Family == "v6" {
			overhead = ipv6Overhead
		}
		r.PathMTU = r.LargestOK + overhead
	}
}

// trimHops sorts hops by hop limit and drops everything after the first hop at
// which the destination itself answered.
func trimHops(hops []Hop) []Hop {
	for i := 0; i < len(hops); i++ {
		for j := i + 1; j < len(hops); j++ {
			if hops[j].TTL < hops[i].TTL {
				hops[i], hops[j] = hops[j], hops[i]
			}
		}
	}
	for i, h := range hops {
		if h.Reached {
			return hops[:i+1]
		}
	}
	return hops
}
