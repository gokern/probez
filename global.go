package probez

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

var defaultProbe atomic.Pointer[Probe] //nolint:gochecknoglobals // singleton by design

var errAlreadyStarted = errors.New("probez: already started")

// Start creates the default Probe, starts the HTTP server, and stores it
// as the package-level default. Call once from main().
// Returns an error if called more than once.
func Start(port int, opts ...Option) error {
	p, err := New(port, opts...)
	if err != nil {
		return err
	}

	if !defaultProbe.CompareAndSwap(nil, p) {
		_ = p.Close(context.Background())

		return errAlreadyStarted
	}

	return nil
}

// Default returns the package-level Probe set by Start.
// Panics if Start has not been called.
func Default() *Probe {
	p := defaultProbe.Load()
	if p == nil {
		panic("probez: call Start() first")
	}

	return p
}

// Close gracefully stops the default Probe's HTTP server.
func Close(ctx context.Context) error {
	p := defaultProbe.Swap(nil)
	if p == nil {
		return nil
	}

	err := p.Close(ctx)
	if err != nil {
		return fmt.Errorf("probez: %w", err)
	}

	return nil
}

// MarkStarted signals that initialization is complete.
func MarkStarted() { Default().MarkStarted() }

// MarkReady signals that the service is ready for work.
func MarkReady() { Default().MarkReady() }

// Ping updates the heartbeat timestamp.
func Ping() { Default().Ping() }

// Shutdown transitions to draining state.
func Shutdown() { Default().Shutdown() }

// Addr returns the address the default Probe is listening on.
func Addr() string { return Default().Addr() }
