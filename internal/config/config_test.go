package config

import (
	"reflect"
	"testing"
)

func configTestEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ROLE", "UVICORN_PORT", "REDIS_DB", "DB_BACKUP_KEEP", "USAGE_RETENTION_DAYS", "DB_MAX_CONNECTIONS", "API_REPLICA_COUNT", "ALLOWED_ORIGINS"} {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/rapido_test")
}

func TestDefaultsRestrictBrowserOriginsAndReserveDatabaseConnections(t *testing.T) {
	configTestEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseMaxConnections != 200 || cfg.APIReplicaCount != 1 {
		t.Fatal("default database budget or API replica count changed unexpectedly")
	}
	if !reflect.DeepEqual(cfg.AllowedOrigins, []string{"http://localhost:5173", "http://127.0.0.1:5173"}) {
		t.Fatal("default browser origins do not restrict access to local development origins")
	}
}

func TestInvalidDeploymentConfigurationIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"missing database", "DATABASE_URL", ""},
		{"unknown role", "ROLE", "worker"},
		{"invalid HTTP port", "UVICORN_PORT", "bad"},
		{"invalid Redis database", "REDIS_DB", "bad"},
		{"invalid backup count", "DB_BACKUP_KEEP", "bad"},
		{"negative retention", "USAGE_RETENTION_DAYS", "-1"},
		{"insufficient DB reserve", "DB_MAX_CONNECTIONS", "30"},
		{"zero replicas", "API_REPLICA_COUNT", "0"},
		{"replica budget exhausted", "API_REPLICA_COUNT", "171"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configTestEnvironment(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid deployment configuration was accepted")
			}
		})
	}
}

func TestOriginsAreTrimmedAndEmptyEntriesRemoved(t *testing.T) {
	configTestEnvironment(t)
	t.Setenv("ALLOWED_ORIGINS", " https://panel.example.com, ,https://admin.example.com, ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.AllowedOrigins, []string{"https://panel.example.com", "https://admin.example.com"}) {
		t.Fatal("configured browser origins did not preserve the explicit allowlist")
	}
}
