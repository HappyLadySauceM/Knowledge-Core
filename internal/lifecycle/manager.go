package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type Hook struct {
	Name  string
	Start func(context.Context) error
	Stop  func(context.Context) error
}

type Manager struct {
	mu      sync.Mutex
	hooks   []Hook
	started int
	running bool
}

func (m *Manager) Add(hook Hook) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("add lifecycle hook: manager already started")
	}
	if hook.Name == "" {
		return errors.New("add lifecycle hook: name is required")
	}
	for _, existing := range m.hooks {
		if existing.Name == hook.Name {
			return fmt.Errorf("add lifecycle hook: duplicate name %q", hook.Name)
		}
	}
	m.hooks = append(m.hooks, hook)
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return errors.New("start lifecycle: manager already started")
	}
	m.running = true
	for index, hook := range m.hooks {
		if hook.Start != nil {
			if err := hook.Start(ctx); err != nil {
				m.started = index
				rollbackErr := m.stopLocked(ctx)
				return errors.Join(fmt.Errorf("start lifecycle hook %q: %w", hook.Name, err), rollbackErr)
			}
		}
		m.started = index + 1
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

func (m *Manager) stopLocked(ctx context.Context) error {
	var joined error
	for index := m.started - 1; index >= 0; index-- {
		hook := m.hooks[index]
		if hook.Stop != nil {
			if err := hook.Stop(ctx); err != nil {
				joined = errors.Join(joined, fmt.Errorf("stop lifecycle hook %q: %w", hook.Name, err))
			}
		}
	}
	m.started = 0
	m.running = false
	return joined
}
