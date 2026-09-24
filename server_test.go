package moon

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().String()
}

func waitServing(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server did not start serving %s", addr)
}

func TestServer_IsPreforkChild(t *testing.T) {
	t.Setenv(preforkChildEnv, "1")
	require.True(t, IsPreforkChild())

	t.Setenv(preforkChildEnv, "")
	require.False(t, IsPreforkChild())

	t.Setenv(preforkChildEnv, "0")
	require.False(t, IsPreforkChild())
}

func TestServer_StartServiceFailure(t *testing.T) {
	app := New(WithListenAddress("127.0.0.1:0"))
	svc := newStubA()
	svc.startErr = errors.New("start boom")
	app.AddService(svc)

	require.ErrorIs(t, app.Start(), ErrFailedToStartServices)
	require.Equal(t, int32(1), svc.startCalls.Load())
}

func TestServer_StartShutdownLifecycle(t *testing.T) {
	addr := freePort(t)
	app := New(WithListenAddress(addr))
	svc := newStubA()
	app.AddService(svc)

	errCh := make(chan error, 1)
	go func() { errCh <- app.Start() }()
	waitServing(t, addr)

	require.NoError(t, app.Shutdown())
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after Shutdown")
	}

	require.Equal(t, int32(1), svc.startCalls.Load())
	require.Equal(t, int32(1), svc.stopCalls.Load())
}

func TestServer_GetListener(t *testing.T) {
	t.Run("plain binds", func(t *testing.T) {
		app := New()
		l, err := app.getListener("127.0.0.1:0")
		require.NoError(t, err)
		require.NoError(t, l.Close())
	})

	t.Run("prefork binds", func(t *testing.T) {
		app := New(WithPrefork(true))
		l, err := app.getListener("127.0.0.1:0")
		require.NoError(t, err)
		require.NoError(t, l.Close())
	})

	t.Run("invalid address errors", func(t *testing.T) {
		app := New()
		_, err := app.getListener("invalid")
		require.Error(t, err)
	})
}

func TestServer_IgnoreErrServerClosed(t *testing.T) {
	require.NoError(t, ignoreErrServerClosed(nil))
	require.NoError(t, ignoreErrServerClosed(http.ErrServerClosed))
	require.NoError(t, ignoreErrServerClosed(fmt.Errorf("wrap: %w", http.ErrServerClosed)))

	other := errors.New("boom")
	require.Equal(t, other, ignoreErrServerClosed(other))
}

func TestServer_Shutdown(t *testing.T) {
	t.Run("idle stops services", func(t *testing.T) {
		app := New()
		svc := newStubA()
		app.AddService(svc)
		require.NoError(t, app.StartServices())

		require.NoError(t, app.Shutdown())
		require.Equal(t, int32(1), svc.stopCalls.Load())
	})

	t.Run("with shutdown timeout", func(t *testing.T) {
		app := New(WithShutdownTimeout(time.Second))
		svc := newStubA()
		app.AddService(svc)
		require.NoError(t, app.StartServices())

		require.NoError(t, app.Shutdown())
		require.Equal(t, int32(1), svc.stopCalls.Load())
	})

	t.Run("prefork child takes server path", func(t *testing.T) {
		t.Setenv(preforkChildEnv, "1")
		app := New(WithPrefork(true))
		require.NoError(t, app.Shutdown())
	})
}
