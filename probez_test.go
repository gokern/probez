package probez

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewProbe(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)

		require.Equal(t, int32(stateStarting), p.state.Load())
		require.Equal(t, 30*time.Second, p.staleAfter)
		require.Equal(t, 60*time.Second, p.startupGrace)
	})
}

func TestStateTransitions(t *testing.T) {
	t.Parallel()

	t.Run("forward", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)

		require.Equal(t, int32(stateStarting), p.state.Load())

		p.MarkStarted()
		require.Equal(t, int32(stateStarted), p.state.Load())

		p.MarkReady()
		require.Equal(t, int32(stateReady), p.state.Load())

		p.Shutdown()
		require.Equal(t, int32(stateDraining), p.state.Load())
	})

	t.Run("skip_to_shutdown", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		p.Shutdown()
		require.Equal(t, int32(stateDraining), p.state.Load())
	})

	t.Run("no_backward", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		p.MarkStarted()
		p.MarkReady()

		p.MarkStarted() // should be no-op
		require.Equal(t, int32(stateReady), p.state.Load())
	})

	t.Run("shutdown_idempotent", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		p.MarkStarted()
		p.MarkReady()
		p.Shutdown()
		p.Shutdown() // should not panic
		require.Equal(t, int32(stateDraining), p.state.Load())
	})
}

func TestPing(t *testing.T) {
	t.Parallel()

	t.Run("updates_heartbeat", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		before := p.lastPing.Load()

		time.Sleep(time.Millisecond)
		p.Ping()
		after := p.lastPing.Load()
		require.Greater(t, after, before)
	})
}

func TestHandleStartupz(t *testing.T) {
	t.Parallel()

	t.Run("starting_returns_503", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		rec := httptest.NewRecorder()
		p.handleStartupz(rec, httptest.NewRequest(http.MethodGet, "/startupz", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("started_returns_200", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		p.MarkStarted()

		rec := httptest.NewRecorder()
		p.handleStartupz(rec, httptest.NewRequest(http.MethodGet, "/startupz", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandleLivez(t *testing.T) {
	t.Parallel()

	t.Run("starting_returns_503", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		rec := httptest.NewRecorder()
		p.handleLivez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("in_grace_returns_200", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithStartupGrace(time.Hour))
		p.MarkStarted()

		rec := httptest.NewRecorder()
		p.handleLivez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("stale_heartbeat_returns_503", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithStaleAfter(time.Nanosecond), WithStartupGrace(0))
		p.MarkStarted()
		p.Ping()
		time.Sleep(time.Millisecond)

		rec := httptest.NewRecorder()
		p.handleLivez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("fresh_heartbeat_returns_200", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithStaleAfter(time.Hour), WithStartupGrace(0))
		p.MarkStarted()
		p.Ping()

		rec := httptest.NewRecorder()
		p.handleLivez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestAutoLive(t *testing.T) {
	t.Parallel()

	t.Run("livez_200_without_ping", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithAutoLive(), WithStartupGrace(0))
		p.MarkStarted()

		rec := httptest.NewRecorder()
		p.handleLivez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("livez_503_before_started", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithAutoLive())
		rec := httptest.NewRecorder()
		p.handleLivez(rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("readyz_works_without_ping", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithAutoLive())
		p.MarkStarted()
		p.MarkReady()

		rec := httptest.NewRecorder()
		p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandleReadyz(t *testing.T) {
	t.Parallel()

	t.Run("not_ready_returns_503", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		p.MarkStarted()

		rec := httptest.NewRecorder()
		p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("ready_returns_200", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithStaleAfter(time.Hour), WithStartupGrace(time.Hour))
		p.MarkStarted()
		p.MarkReady()
		p.Ping()

		rec := httptest.NewRecorder()
		p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("draining_returns_503", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithStaleAfter(time.Hour), WithStartupGrace(time.Hour))
		p.MarkStarted()
		p.MarkReady()
		p.Ping()
		p.Shutdown()

		rec := httptest.NewRecorder()
		p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("check_fails_returns_503", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0,
			WithStaleAfter(time.Hour),
			WithStartupGrace(time.Hour),
			WithReadinessCheck("bad", func(_ context.Context) error {
				return errors.New("down")
			}),
		)
		p.MarkStarted()
		p.MarkReady()
		p.Ping()

		rec := httptest.NewRecorder()
		p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestHandleHealthz(t *testing.T) {
	t.Parallel()

	t.Run("aliases_readyz", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0, WithStaleAfter(time.Hour), WithStartupGrace(time.Hour))
		p.MarkStarted()
		p.MarkReady()
		p.Ping()

		rec := httptest.NewRecorder()
		p.handleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandleStatus(t *testing.T) {
	t.Parallel()

	t.Run("always_200_with_json", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		p.MarkStarted()
		p.Ping()

		rec := httptest.NewRecorder()
		p.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
		require.Equal(t, http.StatusOK, rec.Code)

		var body map[string]any

		err := json.NewDecoder(rec.Body).Decode(&body)
		require.NoError(t, err)
		require.Equal(t, "started", body["state"])
		require.Contains(t, body, "uptime_ms")
		require.Contains(t, body, "heartbeat_age_ms")
		require.Contains(t, body, "stale_after_ms")
	})
}

func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()

	t.Run("post_readyz_returns_405", func(t *testing.T) {
		t.Parallel()

		p := newProbe(0)
		rec := httptest.NewRecorder()
		p.newMux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/readyz", nil))
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("starts_http_server", func(t *testing.T) {
		t.Parallel()

		p, err := New(0, WithHost("127.0.0.1"))
		require.NoError(t, err)

		defer func() { _ = p.Close(context.Background()) }()

		// Should respond to /startupz with 503 (still starting)
		resp, err := http.Get("http://" + p.Addr() + "/startupz")
		require.NoError(t, err)

		_ = resp.Body.Close()

		require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

		// Mark started, should return 200
		p.MarkStarted()
		resp, err = http.Get("http://" + p.Addr() + "/startupz")
		require.NoError(t, err)

		_ = resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("invalid_addr", func(t *testing.T) {
		t.Parallel()

		p, err := New(0, WithHost("127.0.0.1"))
		require.NoError(t, err)

		defer func() { _ = p.Close(context.Background()) }()

		// Extract port from p1 and try to bind the same port.
		_, portStr, err := net.SplitHostPort(p.Addr())
		require.NoError(t, err)
		port, err := strconv.Atoi(portStr)
		require.NoError(t, err)

		_, err = New(port, WithHost("127.0.0.1"))
		require.Error(t, err)
	})
}

func TestClose(t *testing.T) {
	t.Parallel()

	t.Run("stops_server", func(t *testing.T) {
		t.Parallel()

		p, err := New(0, WithHost("127.0.0.1"))
		require.NoError(t, err)

		addr := p.Addr()
		err = p.Close(context.Background())
		require.NoError(t, err)

		resp, err := http.Get("http://" + addr + "/livez")
		if err == nil {
			_ = resp.Body.Close()
		}

		require.Error(t, err)
	})
}
