package moon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubBase struct {
	name       string
	ready      bool
	stopOrder  *[]string
	startErr   error
	stopErr    error
	startCalls atomic.Int32
	stopCalls  atomic.Int32
}

func (s *stubBase) Name() string { return s.name }

func (s *stubBase) IsReady() bool { return s.ready }

func (s *stubBase) Start() error {
	s.startCalls.Add(1)
	return s.startErr
}

func (s *stubBase) Stop() error {
	s.stopCalls.Add(1)
	if s.stopOrder != nil {
		*s.stopOrder = append(*s.stopOrder, s.name)
	}
	return s.stopErr
}

type stubServiceA struct{ *stubBase }
type stubServiceB struct{ *stubBase }

func newStubA() *stubServiceA { return &stubServiceA{&stubBase{name: "a", ready: true}} }
func newStubB() *stubServiceB { return &stubServiceB{&stubBase{name: "b", ready: true}} }

func TestService_AddGet(t *testing.T) {
	t.Run("nil panics", func(t *testing.T) {
		app := New()
		var nilSvc *stubServiceA
		require.PanicsWithValue(t, "service cannot be nil", func() {
			app.AddService(nilSvc)
		})
	})

	t.Run("duplicate type panics", func(t *testing.T) {
		app := New()
		app.AddService(newStubA())
		require.PanicsWithValue(t, "service of type: *moon.stubServiceA is already registered", func() {
			app.AddService(newStubA())
		})
	})

	t.Run("get before start panics", func(t *testing.T) {
		app := New()
		app.AddService(newStubA())

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetService[*stubServiceA]() })
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("get after start returns instance", func(t *testing.T) {
		app := New()
		svc := newStubA()
		app.AddService(svc)
		require.NoError(t, app.StartServices())

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Same(t, svc, ctx.GetService[*stubServiceA]())
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("missing type panics", func(t *testing.T) {
		app := New()
		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetService[*stubServiceA]() })
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

type stubReadiness struct {
	name  string
	ready bool
}

func (s stubReadiness) Name() string { return s.name }
func (s stubReadiness) Start() error { return nil }
func (s stubReadiness) Stop() error  { return nil }
func (s stubReadiness) IsReady() bool {
	return s.ready
}

func TestCheckServicesReadiness(t *testing.T) {
	newReady := func(ready bool) stubReadiness {
		return stubReadiness{name: "s", ready: ready}
	}

	t.Run("no services", func(t *testing.T) {
		require.True(t, CheckServicesReadiness())
	})

	t.Run("all ready", func(t *testing.T) {
		require.True(t, CheckServicesReadiness(newReady(true), newReady(true)))
	})

	t.Run("one not ready", func(t *testing.T) {
		require.False(t, CheckServicesReadiness(newReady(true), newReady(false)))
	})

	t.Run("nil service", func(t *testing.T) {
		require.False(t, CheckServicesReadiness(newReady(true), nil))
	})
}

func TestApp_CheckServicesReadiness(t *testing.T) {
	t.Run("no services", func(t *testing.T) {
		require.True(t, New().CheckServicesReadiness())
	})

	t.Run("all started and ready", func(t *testing.T) {
		app := New()
		app.AddService(newStubA())
		app.AddService(newStubB())
		require.NoError(t, app.StartServices())
		t.Cleanup(app.StopServices)

		require.True(t, app.CheckServicesReadiness())
	})

	t.Run("registered but not started", func(t *testing.T) {
		app := New()
		app.AddService(newStubA())

		require.False(t, app.CheckServicesReadiness())
	})

	t.Run("one not ready", func(t *testing.T) {
		app := New()
		a, b := newStubA(), newStubB()
		app.AddService(a)
		app.AddService(b)
		require.NoError(t, app.StartServices())
		t.Cleanup(app.StopServices)
		b.ready = false

		require.False(t, app.CheckServicesReadiness())
	})
}

func TestService_StopOrder(t *testing.T) {
	app := New()
	var order []string
	a, b := newStubA(), newStubB()
	a.stopOrder = &order
	b.stopOrder = &order
	app.AddService(a)
	app.AddService(b)
	require.NoError(t, app.StartServices())

	app.StopServices()

	require.Equal(t, []string{"b", "a"}, order)
}

func TestService_StartStop(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		app := New()
		a, b := newStubA(), newStubB()
		app.AddService(a)
		app.AddService(b)
		require.NoError(t, app.StartServices())

		require.Equal(t, int32(1), a.startCalls.Load())
		require.Equal(t, int32(1), b.startCalls.Load())
		require.Equal(t, int32(0), a.stopCalls.Load())
		require.Equal(t, int32(0), b.stopCalls.Load())

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Same(t, a, ctx.GetService[*stubServiceA]())
			require.Same(t, b, ctx.GetService[*stubServiceB]())
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("already started services are skipped", func(t *testing.T) {
		app := New()
		a, b := newStubA(), newStubB()
		app.AddService(a)
		app.AddService(b)
		require.NoError(t, app.StartServices())
		require.NoError(t, app.StartServices())

		require.Equal(t, int32(1), a.startCalls.Load())
		require.Equal(t, int32(1), b.startCalls.Load())
	})

	t.Run("stopped services start again", func(t *testing.T) {
		app := New()
		a := newStubA()
		app.AddService(a)
		require.NoError(t, app.StartServices())

		app.StopServices()
		require.NoError(t, app.StartServices())
		require.Equal(t, int32(2), a.startCalls.Load())
	})

	t.Run("start failure aborts and discards", func(t *testing.T) {
		startBoom := errors.New("start boom")
		app := New()
		a, b := newStubA(), newStubB()
		b.startErr = startBoom
		app.AddService(a)
		app.AddService(b)

		err := app.StartServices()
		require.ErrorIs(t, err, startBoom)
		require.ErrorContains(t, err, `failed to start "b" service`)

		// failed service never started, never stopped, never visible
		require.Equal(t, int32(1), b.startCalls.Load())
		require.Equal(t, int32(0), b.stopCalls.Load())

		// the other service was started then stopped and discarded along the abort
		require.Equal(t, int32(1), a.startCalls.Load())
		require.Equal(t, int32(1), a.stopCalls.Load())

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetService[*stubServiceA]() })
			require.Panics(t, func() { ctx.GetService[*stubServiceB]() })
			return nil
		})
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("stop failure swallowed", func(t *testing.T) {
		app := New()
		a := newStubA()
		a.stopErr = errors.New("stop boom")
		app.AddService(a)
		require.NoError(t, app.StartServices())

		app.StopServices()
		require.Equal(t, int32(1), a.stopCalls.Load())
		require.True(t, app.CheckServicesReadiness())
	})

	t.Run("stop without start is clean", func(t *testing.T) {
		app := New()
		a := newStubA()
		app.AddService(a)

		app.StopServices()
		require.Equal(t, int32(0), a.stopCalls.Load())
	})

	t.Run("shutdown tolerates stop error", func(t *testing.T) {
		app := New()
		a := newStubA()
		a.stopErr = errors.New("stop boom")
		app.AddService(a)
		require.NoError(t, app.StartServices())

		require.NoError(t, app.Shutdown())
		require.Equal(t, int32(1), a.stopCalls.Load())
	})

	t.Run("shutdown clean", func(t *testing.T) {
		app := New()
		a := newStubA()
		app.AddService(a)
		require.NoError(t, app.StartServices())

		require.NoError(t, app.Shutdown())
		require.Equal(t, int32(1), a.stopCalls.Load())
	})
}
