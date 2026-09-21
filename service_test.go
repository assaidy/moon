package moon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubBase struct {
	name       string
	startErr   error
	stopErr    error
	startBlock time.Duration
	stopBlock  time.Duration
	startCalls atomic.Int32
	stopCalls  atomic.Int32
	startCtx   context.Context
	stopCtx    context.Context
}

func (s *stubBase) Name() string { return s.name }

func (s *stubBase) IsAvailable() bool { return true }

func (s *stubBase) Start(ctx context.Context) error {
	s.startCalls.Add(1)
	s.startCtx = ctx
	if s.startBlock > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.startBlock):
		}
	}
	return s.startErr
}

func (s *stubBase) Stop(ctx context.Context) error {
	s.stopCalls.Add(1)
	s.stopCtx = ctx
	if s.stopBlock > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.stopBlock):
		}
	}
	return s.stopErr
}

type stubServiceA struct{ *stubBase }
type stubServiceB struct{ *stubBase }

func newStubA() *stubServiceA { return &stubServiceA{&stubBase{name: "a"}} }
func newStubB() *stubServiceB { return &stubServiceB{&stubBase{name: "b"}} }

func TestService_AddGet(t *testing.T) {
	t.Run("nil panics", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		var nilSvc *stubServiceA
		require.PanicsWithValue(t, "service cannot be nil", func() {
			app.AddService(nilSvc)
		})
	})

	t.Run("replace same type", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		first, second := newStubA(), newStubA()
		app.AddService(first)
		app.AddService(second)
		require.NoError(t, app.StartServices())

		app.Use("/", func(ctx *Context) error {
			require.Same(t, second, ctx.GetService[*stubServiceA]())
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("get before start panics", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		app.AddService(newStubA())

		app.Use("/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetService[*stubServiceA]() })
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("get after start returns instance", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		svc := newStubA()
		app.AddService(svc)
		require.NoError(t, app.StartServices())

		app.Use("/", func(ctx *Context) error {
			require.Same(t, svc, ctx.GetService[*stubServiceA]())
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("missing type panics", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		app.Use("/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetService[*stubServiceA]() })
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestService_StartStop(t *testing.T) {
	t.Run("sequential happy path", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		a, b := newStubA(), newStubB()
		app.AddService(a)
		app.AddService(b)
		require.NoError(t, app.StartServices())

		require.Equal(t, int32(1), a.startCalls.Load())
		require.Equal(t, int32(1), b.startCalls.Load())
		require.Equal(t, int32(0), a.stopCalls.Load())
		require.Equal(t, int32(0), b.stopCalls.Load())

		app.Use("/", func(ctx *Context) error {
			require.Same(t, a, ctx.GetService[*stubServiceA]())
			require.Same(t, b, ctx.GetService[*stubServiceB]())
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("parallel happy path", func(t *testing.T) {
		app := New(WithRequestLogging(false), WithParallelServiceStart(), WithParallelServiceStop())
		a, b := newStubA(), newStubB()
		app.AddService(a)
		app.AddService(b)
		require.NoError(t, app.StartServices())

		require.Equal(t, int32(1), a.startCalls.Load())
		require.Equal(t, int32(1), b.startCalls.Load())

		app.StopServices()
		require.Equal(t, int32(1), a.stopCalls.Load())
		require.Equal(t, int32(1), b.stopCalls.Load())
	})

	t.Run("start failure aborts and discards", func(t *testing.T) {
		startBoom := errors.New("start boom")
		app := New(WithRequestLogging(false))
		a, b := newStubA(), newStubB()
		b.startErr = startBoom
		app.AddService(a)
		app.AddService(b)

		err := app.StartServices()
		require.ErrorIs(t, err, startBoom)

		// failed service never started, never stopped, never visible
		require.Equal(t, int32(1), b.startCalls.Load())
		require.Equal(t, int32(0), b.stopCalls.Load())

		// the other service was either never started, or started then
		// stopped and discarded along the abort
		require.Equal(t, a.startCalls.Load(), a.stopCalls.Load())

		app.Use("/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetService[*stubServiceA]() })
			require.Panics(t, func() { ctx.GetService[*stubServiceB]() })
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("parallel start failure", func(t *testing.T) {
		startBoom := errors.New("start boom")
		app := New(WithRequestLogging(false), WithParallelServiceStart())
		a, b := newStubA(), newStubB()
		b.startErr = startBoom
		app.AddService(a)
		app.AddService(b)

		require.ErrorIs(t, app.StartServices(), startBoom)
		require.Equal(t, int32(0), b.stopCalls.Load())
		require.Equal(t, a.startCalls.Load(), a.stopCalls.Load())
	})

	t.Run("start timeout", func(t *testing.T) {
		app := New(WithRequestLogging(false), WithServiceStartTimeout(50*time.Millisecond))
		a := newStubA()
		a.startBlock = time.Second
		app.AddService(a)

		err := app.StartServices()
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NotNil(t, a.startCtx)
		if deadline, ok := a.startCtx.Deadline(); ok {
			require.WithinDuration(t, time.Now(), deadline, time.Second)
		} else {
			t.Fatal("expected start context with deadline")
		}
	})

	t.Run("stop failure swallowed", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		a := newStubA()
		a.stopErr = errors.New("stop boom")
		app.AddService(a)
		require.NoError(t, app.StartServices())

		app.StopServices()
		require.Equal(t, int32(1), a.stopCalls.Load())
	})

	t.Run("parallel stop failure swallowed", func(t *testing.T) {
		app := New(WithRequestLogging(false), WithParallelServiceStop())
		a, b := newStubA(), newStubB()
		a.stopErr = errors.New("stop a")
		b.stopErr = errors.New("stop b")
		app.AddService(a)
		app.AddService(b)
		require.NoError(t, app.StartServices())

		app.StopServices()
		require.Equal(t, int32(1), a.stopCalls.Load())
		require.Equal(t, int32(1), b.stopCalls.Load())
	})

	t.Run("stop timeout enforced", func(t *testing.T) {
		app := New(WithRequestLogging(false), WithServiceStopTimeout(50*time.Millisecond))
		a := newStubA()
		a.stopBlock = time.Second
		app.AddService(a)
		require.NoError(t, app.StartServices())

		app.StopServices()
		require.Equal(t, int32(1), a.stopCalls.Load())
		require.NotNil(t, a.stopCtx)
		_, ok := a.stopCtx.Deadline()
		require.True(t, ok, "expected stop context with deadline")
	})

	t.Run("stop without start is clean", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		a := newStubA()
		app.AddService(a)

		app.StopServices()
		require.Equal(t, int32(0), a.stopCalls.Load())
	})

	t.Run("prefork parent starts nothing", func(t *testing.T) {
		t.Setenv(preforkChildEnv, "")
		app := New(WithRequestLogging(false), WithPrefork(true))
		a := newStubA()
		app.AddService(a)

		require.NoError(t, app.StartServices())
		require.Equal(t, int32(0), a.startCalls.Load())
		app.StopServices()
		require.Equal(t, int32(0), a.stopCalls.Load())
	})

	t.Run("shutdown tolerates stop error", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		a := newStubA()
		a.stopErr = errors.New("stop boom")
		app.AddService(a)
		require.NoError(t, app.StartServices())

		require.NoError(t, app.Shutdown())
		require.Equal(t, int32(1), a.stopCalls.Load())
	})

	t.Run("shutdown clean", func(t *testing.T) {
		app := New(WithRequestLogging(false))
		a := newStubA()
		app.AddService(a)
		require.NoError(t, app.StartServices())

		require.NoError(t, app.Shutdown())
		require.Equal(t, int32(1), a.stopCalls.Load())
	})
}
