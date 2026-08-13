package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ConfigLoader owns the mutable command/config state for one application.
// Implementations must not use the global pflag or Viper instances.
type ConfigLoader[C any] interface {
	AddFlags(*pflag.FlagSet)
	Load(context.Context, *cobra.Command) (C, error)
}

// RuntimeBinder attaches configuration sources that need process-owned
// lifecycle, logging, metrics, or cleanup after the runtime exists.
type RuntimeBinder interface {
	BindRuntime(context.Context, *Runtime) error
}

// RuntimeOptions contains the process-wide settings needed before service
// dependencies can be constructed.
type RuntimeOptions struct {
	Service         string
	Environment     string
	Version         string
	LogLevel        string
	LogAddSource    bool
	LogHealthChecks bool
	OTLPEndpoint    string
	TraceSampleRate float64
	TraceInsecure   bool
	TraceHeaders    map[string]string
	TraceBatch      time.Duration
	TraceExport     time.Duration
	TraceTLS        *tls.Config
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

// RuntimeOptionsFrom maps reusable process options to the application
// runtime. Services remain responsible for validating their complete Config
// before calling this helper.
func RuntimeOptionsFrom(
	appOptions *option.AppOptions,
	logOptions *option.LogOptions,
	traceOptions *option.TraceOptions,
) (RuntimeOptions, error) {
	switch {
	case appOptions == nil:
		return RuntimeOptions{}, errors.New("configure application runtime: app options are required")
	case logOptions == nil:
		return RuntimeOptions{}, errors.New("configure application runtime: log options are required")
	case traceOptions == nil:
		return RuntimeOptions{}, errors.New("configure application runtime: trace options are required")
	}
	if err := errors.Join(appOptions.Validate(), logOptions.Validate(), traceOptions.Validate()); err != nil {
		return RuntimeOptions{}, fmt.Errorf("configure application runtime: %w", err)
	}

	runtimeOptions := RuntimeOptions{
		Service:         appOptions.Name,
		Environment:     appOptions.Environment,
		Version:         appOptions.Version,
		LogLevel:        logOptions.Level,
		LogAddSource:    logOptions.AddSource,
		LogHealthChecks: logOptions.HealthCheckRequests,
		TraceBatch:      traceOptions.BatchTimeout,
		TraceExport:     traceOptions.ExportTimeout,
		StartupTimeout:  appOptions.StartupTimeout,
		ShutdownTimeout: appOptions.ShutdownTimeout,
	}
	if !traceOptions.Enabled {
		return runtimeOptions, nil
	}

	tlsConfig, err := traceOptions.TLS.ClientTLSConfig()
	if err != nil {
		return RuntimeOptions{}, fmt.Errorf("configure application tracing TLS: %w", err)
	}
	runtimeOptions.OTLPEndpoint = traceOptions.Endpoint
	runtimeOptions.TraceSampleRate = traceOptions.SampleRatio
	runtimeOptions.TraceInsecure = traceOptions.Insecure
	runtimeOptions.TraceHeaders = traceOptions.Headers
	runtimeOptions.TraceTLS = tlsConfig
	return runtimeOptions, nil
}

// Spec describes one executable application without coupling pkg/app to its
// private configuration or dependency graph.
type Spec[C any] struct {
	Name           string
	Config         ConfigLoader[C]
	RuntimeOptions func(C) (RuntimeOptions, error)
	Register       func(context.Context, C, *Runtime) error
}

// NewAPICommand creates the root command for a service process. Libraries
// return errors to the executable and never terminate the process.
func NewAPICommand[C any](parent context.Context, spec Spec[C]) *cobra.Command {
	if parent == nil {
		parent = context.Background()
	}

	cmd := &cobra.Command{
		Use:           spec.Name,
		Short:         "Run the " + spec.Name + " service",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateSpec(spec); err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
			defer stop()
			cmd.SetContext(ctx)

			cfg, err := spec.Config.Load(ctx, cmd)
			if err != nil {
				return fmt.Errorf("load %s configuration: %w", spec.Name, err)
			}
			return execute(ctx, cfg, spec)
		},
	}
	cmd.CompletionOptions.DisableDefaultCmd = true

	if spec.Config != nil {
		spec.Config.AddFlags(cmd.Flags())
	}
	return cmd
}

