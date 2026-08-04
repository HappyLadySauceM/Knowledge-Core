package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
)

// Component is a long-running transport or worker managed by the application.
type Component interface {
	Name() string
	Serve() error
	Ready(context.Context) error
	Shutdown(context.Context) error
}

// ComponentFuncs adapts functions to Component.
type ComponentFuncs struct {
	ComponentName string
	ServeFunc     func() error
	ReadyFunc     func(context.Context) error
	ShutdownFunc  func(context.Context) error
}

func (c ComponentFuncs) Name() string { return c.ComponentName }

func (c ComponentFuncs) Serve() error {
	if c.ServeFunc == nil {
		return errors.New("serve function is required")
	}
	return c.ServeFunc()
}

func (c ComponentFuncs) Ready(ctx context.Context) error {
	if c.ReadyFunc == nil {
		return nil
	}
	return c.ReadyFunc(ctx)
}

func (c ComponentFuncs) Shutdown(ctx context.Context) error {
	if c.ShutdownFunc == nil {
		return nil
	}
	return c.ShutdownFunc(ctx)
}

type cleanupHook struct {
	name string
	stop func(context.Context) error
}

// Runtime is the process-scoped dependency registry owned by pkg/app.
type Runtime struct {
	Logger   *slog.Logger
	Trace    *trace.Runtime
	Health   *health.Registry
	Metrics  *metrics.Registry
	logLevel *slog.LevelVar
	shutdown time.Duration

	mu         sync.Mutex
	components []Component
	cleanups   []cleanupHook
	running    bool
	stopped    bool
	stopUnsafe bool
	isClosed   bool
}

func newRuntime(logger *slog.Logger, levelControl *slog.LevelVar, telemetry *trace.Runtime, metricRegistry *metrics.Registry, shutdown time.Duration) *Runtime {
	return &Runtime{
		Logger:   logger,
		Trace:    telemetry,
		Health:   health.NewRegistry(),
		Metrics:  metricRegistry,
		logLevel: levelControl,
		shutdown: shutdown,
	}
}

func (r *Runtime) SetLogLevel(level string) error {
	if r == nil {
		return errors.New("set application log level: runtime is required")
	}
	if err := corelog.SetLevel(r.logLevel, level); err != nil {
		return fmt.Errorf("set application log level: %w", err)
	}
	return nil
}

// AddComponent registers a transport or worker before the runtime starts.
// Components shut down in reverse registration order.
func (r *Runtime) AddComponent(component Component) error {
	if component == nil {
		return errors.New("register application component: component is required")
	}
	name := component.Name()
	if name == "" {
		return errors.New("register application component: name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running || r.isClosed {
		return errors.New("register application component: runtime already started")
	}
	for _, existing := range r.components {
		if existing.Name() == name {
			return fmt.Errorf("register application component: duplicate name %q", name)
		}
	}
	r.components = append(r.components, component)
	return nil
}

// AddCleanup registers an already-open resource. Cleanups run once in reverse
// registration order, including when bootstrap fails.
func (r *Runtime) AddCleanup(name string, stop func(context.Context) error) error {
	if name == "" || stop == nil {
		return errors.New("register application cleanup: name and stop function are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running || r.isClosed {
		return errors.New("register application cleanup: runtime already started")
	}
	for _, existing := range r.cleanups {
		if existing.name == name {
			return fmt.Errorf("register application cleanup: duplicate name %q", name)
		}
	}
	r.cleanups = append(r.cleanups, cleanupHook{name: name, stop: stop})
	return nil
}

type componentResult struct {
	name string
	err  error
}

func (r *Runtime) run(ctx, startupCtx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if startupCtx == nil {
		startupCtx = ctx
	}

	r.mu.Lock()
	if r.running || r.isClosed {
		r.mu.Unlock()
		return errors.New("run application: runtime already used")
	}
	if len(r.components) == 0 {
		r.mu.Unlock()
		return errors.New("run application: at least one component is required")
	}
	r.running = true
	components := append([]Component(nil), r.components...)
	r.mu.Unlock()

	results := make(chan componentResult, len(components))
	readiness := make(chan componentResult, len(components))
	readyCtx, cancelReady := context.WithCancel(startupCtx)
	defer cancelReady()
	for _, component := range components {
		component := component
		invoked := make(chan struct{})
		go func() {
			close(invoked)
			results <- componentResult{name: component.Name(), err: serveComponent(component)}
		}()
		go func() {
			select {
			case <-invoked:
				readiness <- componentResult{name: component.Name(), err: readyComponent(readyCtx, component)}
			case <-readyCtx.Done():
				readiness <- componentResult{name: component.Name(), err: readyCtx.Err()}
			}
		}()
	}

	remaining := len(components)
	readyCount := 0
	startupFailed := false
	var joined error
	for readyCount < len(components) && !startupFailed {
		select {
		case result := <-readiness:
			if result.err != nil {
				if ctx.Err() == nil || !errors.Is(result.err, ctx.Err()) {
					joined = errors.Join(joined, fmt.Errorf("start component %q: %w", result.name, result.err))
				}
				startupFailed = true
				continue
			}
			readyCount++
		case result := <-results:
			remaining--
			if ctx.Err() == nil || !isExpectedStopError(result.err) {
				joined = errors.Join(joined, unexpectedServeError(result))
			}
			startupFailed = true
		case <-readyCtx.Done():
			if ctx.Err() == nil {
				joined = errors.Join(joined, fmt.Errorf("start application components: %w", readyCtx.Err()))
			}
			startupFailed = true
		case <-ctx.Done():
			startupFailed = true
		}
	}
	cancelReady()

	// Prefer a component result that is already available over publishing a
	// transient ready state for a component that failed during its handshake.
	if !startupFailed {
		select {
		case result := <-results:
			remaining--
			joined = errors.Join(joined, unexpectedServeError(result))
			startupFailed = true
		default:
		}
	}

	if !startupFailed {
		r.Health.SetServing(true)
		r.Metrics.SetReady(true)
		r.Logger.InfoContext(ctx, "application ready",
			slog.String("component", "app"),
			slog.String("event", "lifecycle"),
			slog.String("phase", "ready"),
		)
	}

	if !startupFailed {
		select {
		case result := <-results:
			remaining--
			if ctx.Err() == nil || !isExpectedStopError(result.err) {
				joined = errors.Join(joined, unexpectedServeError(result))
			}
		case <-ctx.Done():
		}
	}

	r.Health.SetServing(false)
	r.Metrics.SetReady(false)
	r.Logger.InfoContext(context.Background(), "application draining",
		slog.String("component", "app"),
		slog.String("event", "lifecycle"),
		slog.String("phase", "draining"),
	)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), r.shutdown)
	defer cancel()
	shutdownErr, shutdownReturned := r.shutdownComponents(shutdownCtx)
	joined = errors.Join(joined, shutdownErr)
	waitErr, allExited := waitForComponents(shutdownCtx, results, remaining)
	joined = errors.Join(joined, waitErr)

	if !shutdownReturned || !allExited {
		r.abandonResources()
		joined = errors.Join(joined, errors.New("application resources left open because one or more components did not stop safely"))
		return joined
	}

	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), r.shutdown)
	defer cancelCleanup()
	joined = errors.Join(joined, r.close(cleanupCtx))
	return joined
}

