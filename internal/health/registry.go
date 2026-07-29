package health

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type Check func(context.Context) error

type Registry struct {
	mu     sync.RWMutex
	checks map[string]Check
	ready  atomic.Bool
}

func NewRegistry() *Registry {
	return &Registry{checks: make(map[string]Check)}
}

func (r *Registry) Add(name string, check Check) error {
	if name == "" || check == nil {
		return errors.New("add health check: name and check are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.checks[name]; exists {
		return fmt.Errorf("add health check: duplicate name %q", name)
	}
	r.checks[name] = check
	return nil
}

func (r *Registry) SetServing(serving bool) {
	r.ready.Store(serving)
}

func (r *Registry) Ready(ctx context.Context) error {
	if !r.ready.Load() {
		return errors.New("service is draining or starting")
	}
	r.mu.RLock()
	checks := make(map[string]Check, len(r.checks))
	for name, check := range r.checks {
		checks[name] = check
	}
	r.mu.RUnlock()

	var joined error
	for name, check := range checks {
		if err := check(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("%s: %w", name, err))
		}
	}
	return joined
}
