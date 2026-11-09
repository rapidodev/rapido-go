package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/legendary1205/rapido-go/internal/xrayimport"
)

// productionShapedCore is a document of the shape production runs: one VLESS
// inbound on many ports, about 50 direct exits each pinned to its own tunnel
// with a direct fallback, one port-based routing rule per exit, and plain udp
// DNS servers.
func productionShapedCore() (coreConfigDTO, []xrayimport.ExportInbound) {
	const exits = 50
	core := coreConfigDTO{LogLevel: "warn", SniffEnabled: true}
	var ports []int
	for i := range exits {
		port := 20000 + i*4
		ports = append(ports, port)
		tag := fmt.Sprintf("exit-%02d", i)
		core.Outbounds = append(core.Outbounds, outboundDTO{
			Tag: tag, Type: "direct", BindInterface: fmt.Sprintf("wg-%02d", i), DirectFallback: true,
		})
		core.RoutingRules = append(core.RoutingRules, routingRuleDTO{
			Inbound: []string{"main"}, InboundPort: []int{port}, OutboundTag: tag,
		})
	}
	core.DNSServers = []dnsServerDTO{
		{Tag: "dns-1", Type: "udp", Address: "1.1.1.1"},
		{Tag: "dns-2", Type: "udp", Address: "8.8.8.8"},
	}
	inbounds := []xrayimport.ExportInbound{{
		Tag: "main", Protocol: "vless", Network: "tcp", Security: "none",
		Port: int32(ports[0]), Ports: ports,
		Clients: []xrayimport.ExportClient{{Email: "alice", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-0000000000a1"}},
	}}
	return core, inbounds
}

func productionShapedExport(t *testing.T) []byte {
	t.Helper()
	core, inbounds := productionShapedCore()
	raw, err := xrayimport.BuildXrayJSON(inbounds, coreOutboundsToExport(core.Outbounds),
		coreRoutingRulesToExport(core.RoutingRules), coreDNSServersToExport(core.DNSServers), core.LogLevel, core.SniffEnabled)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The outbound fields for other providers' servers must not change a byte of a
// document that uses none of them: not as stored, not as Xray JSON, and not in
// what the nodes are sent. The production preflight compares exactly these.
func TestProductionShapedDocumentConvertsExactlyAsBefore(t *testing.T) {
	core, _ := productionShapedCore()
	if msg := validateCoreConfig(core); msg != "" {
		t.Fatalf("the document is refused: %s", msg)
	}

	// Stored: each outbound keeps its four keys and nothing else, and a
	// decode and encode gives back the same bytes.
	for i, ob := range core.Outbounds {
		got, _ := json.Marshal(ob)
		want := fmt.Sprintf(`{"tag":"exit-%02d","type":"direct","bind_interface":"wg-%02d","direct_fallback":true}`, i, i)
		if string(got) != want {
			t.Fatalf("outbound %d stores as %s, want %s", i, got, want)
		}
	}
	for name, section := range map[string]any{"outbounds": core.Outbounds, "routing_rules": core.RoutingRules, "dns_servers": core.DNSServers} {
		first, _ := json.Marshal(section)
		decoded := reflect.New(reflect.TypeOf(section))
		if err := json.Unmarshal(first, decoded.Interface()); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(decoded.Elem().Interface())
		if !bytes.Equal(first, again) {
			t.Errorf("%s does not round-trip:\n%s\n%s", name, first, again)
		}
	}

	// As Xray JSON: byte for byte what the code before these fields exported
	// (testdata/production_shaped_export.json was written by it).
	export := productionShapedExport(t)
	golden, err := os.ReadFile(filepath.Join("testdata", "production_shaped_export.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may have turned its line ends into CRLF.
	golden = bytes.ReplaceAll(golden, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(bytes.TrimSpace(export), bytes.TrimSpace(golden)) {
		t.Errorf("the Xray export changed:\n%s", export)
	}

	// And back: the export parses into the same outbounds, which export again
	// to the same JSON.
	parsed, err := xrayimport.ParseXrayConfig(export)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Outbounds) != len(core.Outbounds) {
		t.Fatalf("parsed %d outbounds, want %d", len(parsed.Outbounds), len(core.Outbounds))
	}
	for i, ob := range parsed.Outbounds {
		want := xrayimport.Outbound{Tag: core.Outbounds[i].Tag, Type: "direct", BindInterface: core.Outbounds[i].BindInterface}
		if !reflect.DeepEqual(ob, want) {
			t.Errorf("outbound %d parsed as %+v, want %+v", i, ob, want)
		}
	}
	var doc, reDoc struct {
		Outbounds json.RawMessage `json:"outbounds"`
	}
	reExport, err := xrayimport.BuildXrayJSON(nil, parsed.Outbounds, nil, nil, "warn", false)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(export, &doc) != nil || json.Unmarshal(reExport, &reDoc) != nil {
		t.Fatal("export is not JSON")
	}
	if !jsonEqual(t, doc.Outbounds, reDoc.Outbounds) {
		t.Errorf("outbounds re-export differently:\n%s\n%s", doc.Outbounds, reDoc.Outbounds)
	}

	// To the nodes: every agent, old or new, is sent the same core.
	before, _ := json.Marshal(nodeConfigSchema{compact: true, delta: true, extendedRules: true}.outboundsFor(core))
	for _, schema := range []nodeConfigSchema{{}, {compact: true, delta: true, extendedRules: true, streamOutbounds: true}} {
		after, _ := json.Marshal(schema.outboundsFor(core))
		if !bytes.Equal(before, after) {
			t.Errorf("schema %+v is sent a different core", schema)
		}
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// What the production preflight does against a throwaway panel: save today's
// live document and read it back, requiring the three core sections unchanged.
func TestProductionShapedDocumentSavesAndReadsBackUnchanged(t *testing.T) {
	router, token := newTestRouter(t)
	core, _ := productionShapedCore()
	doc := map[string]any{
		"log_level": core.LogLevel, "sniff_enabled": core.SniffEnabled,
		"outbounds": core.Outbounds, "routing_rules": core.RoutingRules, "dns_servers": core.DNSServers,
		"inbounds": []map[string]any{{"tag": "main", "protocol": "vless", "network": "tcp", "security": "none"}},
	}
	if resp := doRequest(t, router, "PUT", "/api/settings/xray-config", token, doc); resp.Code != http.StatusOK {
		t.Fatalf("save: %d %s", resp.Code, resp.Raw)
	}
	resp := doRequest(t, router, "GET", "/api/settings/xray-config", token, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("read: %d %s", resp.Code, resp.Raw)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(resp.Raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"outbounds", "routing_rules", "dns_servers"} {
		want, _ := json.Marshal(doc[key])
		if !bytes.Equal(bytes.TrimSpace(got[key]), want) {
			t.Errorf("%s came back different:\n%s\n%s", key, want, got[key])
		}
	}
}
