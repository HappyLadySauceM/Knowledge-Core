// Package metrics owns an isolated Prometheus registry for each service.
package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry embeds Prometheus's registry and therefore directly implements
// prometheus.Registerer and prometheus.Gatherer.
type Registry struct {
	*prometheus.Registry
}

func NewRegistry() *Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Registry{Registry: registry}
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
	return promhttp.HandlerFor(r.Gatherer(), promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
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
