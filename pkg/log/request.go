package log

import "sync/atomic"

// RequestControl owns process-wide switches used by transport access logs.
type RequestControl struct {
	healthCheckRequests atomic.Bool
}

func NewRequestControl(healthCheckRequests bool) *RequestControl {
	control := new(RequestControl)
	control.healthCheckRequests.Store(healthCheckRequests)
	return control
}

func (c *RequestControl) HealthCheckRequests() bool {
	return c == nil || c.healthCheckRequests.Load()
}

func (c *RequestControl) SetHealthCheckRequests(enabled bool) {
	if c != nil {
		c.healthCheckRequests.Store(enabled)
	}
}
