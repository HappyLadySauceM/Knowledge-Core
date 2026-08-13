package option

import (
	"fmt"
	"strings"
)

type LogOptions struct {
	Level               string `mapstructure:"level" json:"level" yaml:"level"`
	AddSource           bool   `mapstructure:"add_source" json:"add_source" yaml:"add_source"`
	HealthCheckRequests bool   `mapstructure:"health_check_requests" json:"health_check_requests" yaml:"health_check_requests"`
}

func NewLogOptions() *LogOptions {
	return &LogOptions{Level: "info", HealthCheckRequests: true}
}

func (o LogOptions) Validate() error {
	var levelErr error
	switch strings.ToLower(o.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		levelErr = fmt.Errorf("log.level must be one of debug, info, warn, error, got %q", o.Level)
	}
	return levelErr
}
