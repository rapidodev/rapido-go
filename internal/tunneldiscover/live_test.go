package tunneldiscover

import (
	"os"
	"path/filepath"
	"testing"
)

// These two tests are developer tools, skipped unless their env var is set:
// DUMP_SCRIPTS_DIR writes the two read-only scripts to files so they can be
// run on a real server, and LIVE_NODE_OUT / LIVE_RELAY_OUT feed what they
// printed back through the analysis.

func TestDumpScripts(t *testing.T) {
	dir := os.Getenv("DUMP_SCRIPTS_DIR")
	if dir == "" {
		t.Skip("DUMP_SCRIPTS_DIR not set")
	}
	_ = os.WriteFile(filepath.Join(dir, "node.sh"), []byte(nodeScript), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "relay.sh"), []byte(relayScript), 0o644)
}

func TestLiveOutputs(t *testing.T) {
	nodeOut := os.Getenv("LIVE_NODE_OUT")
	if nodeOut == "" {
		t.Skip("LIVE_NODE_OUT not set")
	}
	raw, err := os.ReadFile(nodeOut)
	if err != nil {
		t.Fatal(err)
	}
	var relay *RelayFacts
	if p := os.Getenv("LIVE_RELAY_OUT"); p != "" {
		rraw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f := parseRelayFacts(string(rraw))
		relay = &f
	}
	res := Analyze(parseNodeFacts(string(raw)), relay)
	t.Logf("found=%v method=%q control=%d ports=%v iface=%q relayHost=%q", res.Found, res.Method, res.ControlPort, res.Ports, res.Interface, res.RelayHost)
	t.Logf("protocol=%q relayAddrs=%v nodeNIC=%q relayNIC=%q notes=%v", res.Details.Protocol, res.Details.RelayAddrs, res.Details.NodeNIC, res.Details.RelayNIC, res.Details.Notes)
	if res.Details.HAProxy != nil {
		t.Logf("haproxy socket=%q frontend=%q servers=%+v", res.Details.HAProxy.Socket, res.Details.HAProxy.Frontend, res.Details.HAProxy.Servers)
	}
	if res.Details.FRP != nil {
		t.Logf("frp=%+v", *res.Details.FRP)
	}
}
