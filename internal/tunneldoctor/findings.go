package tunneldoctor

import (
	"fmt"
	"strings"

	"github.com/legendary1205/rapido-go/internal/netprobe"
)

// BuildFindings lists what the measurements show. Every sentence is built from
// numbers in the Result; none asserts a cause.
func BuildFindings(r *Result) []Finding {
	var out []Finding
	add := func(code, sev, title, detail, titleFA, detailFA string) {
		out = append(out, Finding{Code: code, Severity: sev, Title: title, Detail: detail, TitleFA: titleFA, DetailFA: detailFA})
	}

	for _, end := range []*EndResult{&r.Node, &r.Relay} {
		if !end.Reachable {
			add("unreachable_"+end.Role, SevCritical,
				fmt.Sprintf("The %s could not be measured", end.Role),
				fmt.Sprintf("The panel could not run the measurements on the %s over SSH: %s", end.Role, orNone(end.Error)),
				fmt.Sprintf("اندازه‌گیری روی %s ممکن نبود", faEnd(end.Role)),
				fmt.Sprintf("پنل نتوانست از طریق SSH روی %s اندازه‌گیری را اجرا کند: %s", faEnd(end.Role), orNone(end.Error)))
		}
	}

	for _, end := range []*EndResult{&r.Node, &r.Relay} {
		if !end.Reachable {
			continue
		}
		dirEN, dirFA := directionEN(end.Role), directionFA(end.Role)
		for _, fam := range []struct {
			name string
			p    *netprobe.Ping
		}{{"IPv6", end.Ping.V6}, {"IPv4", end.Ping.V4}} {
			p := fam.p
			if p == nil {
				continue
			}
			code := fmt.Sprintf("%s_%s", end.Role, strings.ToLower(fam.name))
			if p.LossPct > 0 {
				sev := SevInfo
				if p.LossPct >= 1 {
					sev = SevWarning
				}
				if p.LossPct >= 20 {
					sev = SevCritical
				}
				detail := fmt.Sprintf("%d of %d probes (0.2 s apart) got no reply, %.1f%% loss.", p.Sent-p.Received, p.Sent, p.LossPct)
				detailFA := fmt.Sprintf("%d پروب از %d پروب (با فاصلهٔ ۰٫۲ ثانیه) پاسخی نگرفت؛ افت بسته %.1f٪.", p.Sent-p.Received, p.Sent, p.LossPct)
				if p.Received > 0 {
					detail += fmt.Sprintf(" Replies: min %.2f / avg %.2f / max %.2f ms.", p.MinMs, p.AvgMs, p.MaxMs)
					detailFA += fmt.Sprintf(" پاسخ‌ها: min %.2f / avg %.2f / max %.2f ms.", p.MinMs, p.AvgMs, p.MaxMs)
				}
				add("loss_"+code, sev,
					fmt.Sprintf("Packet loss %s over %s", dirEN, fam.name),
					detail,
					fmt.Sprintf("افت بسته %s روی %s", dirFA, fam.name), detailFA)
			}
			if p.Received >= 10 && p.MaxMs > 3*p.AvgMs && p.MaxMs-p.AvgMs > 20 {
				add("spike_"+code, SevInfo,
					fmt.Sprintf("Latency spikes %s over %s", dirEN, fam.name),
					fmt.Sprintf("The slowest reply took %.1f ms against an average of %.1f ms (min %.1f ms, standard deviation %.2f ms).", p.MaxMs, p.AvgMs, p.MinMs, p.JitterMs),
					fmt.Sprintf("جهش تأخیر %s روی %s", dirFA, fam.name),
					fmt.Sprintf("کندترین پاسخ %.1f ms بود در حالی که میانگین %.1f ms است (min %.1f ms، انحراف معیار %.2f ms).", p.MaxMs, p.AvgMs, p.MinMs, p.JitterMs))
			}
		}

		for _, m := range end.MTU {
			standard := 1472
			if m.Family == "v6" {
				standard = 1452
			}
			pingWorks := (m.Family == "v6" && end.Ping.V6 != nil && end.Ping.V6.Received > 0) || (m.Family == "v4" && end.Ping.V4 != nil && end.Ping.V4.Received > 0)
			if !pingWorks || m.LargestOK >= standard {
				continue
			}
			fam := "IPv" + m.Family[1:]
			if m.LargestOK == 0 {
				add("mtu_"+end.Role+"_"+m.Family, SevWarning,
					fmt.Sprintf("No don't-fragment size was answered %s over %s", dirEN, fam),
					fmt.Sprintf("Plain pings are answered, but pings with the don't-fragment bit set got no reply at any tested payload size (%s bytes).", intsList(m)),
					fmt.Sprintf("هیچ اندازهٔ بدون‌تکه‌شدن پاسخ داده نشد %s روی %s", dirFA, fam),
					fmt.Sprintf("پینگ ساده پاسخ می‌گیرد، اما پینگ با بیت don't-fragment در هیچ‌یک از اندازه‌های آزمایش‌شده (%s بایت) پاسخی نگرفت.", intsList(m)))
				continue
			}
			add("mtu_"+end.Role+"_"+m.Family, SevWarning,
				fmt.Sprintf("Smaller path MTU %s over %s", dirEN, fam),
				fmt.Sprintf("The largest don't-fragment payload that was answered is %d bytes (packet size %d); a standard 1500-byte path answers %d. Sizes tried: %s.", m.LargestOK, m.PathMTU, standard, intsList(m)),
				fmt.Sprintf("MTU کوچک‌تر در مسیر %s روی %s", dirFA, fam),
				fmt.Sprintf("بزرگ‌ترین payload بدون‌تکه‌شدن که پاسخ گرفت %d بایت است (اندازهٔ بسته %d)؛ مسیر استاندارد ۱۵۰۰ بایتی تا %d بایت پاسخ می‌دهد. اندازه‌های آزمایش‌شده: %s.", m.LargestOK, m.PathMTU, standard, intsList(m)))
		}
	}

	// TCP connects (node to relay).
	for _, t := range r.Node.TCP {
		fam := "IPv" + t.Family[1:]
		if t.Successes < t.Attempts {
			add("tcp_"+t.Family, SevWarning,
				fmt.Sprintf("TCP connects to the relay control port failed over %s", fam),
				fmt.Sprintf("%d of %d TCP connects to port %d got no answer within 3 s.", t.Attempts-t.Successes, t.Attempts, t.Port),
				fmt.Sprintf("اتصال TCP به پورت کنترل رله روی %s ناموفق بود", fam),
				fmt.Sprintf("%d اتصال TCP از %d اتصال به پورت %d در ۳ ثانیه پاسخ نگرفت.", t.Attempts-t.Successes, t.Attempts, t.Port))
			continue
		}
		var icmp *netprobe.Ping
		if t.Family == "v6" {
			icmp = r.Node.Ping.V6
		} else {
			icmp = r.Node.Ping.V4
		}
		if icmp != nil && icmp.Received > 0 && t.AvgMs > 2*icmp.AvgMs && t.AvgMs-icmp.AvgMs > 10 {
			add("tcp_slow_"+t.Family, SevInfo,
				fmt.Sprintf("TCP connect is slower than ping over %s", fam),
				fmt.Sprintf("A TCP connect to port %d took %.1f ms on average (min %.1f, max %.1f) while the ICMP round trip is %.1f ms.", t.Port, t.AvgMs, t.MinMs, t.MaxMs, icmp.AvgMs),
				fmt.Sprintf("اتصال TCP کندتر از پینگ است (%s)", fam),
				fmt.Sprintf("اتصال TCP به پورت %d به‌طور میانگین %.1f ms طول کشید (min %.1f، max %.1f) در حالی که RTT پینگ %.1f ms است.", t.Port, t.AvgMs, t.MinMs, t.MaxMs, icmp.AvgMs))
		}
	}

	// Network cards and TCP.
	for _, end := range []*EndResult{&r.Node, &r.Relay} {
		if !end.Reachable {
			continue
		}
		if n := end.NIC; n != nil {
			during := n.RxErrors + n.RxDropped + n.RxMissed + n.TxErrors + n.TxDropped + n.TxCarrier + n.QdiscDropped
			if during > 0 {
				add("nic_"+end.Role, SevWarning,
					fmt.Sprintf("The %s network card counted drops or errors during the test", end.Role),
					fmt.Sprintf("Card %s, counter changes during the ~25 s test: rx errors %d, rx dropped %d, rx missed %d, tx errors %d, tx dropped %d, tx carrier errors %d, queue (qdisc) drops %d.",
						n.Name, n.RxErrors, n.RxDropped, n.RxMissed, n.TxErrors, n.TxDropped, n.TxCarrier, n.QdiscDropped),
					fmt.Sprintf("کارت شبکهٔ %s در زمان تست drop یا خطا شمرد", faEnd(end.Role)),
					fmt.Sprintf("کارت %s، تغییر شمارنده‌ها در ~۲۵ ثانیهٔ تست: rx errors %d، rx dropped %d، rx missed %d، tx errors %d، tx dropped %d، tx carrier %d، drop صف (qdisc) %d.",
						n.Name, n.RxErrors, n.RxDropped, n.RxMissed, n.TxErrors, n.TxDropped, n.TxCarrier, n.QdiscDropped))
			} else if n.TotalQdiscDropped > 0 && n.TotalTxPackets > 0 {
				add("nic_history_"+end.Role, SevInfo,
					fmt.Sprintf("The %s card has dropped packets in its queue since boot", end.Role),
					fmt.Sprintf("Card %s: %d queue (qdisc) drops against %d transmitted packets since boot (%.4f%%). None during the test.", n.Name, n.TotalQdiscDropped, n.TotalTxPackets, pct(n.TotalQdiscDropped, n.TotalTxPackets)),
					fmt.Sprintf("کارت %s از زمان بوت در صف خود بسته drop کرده است", faEnd(end.Role)),
					fmt.Sprintf("کارت %s: %d drop صف (qdisc) در مقابل %d بستهٔ ارسالی از زمان بوت (%.4f٪). در زمان تست هیچ drop ای نبود.", n.Name, n.TotalQdiscDropped, n.TotalTxPackets, pct(n.TotalQdiscDropped, n.TotalTxPackets)))
			}
		}
		if t := end.TCPStat; t != nil {
			switch {
			case t.OutSegs >= 1000 && t.RetransPct >= 1:
				add("retrans_"+end.Role, SevWarning,
					fmt.Sprintf("TCP retransmissions on the %s during the test", end.Role),
					fmt.Sprintf("%d of %d TCP segments sent during the test were retransmissions (%.2f%%). Since boot: %.2f%% (%d of %d).", t.RetransSegs, t.OutSegs, t.RetransPct, t.TotalRetransPct, t.TotalRetrans, t.TotalOutSegs),
					fmt.Sprintf("ارسال مجدد TCP روی %s در زمان تست", faEnd(end.Role)),
					fmt.Sprintf("%d از %d سگمنت TCP ارسالی در زمان تست ارسال مجدد بود (%.2f٪). از زمان بوت: %.2f٪ (%d از %d).", t.RetransSegs, t.OutSegs, t.RetransPct, t.TotalRetransPct, t.TotalRetrans, t.TotalOutSegs))
			case t.TotalRetransPct >= 1 && t.TotalOutSegs >= 100000:
				add("retrans_history_"+end.Role, SevInfo,
					fmt.Sprintf("TCP retransmissions on the %s since boot", end.Role),
					fmt.Sprintf("%.2f%% of the TCP segments sent since boot were retransmissions (%d of %d); during the test: %.2f%% (%d of %d).", t.TotalRetransPct, t.TotalRetrans, t.TotalOutSegs, t.RetransPct, t.RetransSegs, t.OutSegs),
					fmt.Sprintf("ارسال مجدد TCP روی %s از زمان بوت", faEnd(end.Role)),
					fmt.Sprintf("%.2f٪ از سگمنت‌های TCP ارسالی از زمان بوت ارسال مجدد بود (%d از %d)؛ در زمان تست: %.2f٪ (%d از %d).", t.TotalRetransPct, t.TotalRetrans, t.TotalOutSegs, t.RetransPct, t.RetransSegs, t.OutSegs))
			}
		}
	}

	// HAProxy failover state on the node.
	if ha := r.Node.HAProxy; ha != nil && ha.Present {
		var primaryDown, onBackup []string
		var primarySessions, backupSessions int64
		for _, s := range ha.Servers {
			if s.Backup {
				backupSessions += s.Sessions
			} else {
				primarySessions += s.Sessions
			}
			if !s.Backup && !strings.HasPrefix(strings.ToUpper(s.Status), "UP") {
				primaryDown = append(primaryDown, fmt.Sprintf("%s (%s)", s.Name, s.Status))
			}
			if s.Backup && s.Sessions > 0 {
				onBackup = append(onBackup, fmt.Sprintf("%s: %d sessions", s.Name, s.Sessions))
			}
		}
		if len(primaryDown) > 0 {
			add("ha_primary_down", SevCritical,
				"The primary relay path is down in HAProxy",
				"HAProxy on the node reports the primary server as "+strings.Join(primaryDown, ", ")+".",
				"مسیر اصلی رله در HAProxy پایین است",
				"HAProxy روی نود سرور اصلی را به این صورت گزارش می‌کند: "+strings.Join(primaryDown, "، ")+".")
		} else if len(onBackup) > 0 && backupSessions > primarySessions {
			add("ha_on_backup", SevWarning,
				"Traffic is on the backup relay path",
				"HAProxy on the node has sessions on the backup server ("+strings.Join(onBackup, ", ")+") while the primary is up, and more of them than on the primary ("+fmt.Sprint(primarySessions)+").",
				"ترافیک روی مسیر پشتیبان رله است",
				"HAProxy روی نود روی سرور پشتیبان نشست دارد ("+strings.Join(onBackup, "، ")+") در حالی که سرور اصلی بالا است و تعداد آن‌ها از نشست‌های سرور اصلی ("+fmt.Sprint(primarySessions)+") بیشتر است.")
		}
	}

	// Hop-by-hop.
	for _, end := range []*EndResult{&r.Node, &r.Relay} {
		for _, hp := range end.Hops {
			if len(hp.Hops) == 0 {
				continue
			}
			fam := "IPv" + hp.Family[1:]
			last := hp.Hops[len(hp.Hops)-1]
			if last.Reached {
				add("hops_"+end.Role+"_"+hp.Family, SevInfo,
					fmt.Sprintf("Hop count %s over %s", directionEN(end.Role), fam),
					fmt.Sprintf("The destination first answered with hop limit %d (hops that answered with their own address: %d).", last.TTL, answeredHops(hp.Hops)),
					fmt.Sprintf("تعداد hop %s روی %s", directionFA(end.Role), fam),
					fmt.Sprintf("مقصد برای اولین بار با hop limit برابر %d پاسخ داد (تعداد hop هایی که با آدرس خودشان پاسخ دادند: %d).", last.TTL, answeredHops(hp.Hops)))
			} else {
				add("hops_unreached_"+end.Role+"_"+hp.Family, SevInfo,
					fmt.Sprintf("The destination did not answer within %d hops %s over %s", maxHops, directionEN(end.Role), fam),
					fmt.Sprintf("Probes with hop limits 1 to %d: %d hops answered with their own address, the destination did not answer.", maxHops, answeredHops(hp.Hops)),
					fmt.Sprintf("مقصد در %d hop %s روی %s پاسخ نداد", maxHops, directionFA(end.Role), fam),
					fmt.Sprintf("پروب با hop limit از ۱ تا %d: %d hop با آدرس خودشان پاسخ دادند و مقصد پاسخ نداد.", maxHops, answeredHops(hp.Hops)))
			}
		}
	}

	out = append(out, historyFindings(r)...)
	return out
}

