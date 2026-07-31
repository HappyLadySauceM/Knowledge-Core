package redis

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	redisclient "github.com/redis/go-redis/v9"
)

type poolCollector struct {
	client             *redisclient.Client
	dependency         string
	maxConnections     int
	minIdleConnections int
	connections        *prometheus.Desc
	pendingRequests    *prometheus.Desc
	hits               *prometheus.Desc
	misses             *prometheus.Desc
	timeouts           *prometheus.Desc
	waits              *prometheus.Desc
	waitDuration       *prometheus.Desc
	unusable           *prometheus.Desc
	staleConnections   *prometheus.Desc
	configuredMaximum  *prometheus.Desc
	configuredMinIdle  *prometheus.Desc
}

func newPoolCollector(dependency string, client *redisclient.Client) *poolCollector {
	dependency = strings.TrimSpace(dependency)
	labels := []string{"dependency"}
	return &poolCollector{
		client:             client,
		dependency:         dependency,
		maxConnections:     client.Options().PoolSize,
		minIdleConnections: client.Options().MinIdleConns,
		connections: prometheus.NewDesc(
			"knowledge_core_redis_pool_connections",
			"Current number of Redis connections by state.",
			[]string{"dependency", "state"}, nil,
		),
		pendingRequests: prometheus.NewDesc(
			"knowledge_core_redis_pool_pending_requests",
			"Current number of requests waiting for a Redis connection.",
			labels, nil,
		),
		hits: prometheus.NewDesc(
			"knowledge_core_redis_pool_hits_total",
			"Total number of times a free Redis connection was found.",
			labels, nil,
		),
		misses: prometheus.NewDesc(
			"knowledge_core_redis_pool_misses_total",
			"Total number of times a free Redis connection was not found.",
			labels, nil,
		),
		timeouts: prometheus.NewDesc(
			"knowledge_core_redis_pool_timeouts_total",
			"Total number of Redis connection pool wait timeouts.",
			labels, nil,
		),
		waits: prometheus.NewDesc(
			"knowledge_core_redis_pool_waits_total",
			"Total number of waits for a Redis connection.",
			labels, nil,
		),
		waitDuration: prometheus.NewDesc(
			"knowledge_core_redis_pool_wait_duration_seconds_total",
			"Total time spent waiting for Redis connections in seconds.",
			labels, nil,
		),
		unusable: prometheus.NewDesc(
			"knowledge_core_redis_pool_unusable_connections_total",
			"Total number of unusable Redis connections encountered.",
			labels, nil,
		),
		staleConnections: prometheus.NewDesc(
			"knowledge_core_redis_pool_stale_connections_total",
			"Total number of stale Redis connections removed.",
			labels, nil,
		),
		configuredMaximum: prometheus.NewDesc(
			"knowledge_core_redis_pool_max_connections",
			"Configured maximum Redis connection pool size.",
			labels, nil,
		),
		configuredMinIdle: prometheus.NewDesc(
			"knowledge_core_redis_pool_min_idle_connections",
			"Configured minimum number of idle Redis connections.",
			labels, nil,
		),
	}
}

func (c *poolCollector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{
		c.connections,
		c.pendingRequests,
		c.hits,
		c.misses,
		c.timeouts,
		c.waits,
		c.waitDuration,
		c.unusable,
		c.staleConnections,
		c.configuredMaximum,
		c.configuredMinIdle,
	} {
		descriptions <- description
	}
}

func (c *poolCollector) Collect(metrics chan<- prometheus.Metric) {
	stats := c.client.PoolStats()
	usedConnections := stats.TotalConns
	if stats.IdleConns <= usedConnections {
		usedConnections -= stats.IdleConns
	}
	for state, value := range map[string]uint32{
		"total": stats.TotalConns,
		"idle":  stats.IdleConns,
		"used":  usedConnections,
	} {
		metrics <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, float64(value), c.dependency, state)
	}
	metrics <- prometheus.MustNewConstMetric(c.pendingRequests, prometheus.GaugeValue, float64(stats.PendingRequests), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(stats.Hits), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(stats.Misses), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.timeouts, prometheus.CounterValue, float64(stats.Timeouts), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.waits, prometheus.CounterValue, float64(stats.WaitCount), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.waitDuration, prometheus.CounterValue, float64(stats.WaitDurationNs)/1e9, c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.unusable, prometheus.CounterValue, float64(stats.Unusable), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.staleConnections, prometheus.CounterValue, float64(stats.StaleConns), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.configuredMaximum, prometheus.GaugeValue, float64(c.maxConnections), c.dependency)
	metrics <- prometheus.MustNewConstMetric(c.configuredMinIdle, prometheus.GaugeValue, float64(c.minIdleConnections), c.dependency)
}

var _ prometheus.Collector = (*poolCollector)(nil)
