// Package health provides concurrency-safe liveness and readiness checks.
package health

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrNotServing = errors.New("service is starting or draining")

type Check func(context.Context) error

type Registry struct {
	mu        sync.RWMutex
	readiness map[string]Check
	liveness  map[string]Check
	serving   atomic.Bool
}

func NewRegistry() *Registry {
	return &Registry{
		readiness: make(map[string]Check),
		liveness:  make(map[string]Check),
	}
}

// Add registers a readiness check. It is the compatibility shorthand for
// AddReadiness.
func (r *Registry) Add(name string, check Check) error {
	return r.AddReadiness(name, check)
}

func (r *Registry) AddReadiness(name string, check Check) error {
	if r == nil {
		return errors.New("add readiness check: registry is nil")
	}
	return r.add(r.readiness, "readiness", name, check)
}

func (r *Registry) AddLiveness(name string, check Check) error {
	if r == nil {
		return errors.New("add liveness check: registry is nil")
	}
	return r.add(r.liveness, "liveness", name, check)
}

func (r *Registry) add(target map[string]Check, kind, name string, check Check) error {
	if r == nil {
		return fmt.Errorf("add %s check: registry is nil", kind)
	}
	name = strings.TrimSpace(name)
	if name == "" || check == nil {
		return fmt.Errorf("add %s check: name and check are required", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := target[name]; exists {
		return fmt.Errorf("add %s check: duplicate name %q", kind, name)
	}
	target[name] = check
	return nil
}

func (r *Registry) SetServing(serving bool) {
	if r != nil {
		r.serving.Store(serving)
	}
}

func (r *Registry) Serving() bool {
	return r != nil && r.serving.Load()
}

func (r *Registry) Ready(ctx context.Context) error {
	if r == nil || !r.serving.Load() {
		return ErrNotServing
	}
	return runChecks(ctx, r.snapshot(r.readiness))
}

func (r *Registry) Live(ctx context.Context) error {
	if r == nil {
		return errors.New("health registry is nil")
	}
	return runChecks(ctx, r.snapshot(r.liveness))
}

func (r *Registry) snapshot(source map[string]Check) map[string]Check {
	r.mu.RLock()
	defer r.mu.RUnlock()
	checks := make(map[string]Check, len(source))
	for name, check := range source {
		checks[name] = check
	}
	return checks
}

func runChecks(ctx context.Context, checks map[string]Check) error {
	if ctx == nil {
		ctx = context.Background()
	}
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)

	var joined error
	for _, name := range names {
		if err := safeCheck(ctx, checks[name]); err != nil {
			joined = errors.Join(joined, fmt.Errorf("%s: %w", name, err))
		}
	}
	return joined
}

func safeCheck(ctx context.Context, check Check) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("health check panicked: %v", recovered)
		}
	}()
	return check(ctx)
}
