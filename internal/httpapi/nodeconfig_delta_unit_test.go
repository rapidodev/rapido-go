package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// A node that has heard of no change for longer than the cache's freshness
// limit still gets only the users that changed, not the whole list again.
func TestDeltaIsServedFromABaseOlderThanTheFreshnessLimit(t *testing.T) {
	users := make([]nodeConfigUserSpec, 300)
	for i := range users {
		users[i] = nodeConfigUserSpec{Name: fmt.Sprintf("u%03d", i), UUID: fmt.Sprintf("uuid-%03d", i), Flow: "xtls-rprx-vision"}
	}
	payload := func(version, ref string, list []nodeConfigUserSpec) []byte {
		raw, err := json.Marshal(map[string]any{
			"version":   version,
			"core":      map[string]any{"log_level": "warn"},
			"inbounds":  []any{map[string]any{"tag": "main", "protocol": "vless", "users_ref": ref}},
			"user_sets": map[string]any{ref: list},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	c := &nodeConfigCache{}
	old := &nodeConfigEntry{version: 1, builtAt: time.Now().Add(-5 * time.Minute), etag: `"v1"`, body: payload("v1", "set1", users)}
	c.store("profile", old)

	changed := append([]nodeConfigUserSpec(nil), users...)
	changed[42].UUID = "uuid-new"
	current := &nodeConfigEntry{version: 2, builtAt: time.Now(), etag: `"v2"`, body: payload("v2", "set2", changed)}
	c.store("profile", current)

	raw := c.delta("profile", `"v1"`, current)
	if raw == nil {
		t.Fatal("a five-minute-old base must still give a delta")
	}
	var d struct {
		Base   string                `json:"base_version"`
		Deltas map[string]usersDelta `json:"user_sets_delta"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Base != "v1" || len(d.Deltas["set2"].Upsert) != 1 || d.Deltas["set2"].Upsert[0].UUID != "uuid-new" || d.Deltas["set2"].Base != "set1" {
		t.Fatalf("delta = %s", raw)
	}

	// Past the delta limit the base is gone, and the whole payload is sent.
	stale := &nodeConfigEntry{version: 3, builtAt: time.Now().Add(-nodeConfigDeltaTTL - time.Minute), etag: `"v3"`, body: payload("v3", "set3", users)}
	c.store("other", stale)
	next := &nodeConfigEntry{version: 4, builtAt: time.Now(), etag: `"v4"`, body: payload("v4", "set4", changed)}
	c.store("other", next)
	if c.delta("other", `"v3"`, next) != nil {
		t.Error("a base older than nodeConfigDeltaTTL must not be used")
	}
}
