package metrics_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestRegistryIsIsolatedAndExposesHandler(t *testing.T) {
	registry := newRegistry(t, "first")
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "knowledge_core",
		Subsystem: "test",
		Name:      "requests_total",
		Help:      "Test requests.",
	})
	if err := registry.Register(counter); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	counter.Inc()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	registry.Handler().ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("metrics status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "knowledge_core_test_requests_total 1") {
		t.Fatalf("metrics response does not contain counter:\n%s", recorder.Body.String())
	}
}

func TestRegistryExportsApplicationIdentityAndReadiness(t *testing.T) {
	registry := newRegistry(t, "identity")
	info := gatherFamily(t, registry, "knowledge_core_app_info")
	if got := gaugeValue(info, map[string]string{
		"service":     "identity",
		"environment": "testing",
		"version":     "test",
	}); got != 1 {
		t.Fatalf("app info = %v, want 1", got)
	}

	registry.SetReady(true)
	if got := gaugeValue(gatherFamily(t, registry, "knowledge_core_app_ready"), nil); got != 1 {
		t.Fatalf("ready metric = %v, want 1", got)
	}
	registry.SetReady(false)
	if got := gaugeValue(gatherFamily(t, registry, "knowledge_core_app_ready"), nil); got != 0 {
		t.Fatalf("ready metric = %v, want 0", got)
	}
}

func TestRegistryExportsCircuitState(t *testing.T) {
	registry := newRegistry(t, "gateway")
	registry.SetCircuitState("collaboration", circuit.StateOpen)
	family := gatherFamily(t, registry, "knowledge_core_rpc_client_circuit_state")
	if got := gaugeValue(family, map[string]string{"dependency": "collaboration", "state": "open"}); got != 1 {
		t.Fatalf("open state = %v, want 1", got)
	}
	if got := gaugeValue(family, map[string]string{"dependency": "collaboration", "state": "closed"}); got != 0 {
		t.Fatalf("closed state = %v, want 0", got)
	}
	registry.SetCircuitState("collaboration", circuit.StateClosed)
	family = gatherFamily(t, registry, "knowledge_core_rpc_client_circuit_state")
	if got := gaugeValue(family, map[string]string{"dependency": "collaboration", "state": "closed"}); got != 1 {
		t.Fatalf("closed state after reset = %v, want 1", got)
	}
}

func TestRegistriesDoNotShareCollectors(t *testing.T) {
	first := newRegistry(t, "first")
	second := newRegistry(t, "second")
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "isolated_value", Help: "An isolated value."})
	first.MustRegister(gauge)
	families, err := second.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() == "isolated_value" {
			t.Fatal("collector leaked into another service registry")
		}
	}
}

func TestRegisterDBStats(t *testing.T) {
	registry := newRegistry(t, "database")
	db := sql.OpenDB(noopConnector{})
	t.Cleanup(func() { _ = db.Close() })
	if err := registry.RegisterDBStats("identity", db); err != nil {
		t.Fatalf("RegisterDBStats() error = %v", err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != "go_sql_open_connections" {
			continue
		}
		for _, label := range family.Metric[0].Label {
			if label.GetName() == "db_name" && label.GetValue() == "identity" {
				return
			}
		}
	}
	t.Fatal("database/sql metrics with the identity label were not gathered")
}

type noopConnector struct{}

func (noopConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func (noopConnector) Driver() driver.Driver { return noopDriver{} }

type noopDriver struct{}

func (noopDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("not implemented")
}

func newRegistry(t *testing.T, service string) *metrics.Registry {
	t.Helper()
	registry, err := metrics.NewRegistry(metrics.Config{
		Service:     service,
		Environment: "testing",
		Version:     "test",
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func gatherFamily(t *testing.T, registry *metrics.Registry, name string) *dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q was not gathered", name)
	return nil
}

func counterValue(family *dto.MetricFamily, labels map[string]string) float64 {
	for _, metric := range family.Metric {
		if metricHasLabels(metric, labels) {
			return metric.GetCounter().GetValue()
		}
	}
	return 0
}

func gaugeValue(family *dto.MetricFamily, labels map[string]string) float64 {
	for _, metric := range family.Metric {
		if metricHasLabels(metric, labels) {
			return metric.GetGauge().GetValue()
		}
	}
	return 0
}

func metricHasLabels(metric *dto.Metric, expected map[string]string) bool {
	if len(metric.Label) != len(expected) {
		return false
	}
	for _, label := range metric.Label {
		if expected[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
