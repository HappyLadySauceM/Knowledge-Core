package option

import (
	"fmt"
	"strings"
	"time"
)

// AppOptions contains process-wide lifecycle and deployment identity settings.
type AppOptions struct {
	Name            string        `mapstructure:"name" json:"name" yaml:"name"`
	Environment     string        `mapstructure:"environment" json:"environment" yaml:"environment"`
	Version         string        `mapstructure:"version" json:"version" yaml:"version"`
	StartupTimeout  time.Duration `mapstructure:"startup_timeout" json:"startup_timeout" yaml:"startup_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout" json:"shutdown_timeout" yaml:"shutdown_timeout"`
}

func NewAppOptions(name ...string) *AppOptions {
	serviceName := "knowledge-core.service"
	if len(name) > 0 && strings.TrimSpace(name[0]) != "" {
		serviceName = name[0]
	}
	return &AppOptions{
		Name:            serviceName,
		Environment:     "development",
		Version:         "dev",
		StartupTimeout:  30 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}
}

func (o AppOptions) Validate() error {
	return join(
		require("app.name", o.Name),
		require("app.environment", o.Environment),
		positiveDuration("app.startup_timeout", o.StartupTimeout),
		positiveDuration("app.shutdown_timeout", o.ShutdownTimeout),
		validateEnvironment(o.Environment),
	)
}

func validateEnvironment(environment string) error {
	for _, r := range environment {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("app.environment may contain only lowercase letters, digits, '-' and '_', got %q", environment)
	}
	return nil
}
