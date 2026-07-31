// Package metrics owns an isolated Prometheus registry for each service.
package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry embeds Prometheus's registry and therefore directly implements
// prometheus.Registerer and prometheus.Gatherer.
type Registry struct {
	*prometheus.Registry
	ready        prometheus.Gauge
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge
	rpcRequests  *prometheus.CounterVec
	rpcDuration  *prometheus.HistogramVec
	rpcInFlight  *prometheus.GaugeVec
	handler      http.Handler
}

type Config struct {
	Service     string
	Environment string
	Version     string
}

func NewRegistry(config Config) (*Registry, error) {
	config.Service = strings.TrimSpace(config.Service)
	config.Environment = strings.TrimSpace(config.Environment)
	config.Version = strings.TrimSpace(config.Version)
	if config.Service == "" || config.Environment == "" {
		return nil, errors.New("create metrics registry: service and environment are required")
	}
	if config.Version == "" {
		config.Version = "unknown"
	}

	registry := prometheus.NewRegistry()
	appInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "knowledge_core",
		Subsystem: "app",
		Name:      "info",
		Help:      "Static service build and deployment information.",
	}, []string{"service", "environment", "version"})
	ready := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "knowledge_core",
		Subsystem: "app",
		Name:      "ready",
		Help:      "Whether the application is ready to serve traffic.",
	})
	httpRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "knowledge_core",
		Subsystem: "http_server",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests handled.",
	}, []string{"method", "route", "status_code"})
	httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "knowledge_core",
		Subsystem: "http_server",
		Name:      "request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route", "status_code"})
	httpInFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "knowledge_core",
		Subsystem: "http_server",
		Name:      "requests_in_flight",
		Help:      "Current number of HTTP requests being handled.",
	})
	rpcRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "knowledge_core",
		Subsystem: "rpc_server",
		Name:      "requests_total",
		Help:      "Total number of RPC requests handled.",
	}, []string{"rpc_service", "rpc_method", "outcome", "business_code"})
	rpcDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "knowledge_core",
		Subsystem: "rpc_server",
		Name:      "request_duration_seconds",
		Help:      "RPC request duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"rpc_service", "rpc_method", "outcome"})
	rpcInFlight := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "knowledge_core",
		Subsystem: "rpc_server",
		Name:      "requests_in_flight",
		Help:      "Current number of RPC requests being handled.",
	}, []string{"rpc_service", "rpc_method"})

	registeredCollectors := []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		appInfo,
		ready,
		httpRequests,
		httpDuration,
		httpInFlight,
		rpcRequests,
		rpcDuration,
		rpcInFlight,
	}
	for _, collector := range registeredCollectors {
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("create metrics registry: register collector: %w", err)
		}
	}
	appInfo.WithLabelValues(config.Service, config.Environment, config.Version).Set(1)

	result := &Registry{
		Registry:     registry,
		ready:        ready,
		httpRequests: httpRequests,
		httpDuration: httpDuration,
		httpInFlight: httpInFlight,
		rpcRequests:  rpcRequests,
		rpcDuration:  rpcDuration,
		rpcInFlight:  rpcInFlight,
	}
	result.handler = promhttp.InstrumentMetricHandler(
		result.Registerer(),
		promhttp.HandlerFor(result.Gatherer(), promhttp.HandlerOpts{
			EnableOpenMetrics:   true,
			MaxRequestsInFlight: 5,
			Timeout:             10 * time.Second,
		}),
	)
	return result, nil
}

func (r *Registry) Gatherer() prometheus.Gatherer {
	if r == nil || r.Registry == nil {
		return prometheus.DefaultGatherer
	}
	return r.Registry
}

func (r *Registry) Registerer() prometheus.Registerer {
	if r == nil || r.Registry == nil {
		return prometheus.DefaultRegisterer
	}
	return r.Registry
}

func (r *Registry) Handler() http.Handler {
	if r == nil || r.handler == nil {
		return promhttp.Handler()
	}
	return r.handler
}

func (r *Registry) SetReady(ready bool) {
	if r == nil || r.ready == nil {
		return
	}
	if ready {
		r.ready.Set(1)
		return
	}
	r.ready.Set(0)
}

// RegisterDBStats exports database/sql pool saturation and churn metrics on
// this service's isolated registry. name must be a stable, low-cardinality
// logical dependency name rather than a DSN or tenant/database identifier.
func (r *Registry) RegisterDBStats(name string, db *sql.DB) error {
	if r == nil || r.Registry == nil {
		return errors.New("register database metrics: registry is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" || db == nil {
		return errors.New("register database metrics: name and database are required")
	}
	if err := r.Register(collectors.NewDBStatsCollector(db, name)); err != nil {
		return fmt.Errorf("register database metrics %q: %w", name, err)
	}
	return nil
}

var (
	_ prometheus.Gatherer   = (*Registry)(nil)
	_ prometheus.Registerer = (*Registry)(nil)
)