func historyFindings(r *Result) []Finding {
	h := r.History
	var out []Finding
	add := func(code, sev, title, detail, titleFA, detailFA string) {
		out = append(out, Finding{Code: code, Severity: sev, Title: title, Detail: detail, TitleFA: titleFA, DetailFA: detailFA})
	}
	if h.Samples == 0 {
		return out
	}
	window := h.WindowHours
	if h.OutageCount > 0 {
		sev := SevWarning
		if h.DowntimeSecond >= 300 {
			sev = SevCritical
		}
		add("outages", sev,
			fmt.Sprintf("The relay stopped answering %d time(s) in %d h", h.OutageCount, window),
			fmt.Sprintf("%d outage(s), %d s in total, the longest %d s.", h.OutageCount, h.DowntimeSecond, h.LongestOutage),
			fmt.Sprintf("رله %d بار در %d ساعت پاسخ نداد", h.OutageCount, window),
			fmt.Sprintf("%d قطعی، مجموعاً %d ثانیه، طولانی‌ترین %d ثانیه.", h.OutageCount, h.DowntimeSecond, h.LongestOutage))
	}
	if h.Failovers > 0 {
		add("failovers", SevWarning,
			fmt.Sprintf("Traffic failed over to the backup path %d time(s) in %d h", h.Failovers, window),
			fmt.Sprintf("%d failover event(s) and %d failback event(s) were recorded.", h.Failovers, h.Failbacks),
			fmt.Sprintf("ترافیک %d بار در %d ساعت به مسیر پشتیبان منتقل شد", h.Failovers, window),
			fmt.Sprintf("%d رویداد failover و %d رویداد failback ثبت شد.", h.Failovers, h.Failbacks))
	}
	p := h.Plateau
	if p.Detected {
		detail := fmt.Sprintf("%s: ", p.Label)
		detailFA := fmt.Sprintf("%s: ", p.Label)
		if p.Windows > 0 {
			detail += fmt.Sprintf("%d of %d consecutive 20-minute windows were flat within 5%% around %.0f Mbit/s (about %d minutes in total). ", p.Windows, p.WindowsTotal, p.LevelMbps, p.FlatMinutes)
			detailFA += fmt.Sprintf("%d پنجرهٔ ۲۰ دقیقه‌ای از %d پنجرهٔ پیاپی با تلورانس ۵٪ روی حدود %.0f Mbit/s ثابت بود (جمعاً حدود %d دقیقه). ", p.Windows, p.WindowsTotal, p.LevelMbps, p.FlatMinutes)
		}
		detail += fmt.Sprintf("Of the samples above half the maximum, %.0f%% lie within 5%% of the maximum.", p.NearMaxShare*100)
		detailFA += fmt.Sprintf("از نمونه‌های بالای نصف بیشینه، %.0f٪ در ۵٪ بیشینه قرار دارند.", p.NearMaxShare*100)
		if p.CeilingEvents > 0 {
			detail += fmt.Sprintf(" The panel also recorded %d ceiling event(s).", p.CeilingEvents)
			detailFA += fmt.Sprintf(" پنل همچنین %d رویداد سقف پهنای باند ثبت کرده است.", p.CeilingEvents)
		}
		speed := r.Environment.RelayNICSpeed
		if strings.HasPrefix(p.Series, "node") {
			speed = r.Environment.NodeNICSpeed
		}
		if speed > 0 && p.LevelMbps > 0 {
			detail += fmt.Sprintf(" The card reports a link speed of %d Mbit/s (the flat level is %.0f%% of it).", speed, 100*p.LevelMbps/float64(speed))
			detailFA += fmt.Sprintf(" کارت سرعت لینک %d Mbit/s را گزارش می‌کند (سطح ثابت %.0f٪ آن است).", speed, 100*p.LevelMbps/float64(speed))
		}
		add("plateau", SevWarning, "Traffic is pinned flat at one level", detail, "ترافیک روی یک سطح ثابت گیر کرده است", detailFA)
	}
	if lo, hi := lossBands(h.LossByRate); lo != nil && hi != nil {
		add("loss_vs_rate", SevWarning,
			"Packet loss is higher when more traffic flows",
			fmt.Sprintf("Mean ping loss was %.2f%% at %s and %.2f%% at %s (relay card load, %d and %d samples).", lo.loss, bandLabel(lo.band), hi.loss, bandLabel(hi.band), lo.band.Samples, hi.band.Samples),
			"افت بسته در ترافیک بالاتر بیشتر است",
			fmt.Sprintf("میانگین افت پینگ در بار %s برابر %.2f٪ و در بار %s برابر %.2f٪ بود (بار کارت رله، %d و %d نمونه).", bandLabel(lo.band), lo.loss, bandLabel(hi.band), hi.loss, lo.band.Samples, hi.band.Samples))
	}
	if h.V6LossPct != nil && *h.V6LossPct > 0 || h.V4LossPct != nil && *h.V4LossPct > 0 {
		var en, fa []string
		if h.V6LossPct != nil {
			en = append(en, fmt.Sprintf("IPv6 mean loss %.3f%% (loss in %.1f%% of samples)", *h.V6LossPct, 100**h.V6LossyShare))
			fa = append(fa, fmt.Sprintf("IPv6 میانگین افت %.3f٪ (افت در %.1f٪ نمونه‌ها)", *h.V6LossPct, 100**h.V6LossyShare))
		}
		if h.V4LossPct != nil {
			en = append(en, fmt.Sprintf("IPv4 mean loss %.3f%% (loss in %.1f%% of samples)", *h.V4LossPct, 100**h.V4LossyShare))
			fa = append(fa, fmt.Sprintf("IPv4 میانگین افت %.3f٪ (افت در %.1f٪ نمونه‌ها)", *h.V4LossPct, 100**h.V4LossyShare))
		}
		add("history_loss", SevInfo,
			fmt.Sprintf("Packet loss over the last %d h", window),
			strings.Join(en, "; ")+fmt.Sprintf(", from %d one-minute 10-ping samples.", h.Samples),
			fmt.Sprintf("افت بسته در %d ساعت گذشته", window),
			strings.Join(fa, "؛ ")+fmt.Sprintf("، از %d نمونهٔ یک‌دقیقه‌ای ۱۰ پینگی.", h.Samples))
	}
	return out
}

