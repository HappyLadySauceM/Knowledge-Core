// Package kitex assembles reusable Kitex transport options that are not
// observability concerns, including a standard-library TLS client transport.
package kitex

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
)

// OutboundOptions returns tracing, TLS, metrics, and a per-dependency circuit breaker.
// OutboundOptions 返回 tracing、TLS、metrics，以及按依赖隔离的熔断中间件。
func OutboundOptions(
	telemetry *coretrace.Runtime,
	tlsConfig *tls.Config,
	metricsRegistry *metrics.Registry,
	dependency string,
) []kitexclient.Option {
	breaker := circuit.New()
	options := []kitexclient.Option{
		kitexclient.WithMiddleware(metrics.KitexClientMiddleware(metricsRegistry)),
		kitexclient.WithMiddleware(circuit.KitexClientMiddleware(breaker, metricsRegistry.ObserveCircuit(dependency))),
	}
	return append(options, ClientOptions(telemetry, tlsConfig)...)
}

// ClientOptions returns the Knowledge Core TTHeader/tracing baseline and, when
// tlsConfig is non-nil, a TLS dialer backed by Go's net transport. Kitex's
// Linux netpoll transport cannot operate directly on *tls.Conn, so the gonet
// handler is selected together with the dialer as one atomic policy.
func ClientOptions(telemetry *coretrace.Runtime, tlsConfig *tls.Config) []kitexclient.Option {
	options := coretrace.KitexClientOptions(telemetry)
	if tlsConfig == nil {
		return options
	}
	return append(options,
		kitexclient.WithDialer(tlsDialer{config: tlsConfig.Clone()}),
		kitexclient.WithTransHandlerFactory(gonet.NewCliTransHandlerFactory()),
	)
}

type tlsDialer struct {
	config *tls.Config
}

func (d tlsDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(dialer, network, address, d.config.Clone())
}
