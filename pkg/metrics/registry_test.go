package metrics_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func TestRegistryIsIsolatedAndExposesHandler(t *testing.T) {
	registry := metrics.NewRegistry()
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

func TestRegistriesDoNotShareCollectors(t *testing.T) {
	first := metrics.NewRegistry()
	second := metrics.NewRegistry()
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
	registry := metrics.NewRegistry()
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
