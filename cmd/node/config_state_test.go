package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHotUpdatedCredentialsSurviveStateRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	srv := &server{stateDir: stateDir}
	old := &pulledConfig{
		Version: "v1",
		Inbounds: []inboundSpec{{
			Tag: "main", Protocol: "vless", ListenPort: 443,
			Users: []userSpec{{Name: "alice", UUID: "old-uuid"}},
		}},
	}
	if err := srv.persistConfig("last-good-config.json", old); err != nil {
		t.Fatal(err)
	}

	updated := *old
	updated.Version = ""
	updated.Inbounds = append([]inboundSpec(nil), old.Inbounds...)
	updated.Inbounds[0].Users = []userSpec{{Name: "alice", UUID: "new-uuid"}}
	if err := srv.persistConfig("last-good-config.json", &updated); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(stateDir, "last-good-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var restored pulledConfig
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored.Inbounds[0].Users[0].UUID; got != "new-uuid" {
		t.Fatalf("persisted credential = %q, want new-uuid; raw=%s", got, raw)
	}
}
