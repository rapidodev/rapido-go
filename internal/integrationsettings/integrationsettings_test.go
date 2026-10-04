package integrationsettings

import (
	"reflect"
	"testing"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

func TestResolveMergesTelegramTopicIDsOverEnvDefault(t *testing.T) {
	env := Values{TelegramTopicIDs: map[string]int64{"login": 1}}

	// No DB override at all: the env default survives untouched.
	got := Resolve(generated.IntegrationSetting{}, env)
	if !reflect.DeepEqual(got.TelegramTopicIDs, env.TelegramTopicIDs) {
		t.Errorf("with no DB row, TelegramTopicIDs = %v, want the env default %v", got.TelegramTopicIDs, env.TelegramTopicIDs)
	}

	// A DB override replaces it wholesale, not merges per key - matching
	// every other map/slice field this function resolves.
	row := generated.IntegrationSetting{TelegramTopicIds: []byte(`{"infra_alert":42,"user_created":7}`)}
	got = Resolve(row, env)
	want := map[string]int64{"infra_alert": 42, "user_created": 7}
	if !reflect.DeepEqual(got.TelegramTopicIDs, want) {
		t.Errorf("TelegramTopicIDs = %v, want %v", got.TelegramTopicIDs, want)
	}
}
