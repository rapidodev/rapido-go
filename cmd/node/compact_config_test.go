package main

import (
	"encoding/json"
	"testing"
)

func TestCompactDeltaPreservesUnchangedUsersAndRejectsWrongBase(t *testing.T) {
	var config pulledConfig
	full := []byte(`{"version":"v1","core":{},"inbounds":[{"tag":"a","protocol":"vless","users_ref":"old"}],"user_sets":{"old":[{"name":"kept","uuid":"one"},{"name":"removed","uuid":"two"}]}}`)
	if err := json.Unmarshal(full, &config); err != nil {
		t.Fatal(err)
	}
	delta := []byte(`{"version":"v2","base_version":"v1","core":{},"inbounds":[{"tag":"a","protocol":"vless","users_ref":"new"}],"user_sets_delta":{"new":{"base":"old","remove":["removed"],"upsert":[{"name":"added","uuid":"three"}]}}}`)
	if err := json.Unmarshal(delta, &config); err != nil {
		t.Fatal(err)
	}
	users := config.Inbounds[0].Users
	if len(users) != 2 || users[0].Name != "kept" || users[1].Name != "added" {
		t.Fatalf("bad delta merge: %v", users)
	}
	if err := json.Unmarshal(delta, &config); err == nil {
		t.Fatal("wrong-base delta accepted")
	}
}
