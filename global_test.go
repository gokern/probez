package probez

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func resetDefault() {
	_ = Close(context.Background())
}

func TestDefaultPanicsBeforeStart(t *testing.T) { //nolint:paralleltest // shared global state
	resetDefault()
	defer resetDefault()

	require.Panics(t, func() { Default() })
	require.Panics(t, func() { MarkStarted() })
	require.Panics(t, func() { Ping() })
}

func TestStart(t *testing.T) { //nolint:paralleltest // shared global state
	resetDefault()
	defer resetDefault()

	err := Start(0, WithHost("127.0.0.1"))
	require.NoError(t, err)
	require.NotEmpty(t, Addr())
}

func TestStartDouble(t *testing.T) { //nolint:paralleltest // shared global state
	resetDefault()
	defer resetDefault()

	err := Start(0, WithHost("127.0.0.1"))
	require.NoError(t, err)

	err = Start(0, WithHost("127.0.0.1"))
	require.ErrorIs(t, err, errAlreadyStarted)
}

func TestGlobalClose(t *testing.T) { //nolint:paralleltest // shared global state
	resetDefault()
	defer resetDefault()

	err := Start(0, WithHost("127.0.0.1"))
	require.NoError(t, err)

	err = Close(context.Background())
	require.NoError(t, err)

	// After Close, Start should work again.
	err = Start(0, WithHost("127.0.0.1"))
	require.NoError(t, err)
}

func TestGlobalLifecycle(t *testing.T) { //nolint:paralleltest // shared global state
	resetDefault()
	defer resetDefault()

	err := Start(0, WithHost("127.0.0.1"))
	require.NoError(t, err)

	MarkStarted()
	require.Equal(t, int32(stateStarted), Default().state.Load())

	MarkReady()
	require.Equal(t, int32(stateReady), Default().state.Load())

	Ping()
	require.NotZero(t, Default().lastPing.Load())

	Shutdown()
	require.Equal(t, int32(stateDraining), Default().state.Load())
}
