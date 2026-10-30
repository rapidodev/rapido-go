package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin/binding"
)

// serverVerdictOnDocument runs a document through everything PUT
// /api/settings/xray-config checks before it writes, in the same order: the
// binding, the core config, the inbound entries, and whether each rule's
// inbound exists once the document's own inbounds are in place.
func serverVerdictOnDocument(raw []byte) string {
	var dto xrayConfigDTO
	if err := binding.JSON.BindBody(raw, &dto); err != nil {
		return err.Error()
	}
	core := coreConfigDTO{
		LogLevel: dto.LogLevel, SniffEnabled: dto.SniffEnabled,
		Outbounds: dto.Outbounds, RoutingRules: dto.RoutingRules, DNSServers: dto.DNSServers,
	}
	if msg := validateCoreConfig(core); msg != "" {
		return msg
	}
	if msg := validateInboundTags(dto.Inbounds); msg != "" {
		return msg
	}
	entries := make([]inboundSyncEntry, 0, len(dto.Inbounds))
	known := make(map[string]bool, len(dto.Inbounds))
	for _, in := range dto.Inbounds {
		entries = append(entries, inboundSyncEntryFromDetail(in))
		known[in.Tag] = true
	}
	if err := validateInboundEntries(entries); err != nil {
		return err.Error()
	}
	for _, rule := range dto.RoutingRules {
		for _, tag := range rule.Inbound {
			if !known[tag] {
				return "routing rule targets unknown inbound: " + tag
			}
		}
	}
	return ""
}

// The dashboard validates the document as it is edited and holds Apply back on
// what it finds. This file is shared with its coreConfigRules.test.ts: if the
// two ever disagree, the form either blocks a config the server would take or
// promises one the server then rejects.
func TestCoreConfigValidationMatchesDashboard(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "utils", "__tests__", "core-config-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string          `json:"name"`
		Valid bool            `json:"valid"`
		Doc   json.RawMessage `json:"doc"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 90 {
		t.Fatalf("only %d cases: is this the right file?", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := serverVerdictOnDocument(tc.Doc)
			if tc.Valid && got != "" {
				t.Fatalf("the dashboard passes this, the server says: %s", got)
			}
			if !tc.Valid && got == "" {
				t.Fatal("the dashboard refuses this, the server would take it")
			}
		})
	}
}
