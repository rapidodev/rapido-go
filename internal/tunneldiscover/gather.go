package tunneldiscover

import (
	"context"
	"strings"

	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// Both scripts only read. Every section starts with a "###NAME" line so the
// output can be split without guessing; config files are introduced by
// "@@FILE <path>" and have their auth token masked before they leave the box.
const nodeScript = `
echo "###ROUTE4"; ip route show default 2>/dev/null
echo "###ROUTE6"; ip -6 route show default 2>/dev/null
echo "###ADDRS"; ip -o -4 addr show 2>/dev/null
echo "###IPTUNNEL"; ip tunnel show 2>/dev/null
echo "###WG"; wg show all dump 2>/dev/null
echo "###FRPC"
ps -eo args= 2>/dev/null | grep -E '(^|/)frpc( |$)' | grep -v grep | sed -nE 's/.*(-c|--config)[ =]([^ ]+).*/\2/p' | sort -u | while read -r f; do
  echo "@@FILE $f"; sed -E 's/(token *= *").*/\1***"/' "$f" 2>/dev/null
done
echo "###HAPROXY"
hp=$(ps -eo args= 2>/dev/null | grep -E '(^|/)haproxy( |$)' | grep -v grep | head -1 | sed -nE 's/.* -f ([^ ]+).*/\1/p')
if [ -z "$hp" ] && [ -f /etc/haproxy/haproxy.cfg ] && pgrep -x haproxy >/dev/null 2>&1; then hp=/etc/haproxy/haproxy.cfg; fi
if [ -n "$hp" ]; then echo "@@FILE $hp"; cat "$hp" 2>/dev/null; fi
echo "###END"
`

const relayScript = `
echo "###ROUTE4"; ip route show default 2>/dev/null
echo "###ROUTE6"; ip -6 route show default 2>/dev/null
echo "###IPTUNNEL"; ip tunnel show 2>/dev/null
echo "###WG"; wg show all dump 2>/dev/null
echo "###LISTEN"; ss -ltnpH 2>/dev/null
echo "###DNAT"; iptables -t nat -S 2>/dev/null
echo "###FRPS"
ps -eo args= 2>/dev/null | grep -E '(^|/)frps( |$)' | grep -v grep | sed -nE 's/.*(-c|--config)[ =]([^ ]+).*/\2/p' | sort -u | while read -r f; do
  echo "@@FILE $f"; sed -E 's/(token *= *").*/\1***"/' "$f" 2>/dev/null
done
echo "###END"
`

// sections splits script output on its "###NAME" lines.
func sections(out string) map[string]string {
	res := map[string]string{}
	var name string
	var b strings.Builder
	flush := func() {
		if name != "" {
			res[name] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "###") {
			flush()
			name = strings.TrimPrefix(line, "###")
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return res
}

// files splits a section into the config files it carries.
func files(section string) []ConfigFile {
	var res []ConfigFile
	var cur *ConfigFile
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "@@FILE ") {
			res = append(res, ConfigFile{Path: strings.TrimSpace(strings.TrimPrefix(line, "@@FILE "))})
			cur = &res[len(res)-1]
			continue
		}
		if cur != nil {
			cur.Content += line + "\n"
		}
	}
	return res
}

func parseNodeFacts(out string) NodeFacts {
	s := sections(out)
	n := NodeFacts{
		Route4: s["ROUTE4"], Route6: s["ROUTE6"], Addrs: s["ADDRS"], IPTunnel: s["IPTUNNEL"], WGDump: s["WG"],
		FRPC: files(s["FRPC"]),
	}
	if hp := files(s["HAPROXY"]); len(hp) > 0 {
		n.HAProxy = &hp[0]
	}
	return n
}

func parseRelayFacts(out string) RelayFacts {
	s := sections(out)
	return RelayFacts{
		Route4: s["ROUTE4"], Route6: s["ROUTE6"], IPTunnel: s["IPTUNNEL"], WGDump: s["WG"],
		Listen: s["LISTEN"], DNAT: s["DNAT"], FRPS: files(s["FRPS"]),
	}
}

// ReadNode reads the node's side.
func ReadNode(ctx context.Context, node *sshexec.Client) (NodeFacts, error) {
	out, err := node.Run(ctx, nodeScript)
	if err != nil {
		return NodeFacts{}, err
	}
	return parseNodeFacts(out), nil
}

// ReadRelay reads the relay's side.
func ReadRelay(ctx context.Context, relay *sshexec.Client) (RelayFacts, error) {
	out, err := relay.Run(ctx, relayScript)
	if err != nil {
		return RelayFacts{}, err
	}
	return parseRelayFacts(out), nil
}

// Discover reads the node and, when it has a login for it, the relay, and
// returns the verdict. relay may be nil: the node alone usually reveals the
// protocol and the relay's address, which is what is needed to ask the admin
// for the relay's login.
func Discover(ctx context.Context, node, relay *sshexec.Client) (Result, error) {
	nf, err := ReadNode(ctx, node)
	if err != nil {
		return Result{}, err
	}
	var rf *RelayFacts
	if relay != nil {
		f, err := ReadRelay(ctx, relay)
		if err != nil {
			return Result{}, err
		}
		rf = &f
	}
	return Analyze(nf, rf), nil
}
