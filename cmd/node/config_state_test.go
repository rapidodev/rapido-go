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

// Before a hot user change the previous config is snapshotted by linking the
// last-good file this process wrote, not by marshalling every user again. The
// snapshot must still hold exactly the old config after the new one replaces
// last-good, and anything that isn't what was written falls back to a real
// write.
func TestSnapshotPreviousLinksTheLastGoodFileOnlyWhenItIsThatConfig(t *testing.T) {
	dir := t.TempDir()
	srv := &server{stateDir: dir}
	version := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var cfg pulledConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg.Version + "/" + cfg.Inbounds[0].Users[0].UUID
	}
	cfg := func(v, uuid string) *pulledConfig {
		return &pulledConfig{Version: v, Inbounds: []inboundSpec{{
			Tag: "main", Protocol: "vless", ListenPort: 443, Users: []userSpec{{Name: "alice", UUID: uuid}},
		}}}
	}

	a := cfg("a", "uuid-a")
	if err := srv.persistConfig(lastGoodConfigFile, a); err != nil {
		t.Fatal(err)
	}
	if err := srv.snapshotPrevious(a); err != nil {
		t.Fatal(err)
	}
	if got := version(previousConfigFile); got != "a/uuid-a" {
		t.Fatalf("previous = %s, want a/uuid-a", got)
	}
	if got := version(lastGoodConfigFile); got != "a/uuid-a" {
		t.Fatalf("the snapshot must leave last-good in place, it holds %s", got)
	}

	b := cfg("b", "uuid-b")
	if err := srv.persistConfig(lastGoodConfigFile, b); err != nil {
		t.Fatal(err)
	}
	if got := version(previousConfigFile); got != "a/uuid-a" {
		t.Fatalf("writing the new last-good changed the snapshot to %s", got)
	}

	// The same config changed in memory since it was written: write it out.
	b.Version = ""
	if err := srv.snapshotPrevious(b); err != nil {
		t.Fatal(err)
	}
	if got := version(previousConfigFile); got != "/uuid-b" {
		t.Fatalf("previous = %s, want the config as it is now", got)
	}

	// A config that was never written to last-good.
	if err := srv.snapshotPrevious(cfg("c", "uuid-c")); err != nil {
		t.Fatal(err)
	}
	if got := version(previousConfigFile); got != "c/uuid-c" {
		t.Fatalf("previous = %s, want c/uuid-c", got)
	}
	if _, err := os.Stat(filepath.Join(dir, previousConfigFile+".link")); !os.IsNotExist(err) {
		t.Errorf("a temporary link was left behind: %v", err)
	}
}