type lossBand struct {
	band RateBand
	loss float64
}

// lossBands compares the quietest and the busiest load band that have enough
// samples: it returns both only when the busy one's loss is at least double
// and above 0.5 percentage points over the quiet one's.
func lossBands(bands []RateBand) (lo, hi *lossBand) {
	pick := func(b RateBand) (float64, bool) {
		var sum float64
		var n int
		for _, v := range []*float64{b.V6LossPct, b.V4LossPct} {
			if v != nil {
				sum += *v
				n++
			}
		}
		if n == 0 {
			return 0, false
		}
		return sum / float64(n), true
	}
	var usable []lossBand
	for _, b := range bands {
		if b.Samples < 10 {
			continue
		}
		if l, ok := pick(b); ok {
			usable = append(usable, lossBand{b, l})
		}
	}
	if len(usable) < 2 {
		return nil, nil
	}
	first, last := usable[0], usable[len(usable)-1]
	if last.loss > 0.5 && last.loss >= 2*first.loss+0.25 {
		return &first, &last
	}
	return nil, nil
}

func bandLabel(b RateBand) string {
	switch {
	case b.FromMbps == 0:
		return fmt.Sprintf("under %.0f Mbit/s", b.ToMbps)
	case b.ToMbps == 0:
		return fmt.Sprintf("over %.0f Mbit/s", b.FromMbps)
	}
	return fmt.Sprintf("%.0f-%.0f Mbit/s", b.FromMbps, b.ToMbps)
}

func answeredHops(h []Hop) int {
	n := 0
	for _, x := range h {
		if x.Replies > 0 && x.Addr != "" && !x.Reached {
			n++
		}
	}
	return n
}

func intsList(m MTUResult) string {
	var s []string
	for _, p := range m.Probes {
		s = append(s, fmt.Sprint(p.Payload))
	}
	return strings.Join(s, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "no detail"
	}
	return s
}

func directionEN(role string) string {
	if role == "node" {
		return "from the node to the relay"
	}
	return "from the relay to the node"
}

func directionFA(role string) string {
	if role == "node" {
		return "از نود به رله"
	}
	return "از رله به نود"
}

func faEnd(role string) string {
	if role == "node" {
		return "نود"
	}
	return "رله"
}
