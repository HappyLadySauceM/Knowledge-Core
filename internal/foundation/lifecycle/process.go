package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

type Process struct {
	SetServing func(bool)
	Serve      func() error
	Shutdown   func(context.Context) error
	Close      func(context.Context) error
}

func Run(ctx context.Context, shutdownTimeout time.Duration, process Process) error {
	if ctx == nil {
		return errors.New("run process: context is required")
	}
	if shutdownTimeout <= 0 {
		return errors.New("run process: shutdown timeout must be positive")
	}
	if process.Serve == nil {
		return errors.New("run process: serve function is required")
	}

	serveErrCh := make(chan error, 1)
	setServing(process.SetServing, true)
	go func() { serveErrCh <- process.Serve() }()

	var serveErr error
	stopping := false
	select {
	case serveErr = <-serveErrCh:
	case <-ctx.Done():
		stopping = true
	}

	setServing(process.SetServing, false)
	shutdownErr, waitErr := stopTransport(process, serveErrCh, stopping, shutdownTimeout)
	closeErr := closeResources(process.Close, shutdownTimeout)
	if stopping && isExpectedStopError(serveErr) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr, waitErr, closeErr)
}

func setServing(set func(bool), serving bool) {
	if set != nil {
		set(serving)
	}
}

func stopTransport(process Process, serveErrCh <-chan error, wait bool, timeout time.Duration) (shutdownErr, waitErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if process.Shutdown != nil {
		shutdownErr = process.Shutdown(ctx)
	}
	if !wait {
		return shutdownErr, nil
	}
	select {
	case err := <-serveErrCh:
		if !isExpectedStopError(err) {
			waitErr = err
		}
	case <-ctx.Done():
		waitErr = fmt.Errorf("wait for transport shutdown: %w", ctx.Err())
	}
	return shutdownErr, waitErr
}

func closeResources(closeFunc func(context.Context) error, timeout time.Duration) error {
	if closeFunc == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return closeFunc(ctx)
}

func isExpectedStopError(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed)
}