func (r *Runtime) close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.isClosed {
		r.mu.Unlock()
		return nil
	}
	r.isClosed = true
	cleanups := append([]cleanupHook(nil), r.cleanups...)
	r.mu.Unlock()

	joined, shutdownReturned := r.shutdownComponents(ctx)
	if !shutdownReturned {
		return errors.Join(joined, errors.New("application resources left open because component shutdown did not return"))
	}
	for index := len(cleanups) - 1; index >= 0; index-- {
		hook := cleanups[index]
		if err := hook.stop(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("close resource %q: %w", hook.name, err))
		}
	}
	return joined
}

func (r *Runtime) shutdownComponents(ctx context.Context) (error, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.stopped {
		safe := !r.stopUnsafe
		r.mu.Unlock()
		return nil, safe
	}
	r.stopped = true
	components := append([]Component(nil), r.components...)
	r.mu.Unlock()

	var joined error
	allReturned := true
	for index := len(components) - 1; index >= 0; index-- {
		component := components[index]
		result := make(chan error, 1)
		go func() {
			result <- shutdownComponent(ctx, component)
		}()
		err, returned := waitForCall(ctx, result)
		if !returned {
			allReturned = false
			joined = errors.Join(joined, fmt.Errorf("shutdown component %q: %w", component.Name(), ctx.Err()))
			continue
		}
		if err != nil && !isExpectedStopError(err) {
			joined = errors.Join(joined, fmt.Errorf("shutdown component %q: %w", component.Name(), err))
		}
	}
	r.mu.Lock()
	r.stopUnsafe = !allReturned
	r.mu.Unlock()
	return joined, allReturned
}

func (r *Runtime) abandonResources() {
	r.mu.Lock()
	r.isClosed = true
	r.mu.Unlock()
}

func (r *Runtime) closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isClosed
}

func isExpectedStopError(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed)
}

func readyComponent(ctx context.Context, component Component) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = componentPanicError("readiness", component.Name(), recovered)
		}
	}()
	return component.Ready(ctx)
}

func serveComponent(component Component) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = componentPanicError("serve", component.Name(), recovered)
		}
	}()
	return component.Serve()
}

func shutdownComponent(ctx context.Context, component Component) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = componentPanicError("shutdown", component.Name(), recovered)
		}
	}()
	return component.Shutdown(ctx)
}

func componentPanicError(operation, name string, recovered any) error {
	return fmt.Errorf("component %q %s panic: %v\n%s", name, operation, recovered, debug.Stack())
}

func unexpectedServeError(result componentResult) error {
	if result.err == nil {
		return fmt.Errorf("serve component %q: stopped unexpectedly", result.name)
	}
	return fmt.Errorf("serve component %q: %w", result.name, result.err)
}

func waitForComponents(ctx context.Context, results <-chan componentResult, remaining int) (error, bool) {
	var joined error
	for remaining > 0 {
		select {
		case result := <-results:
			remaining--
			if result.err != nil && !isExpectedStopError(result.err) {
				joined = errors.Join(joined, fmt.Errorf("serve component %q: %w", result.name, result.err))
			}
			continue
		default:
		}

		select {
		case result := <-results:
			remaining--
			if result.err != nil && !isExpectedStopError(result.err) {
				joined = errors.Join(joined, fmt.Errorf("serve component %q: %w", result.name, result.err))
			}
		case <-ctx.Done():
			// Drain results that raced with the deadline before deciding that a
			// component is still live.
			for remaining > 0 {
				select {
				case result := <-results:
					remaining--
					if result.err != nil && !isExpectedStopError(result.err) {
						joined = errors.Join(joined, fmt.Errorf("serve component %q: %w", result.name, result.err))
					}
				default:
					return errors.Join(joined, fmt.Errorf("wait for %d component(s) to stop: %w", remaining, ctx.Err())), false
				}
			}
		}
	}
	return joined, true
}

func waitForCall(ctx context.Context, result <-chan error) (error, bool) {
	select {
	case err := <-result:
		return err, true
	default:
	}
	select {
	case err := <-result:
		return err, true
	case <-ctx.Done():
		select {
		case err := <-result:
			return err, true
		default:
			return ctx.Err(), false
		}
	}
}
