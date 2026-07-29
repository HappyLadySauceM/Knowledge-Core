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
		"KC_DATABASE_DSN":                "postgres://redacted",
		"KC_ETCD_ENDPOINTS":              "etcd-a:2379, etcd-b:2379",
		"KC_SHUTDOWN_TIMEOUT":            "15s",
		"KC_DATABASE_MAX_OPEN_CONNS":     "30",
		"KC_OTEL_TRACE_SAMPLE_RATIO":     "0.25",
		"KC_OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
	}
	lookup := func(key string) (string, bool) {
		value, exists := values[key]
		return value, exists
	}
	config, err := loadConfig("identity", "KC_RPC_ADDR", ":8881", Needs{Database: true, Registry: true}, lookup)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.Database.MaxOpenConns != 30 || len(config.Etcd.Endpoints) != 2 || config.Observability.SampleRatio != 0.25 {
		t.Fatalf("loadConfig() = %#v", config)
	}
}

func TestLoadConfigRejectsInvalidObservabilityConfiguration(t *testing.T) {
	for key, value := range map[string]string{
		"KC_LOG_LEVEL":                   "verbose",
		"KC_OTEL_TRACE_SAMPLE_RATIO":     "1.5",
		"KC_OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317",
	} {
		t.Run(key, func(t *testing.T) {
			lookup := func(candidate string) (string, bool) {
				if candidate == key {
					return value, true
				}
				return "", false
			}
			if _, err := loadConfig("gateway", "KC_HTTP_ADDR", ":8080", Needs{}, lookup); err == nil {
				t.Fatalf("loadConfig() accepted %s=%q", key, value)
			}
		})
	}
}
