package option

import (
	"fmt"
	"time"
)

type TraceOptions struct {
	Enabled       bool              `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	Endpoint      string            `mapstructure:"endpoint" json:"endpoint" yaml:"endpoint"`
	SampleRatio   float64           `mapstructure:"sample_ratio" json:"sample_ratio" yaml:"sample_ratio"`
	Insecure      bool              `mapstructure:"insecure" json:"insecure" yaml:"insecure"`
	Headers       map[string]string `mapstructure:"headers" json:"headers" yaml:"headers"`
	BatchTimeout  time.Duration     `mapstructure:"batch_timeout" json:"batch_timeout" yaml:"batch_timeout"`
	ExportTimeout time.Duration     `mapstructure:"export_timeout" json:"export_timeout" yaml:"export_timeout"`
	TLS           TLSOptions        `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewTraceOptions() *TraceOptions {
	return &TraceOptions{
		Enabled:       true,
		Endpoint:      "127.0.0.1:4317",
		SampleRatio:   0.1,
		Insecure:      true,
		Headers:       make(map[string]string),
		BatchTimeout:  5 * time.Second,
		ExportTimeout: 10 * time.Second,
	}
}

func (o TraceOptions) Validate() error {
	var endpointErr error
	if o.Enabled {
		endpointErr = validateEndpoint("trace.endpoint", o.Endpoint)
	}
	var ratioErr error
	if o.SampleRatio < 0 || o.SampleRatio > 1 {
		ratioErr = fmt.Errorf("trace.sample_ratio must be between 0 and 1, got %v", o.SampleRatio)
	}
	var transportErr error
	if o.Insecure && o.TLS.Enabled {
		transportErr = fmt.Errorf("trace.insecure and trace.tls.enabled cannot both be true")
	}
	return join(
		endpointErr,
		ratioErr,
		positiveDuration("trace.batch_timeout", o.BatchTimeout),
		positiveDuration("trace.export_timeout", o.ExportTimeout),
		transportErr,
		o.TLS.Validate(),
	)
}
