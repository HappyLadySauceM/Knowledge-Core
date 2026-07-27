package bootstrap

import "testing"

func TestLoadConfigRequiresDatabaseSecret(t *testing.T) {
	lookup := func(key string) (string, bool) { return "", false }
	_, err := loadConfig("identity", "KC_RPC_ADDR", ":8881", Needs{Database: true}, lookup)
	if err == nil {
		t.Fatal("loadConfig() accepted missing database DSN")
	}
}

func TestLoadConfigAppliesExplicitProviderAndEndpoint(t *testing.T) {
	values := map[string]string{
		"KC_DATABASE_DSN":            "postgres://redacted",
		"KC_ETCD_ENDPOINTS":          "etcd-a:2379, etcd-b:2379",
		"KC_SHUTDOWN_TIMEOUT":        "15s",
		"KC_DATABASE_MAX_OPEN_CONNS": "30",
	}
	lookup := func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	}
	config, err := loadConfig("identity", "KC_RPC_ADDR", ":8881", Needs{Database: true, Registry: true}, lookup)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.Database.MaxOpenConns != 30 || len(config.Etcd.Endpoints) != 2 {
		t.Fatalf("loadConfig() = %#v", config)
	}
}
