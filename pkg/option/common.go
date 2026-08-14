package option

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	minPort = 1
	maxPort = 65535
)

func join(errs ...error) error {
	var result error
	for _, err := range errs {
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func require(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func nonNegativeDuration(name string, value time.Duration) error {
	if value < 0 {
		return fmt.Errorf("%s must be >= 0, got %s", name, value)
	}
	return nil
}

func positiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", name, value)
	}
	return nil
}

func validateListenAddress(name, address string) error {
	if err := require(name, address); err != nil {
		return err
	}
	_, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s must be a host:port address: %w", name, err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < minPort || port > maxPort {
		return fmt.Errorf("%s port must be between %d and %d, got %q", name, minPort, maxPort, rawPort)
	}
	return nil
}

func validateEndpoint(name, address string) error {
	if err := require(name, address); err != nil {
		return err
	}
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s must be a host:port address: %w", name, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%s host is required", name)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < minPort || port > maxPort {
		return fmt.Errorf("%s port must be between %d and %d, got %q", name, minPort, maxPort, rawPort)
	}
	return nil
}

// RejectLoopbackEndpoint rejects localhost and loopback IP dial targets.
// 拒绝 localhost 与环回 IP 作为拨号目标。
func RejectLoopbackEndpoint(name, address string) error {
	if err := validateEndpoint(name, address); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s must be a host:port address: %w", name, err)
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("production %s must not use localhost", name)
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return fmt.Errorf("production %s must not use a loopback address", name)
	}
	return nil
}

func newValueError(name, expectation string, value any) error {
	return fmt.Errorf("%s must be %s, got %v", name, expectation, value)
}