func validateSpec[C any](spec Spec[C]) error {
	switch {
	case spec.Name == "":
		return errors.New("create application command: name is required")
	case spec.Config == nil:
		return errors.New("create application command: config loader is required")
	case spec.RuntimeOptions == nil:
		return errors.New("create application command: runtime options function is required")
	case spec.Register == nil:
		return errors.New("create application command: register function is required")
	default:
		return nil
	}
}

func execute[C any](ctx context.Context, cfg C, spec Spec[C]) (runErr error) {
	opts, err := spec.RuntimeOptions(cfg)
	if err != nil {
		return fmt.Errorf("configure %s application runtime: %w", spec.Name, err)
	}
	if opts.Service == "" {
		opts.Service = spec.Name
	}
	if opts.StartupTimeout <= 0 {
		return errors.New("initialize application runtime: startup timeout must be positive")
	}
	if opts.ShutdownTimeout <= 0 {
		return errors.New("initialize application runtime: shutdown timeout must be positive")
	}

	logger, levelControl, err := corelog.NewWithOptions(corelog.Options{
		Service:     opts.Service,
		Environment: opts.Environment,
		Level:       opts.LogLevel,
		AddSource:   opts.LogAddSource,
		Output:      os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("initialize application logger: %w", err)
	}
	corelog.InstallCloudWeGo(logger, levelControl)
	telemetry, err := trace.New(ctx, trace.Config{
		Service:       opts.Service,
		Environment:   opts.Environment,
		Endpoint:      opts.OTLPEndpoint,
		SampleRatio:   opts.TraceSampleRate,
		Insecure:      opts.TraceInsecure,
		Headers:       opts.TraceHeaders,
		BatchTimeout:  opts.TraceBatch,
		ExportTimeout: opts.TraceExport,
		TLSConfig:     opts.TraceTLS,
	}, logger)
	if err != nil {
		return fmt.Errorf("initialize application tracing: %w", err)
	}

	metricsRegistry, err := metrics.NewRegistry(metrics.Config{
		Service:     opts.Service,
		Environment: opts.Environment,
		Version:     opts.Version,
	})
	if err != nil {
		return errors.Join(fmt.Errorf("initialize application metrics: %w", err), telemetry.Shutdown(context.Background()))
	}

	runtime := newRuntime(logger, levelControl, telemetry, metricsRegistry, opts.LogHealthChecks, opts.ShutdownTimeout)
	if err := runtime.AddCleanup("telemetry", telemetry.Shutdown); err != nil {
		return errors.Join(err, telemetry.Shutdown(context.Background()))
	}
	defer func() {
		if runtime.closed() {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), opts.ShutdownTimeout)
		defer cancel()
		runErr = errors.Join(runErr, runtime.close(closeCtx))
	}()

	logger.InfoContext(ctx, "application bootstrap started",
		slog.String("component", "app"),
		slog.String("event", "bootstrap"),
	)
	startupCtx, cancelStartup := context.WithTimeout(ctx, opts.StartupTimeout)
	err = spec.Register(startupCtx, cfg, runtime)
	if err != nil {
		cancelStartup()
		return fmt.Errorf("register %s application: %w", spec.Name, err)
	}
	if binder, ok := spec.Config.(RuntimeBinder); ok {
		if err := binder.BindRuntime(ctx, runtime); err != nil {
			cancelStartup()
			return fmt.Errorf("bind %s runtime configuration: %w", spec.Name, err)
		}
	}
	if err := runtime.run(ctx, startupCtx); err != nil {
		cancelStartup()
		return err
	}
	cancelStartup()
	logger.InfoContext(context.Background(), "application stopped",
		slog.String("component", "app"),
		slog.String("event", "shutdown"),
	)
	return nil
}
