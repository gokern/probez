// Package probez exposes Kubernetes-style health probe endpoints
// (/startupz, /livez, /readyz, /healthz, /status) for long-running services.
package probez

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	defaultStaleAfter   = 30 * time.Second
	defaultStartupGrace = 60 * time.Second
	readHeaderTimeout   = 5 * time.Second
)

type state int32

const (
	stateStarting state = iota
	stateStarted
	stateReady
	stateDraining
)

func (s state) String() string {
	switch s {
	case stateStarting:
		return "starting"
	case stateStarted:
		return "started"
	case stateReady:
		return "ready"
	case stateDraining:
		return "draining"
	default:
		return "unknown"
	}
}

// ReadinessCheck is a named function invoked synchronously on each /readyz request.
// The Check function receives the HTTP request's context and should respect its deadline.
// Return nil if healthy, non-nil error if not ready.
type ReadinessCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// LivenessCheck is a named predicate: true while the process is still working,
// false to have it restarted. It runs on the goroutine serving the request and
// must not block.
type LivenessCheck struct {
	Name  string
	Check func() bool
}

// Probe tracks application lifecycle and serves health endpoints.
type Probe struct {
	state    atomic.Int32
	lastPing atomic.Int64 // UnixNano

	createdAt      time.Time
	host           string
	port           int
	addr           string // resolved after Listen, used by Addr()
	autoLive       bool
	staleAfter     time.Duration
	startupGrace   time.Duration
	logger         *slog.Logger
	checks         []ReadinessCheck
	livenessChecks []LivenessCheck
	server         *http.Server
}

// Option configures a Probe.
type Option func(*Probe)

// WithHost sets the bind host. Default: "" (all interfaces).
func WithHost(host string) Option {
	return func(p *Probe) { p.host = host }
}

// WithAutoLive disables heartbeat-based liveness. /livez returns 200
// as long as the state is >= started, without requiring Ping() calls.
// Use this for API services where responding to the probe itself proves liveness.
func WithAutoLive() Option {
	return func(p *Probe) { p.autoLive = true }
}

// WithStaleAfter sets the max heartbeat age before /livez returns 503.
// Default: 30s.
func WithStaleAfter(d time.Duration) Option {
	return func(p *Probe) { p.staleAfter = d }
}

// WithStartupGrace sets how long /livez returns 200 without any Ping().
// The grace period is measured from Probe creation, not from MarkStarted().
// Default: 60s.
func WithStartupGrace(d time.Duration) Option {
	return func(p *Probe) { p.startupGrace = d }
}

// WithLogger sets the logger. Default: slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(p *Probe) { p.logger = l }
}

// WithReadinessCheck registers a check called on each /readyz request.
func WithReadinessCheck(name string, fn func(ctx context.Context) error) Option {
	return func(p *Probe) {
		p.checks = append(p.checks, ReadinessCheck{Name: name, Check: fn})
	}
}

// WithLivenessCheck registers a predicate consulted on every liveness decision.
// The process is live only while all of them return true, on top of whatever
// the heartbeat already says.
//
// The predicate runs on more endpoints than the name suggests: /livez, /readyz
// and /healthz through it, and /status, which reports each check by name. Keep
// it to reading a value something else has already computed -- that is also why
// it takes no context and returns no error. A liveness probe that consults a
// dependency turns that dependency's outage into a restart of every replica at
// once.
//
// Pair it with WithAutoLive unless the process also calls Ping. Otherwise the
// heartbeat expires on its own schedule and delays what the check already knows.
func WithLivenessCheck(name string, fn func() bool) Option {
	return func(p *Probe) {
		p.livenessChecks = append(p.livenessChecks, LivenessCheck{Name: name, Check: fn})
	}
}

// newProbe creates a Probe with defaults and applies options.
// Does not start the HTTP server (used in tests).
func newProbe(port int, opts ...Option) *Probe {
	p := &Probe{
		createdAt:    time.Now(),
		port:         port,
		staleAfter:   defaultStaleAfter,
		startupGrace: defaultStartupGrace,
		logger:       slog.Default(),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// New creates a Probe, starts the HTTP server on the given port, and returns it.
// Use WithHost to bind to a specific interface (default: all interfaces).
// Returns an error if the address cannot be bound.
func New(port int, opts ...Option) (*Probe, error) {
	p := newProbe(port, opts...)

	addr := fmt.Sprintf("%s:%d", p.host, p.port)

	var lc net.ListenConfig

	listener, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("probez: listen %s: %w", addr, err)
	}

	p.addr = listener.Addr().String()

	srv := &http.Server{
		Handler:           p.newMux(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	p.server = srv

	go func() {
		serveErr := srv.Serve(listener)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			p.logger.Error("probez: server error", "err", serveErr)
		}
	}()

	p.logger.Info("probez: listening", "addr", p.addr)

	return p, nil
}

// Addr returns the actual address the server is listening on.
// Useful when port 0 is used to get an ephemeral port.
func (p *Probe) Addr() string {
	return p.addr
}

// Close gracefully stops the HTTP server.
func (p *Probe) Close(ctx context.Context) error {
	if p.server == nil {
		return nil
	}

	p.logger.Info("probez: closing")

	err := p.server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("probez: close: %w", err)
	}

	return nil
}

// MarkStarted signals that initialization is complete.
// /startupz will return 200 after this call.
func (p *Probe) MarkStarted() {
	p.transition(stateStarted)
	p.logger.Info("probez: marked started")
}

// MarkReady signals that the service is ready to accept work.
// Implicitly marks the service as started if not already.
// /readyz will return 200 after this call.
func (p *Probe) MarkReady() {
	p.transition(stateReady)
	p.logger.Info("probez: marked ready")
}

// Ping updates the heartbeat timestamp. Call this periodically
// from your main loop to signal liveness.
func (p *Probe) Ping() {
	p.lastPing.Store(time.Now().UnixNano())
}

// Shutdown transitions to draining state. /readyz returns 503,
// /livez continues to respond based on heartbeat.
// The HTTP server is NOT stopped -- it dies with the process.
func (p *Probe) Shutdown() {
	p.transition(stateDraining)
	p.logger.Info("probez: shutting down")
}

// transition moves to the target state only if it's forward.
func (p *Probe) transition(target state) {
	for {
		cur := state(p.state.Load())
		if cur >= target {
			return
		}

		if p.state.CompareAndSwap(int32(cur), int32(target)) {
			return
		}
	}
}

// currentState returns the current state.
func (p *Probe) currentState() state {
	return state(p.state.Load())
}

// isLive checks liveness logic: state >= started AND every registered check
// AND (autoLive OR in grace OR heartbeat fresh).
func (p *Probe) isLive() bool {
	if p.currentState() < stateStarted {
		return false
	}

	// A check that says the work has stopped outranks anything the process says
	// about itself, autoLive included.
	for _, c := range p.livenessChecks {
		if !c.Check() {
			return false
		}
	}

	if p.autoLive {
		return true
	}

	last := p.lastPing.Load()
	if last == 0 {
		// No ping yet -- use grace period from creation time.
		return time.Since(p.createdAt) <= p.startupGrace
	}

	return time.Since(time.Unix(0, last)) <= p.staleAfter
}

// isReady checks readiness logic: state == ready AND alive AND checks pass.
func (p *Probe) isReady(ctx context.Context) bool {
	if p.currentState() != stateReady {
		return false
	}

	if !p.isLive() {
		return false
	}

	for _, c := range p.checks {
		err := c.Check(ctx)
		if err != nil {
			return false
		}
	}

	return true
}
