// Package kitex assembles reusable Kitex transport options that are not
// observability concerns, including a standard-library TLS client transport.
package kitex

import (
	"crypto/tls"
	"net"
	"time"

	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
)

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
