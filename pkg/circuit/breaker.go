// Package circuit provides a consecutive-failure circuit breaker for outbound RPC.
// 包 circuit 提供用于出站 RPC 的连续失败熔断器。
package circuit

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	DefaultFailureThreshold = 5
	DefaultOpenDuration     = 5 * time.Second
	DefaultHalfOpenProbes   = 1
)

// ErrOpen is returned when the breaker is open and rejects a call.
// 熔断器打开并拒绝调用时返回 ErrOpen。
var ErrOpen = errors.New("circuit breaker is open")

type State int

const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half_open"
	case StateOpen:
		return "open"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

type Breaker struct {
	failureThreshold int
	openDuration     time.Duration
	halfOpenProbes   int
	now              func() time.Time

	mu                  sync.Mutex
	state               State
	consecutiveFailures int
	openedAt            time.Time
	halfOpenInFlight    int
}

func New() *Breaker {
	return NewWithClock(time.Now)
}

func NewWithClock(now func() time.Time) *Breaker {
	if now == nil {
		now = time.Now
	}
	return &Breaker{
		failureThreshold: DefaultFailureThreshold,
		openDuration:     DefaultOpenDuration,
		halfOpenProbes:   DefaultHalfOpenProbes,
		now:              now,
	}
}

func (b *Breaker) Allow() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateOpen:
		if b.now().Sub(b.openedAt) < b.openDuration {
			return ErrOpen
		}
		b.state = StateHalfOpen
		b.halfOpenInFlight = 1
		return nil
	case StateHalfOpen:
		if b.halfOpenInFlight >= b.halfOpenProbes {
			return ErrOpen
		}
		b.halfOpenInFlight++
		return nil
	default:
		return nil
	}
}

func (b *Breaker) Success() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecutiveFailures = 0
	b.halfOpenInFlight = 0
	b.state = StateClosed
}

func (b *Breaker) Failure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateHalfOpen {
		b.openLocked()
		return
	}
	b.consecutiveFailures++
	if b.consecutiveFailures >= b.failureThreshold {
		b.openLocked()
	}
}

func (b *Breaker) State() State {
	if b == nil {
		return StateClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateOpen && b.now().Sub(b.openedAt) >= b.openDuration {
		return StateHalfOpen
	}
	return b.state
}

func (b *Breaker) openLocked() {
	b.state = StateOpen
	b.openedAt = b.now()
	b.halfOpenInFlight = 0
	b.consecutiveFailures = b.failureThreshold
}
