package httpapi

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/legendary1205/rapido-go/internal/tunneldoctor"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

// TestAPIDocExamplesMatchTheResponseTypes decodes every example in
// docs/api-network-features.md into the Go type that produces that response,
// refusing unknown fields, so the contract document cannot drift from the code.
func TestAPIDocExamplesMatchTheResponseTypes(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api-network-features.md")
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]func() any{
		"node_path": func() any { return &tunnelmetrics.NodeMetrics{} },
		"events": func() any {
			return &struct {
				TunnelID int32            `json:"tunnel_id"`
				Events   []tunnelEventDTO `json:"events"`
			}{}
		},
		"timeline": func() any {
			return &struct {
				Hours  int              `json:"hours"`
				Events []tunnelEventDTO `json:"events"`
			}{}
		},
		"samples": func() any {
			return &struct {
				TunnelID      int32            `json:"tunnel_id"`
				Hours         int              `json:"hours"`
				BucketSeconds int              `json:"bucket_seconds"`
				Since         string           `json:"since"`
				Points        []samplePointDTO `json:"points"`
				Events        []tunnelEventDTO `json:"events"`
			}{}
		},
		"doctor_running":          func() any { return &tunneldoctor.Job{} },
		"doctor_job":              func() any { return &tunneldoctor.Job{} },
		"overview": func() any {
			return &struct {
				GeneratedAt string             `json:"generated_at"`
				Admins      []adminOverviewDTO `json:"admins"`
				Totals      adminOverviewDTO   `json:"totals"`
			}{}
		},
		"bulk":         func() any { return &bulkResponse{} },
		"node_restart": func() any { return &nodeRestartResponse{} },
		"node_reboot":  func() any { return &nodeRestartResponse{} },
		"node":         func() any { return &nodeDTO{} },
	}

	re := regexp.MustCompile("(?s)<!-- example:(\\w+) -->\\s*```json\\n(.*?)\\n```")
	found := map[string]bool{}
	for _, m := range re.FindAllSubmatch(raw, -1) {
		name := string(m[1])
		mk, ok := targets[name]
		if !ok {
			t.Errorf("example %q has no target type in this test", name)
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(m[2]))
		dec.DisallowUnknownFields()
		if err := dec.Decode(mk()); err != nil {
			t.Errorf("example %q does not match its type: %v", name, err)
		}
		found[name] = true
	}
	for name := range targets {
		if !found[name] {
			t.Errorf("the document has no example %q", name)
		}
	}
}
