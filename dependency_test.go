package moon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDependency(t *testing.T) {
	type testDB struct{ dsn string }

	t.Run("hit distinct types coexist", func(t *testing.T) {
		app := New()
		app.AddDependency("hello")
		app.AddDependency(42)
		app.AddDependency(&testDB{dsn: "postgres://localhost"})

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Equal(t, "hello", ctx.GetDependency[string]())
			require.Equal(t, 42, ctx.GetDependency[int]())
			require.Equal(t, &testDB{dsn: "postgres://localhost"}, ctx.GetDependency[*testDB]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("shared across requests", func(t *testing.T) {
		app := New()
		app.AddDependency("shared")

		var seen []string
		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			seen = append(seen, ctx.GetDependency[string]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, []string{"shared", "shared"}, seen)
	})

	t.Run("duplicate type panics", func(t *testing.T) {
		app := New()
		app.AddDependency("a")
		require.PanicsWithValue(t, "dependency of type: string is already registered", func() {
			app.AddDependency("b")
		})

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Equal(t, "a", ctx.GetDependency[string]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("pointer identity", func(t *testing.T) {
		app := New()
		db := &testDB{dsn: "postgres://localhost"}
		app.AddDependency(db)

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Same(t, db, ctx.GetDependency[*testDB]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("missing panics", func(t *testing.T) {
		app := New()
		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetDependency[string]() })
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("wrong type panics", func(t *testing.T) {
		app := New()
		app.AddDependency(42)

		app.Map(http.MethodGet, "/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetDependency[string]() })
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("nil panics", func(t *testing.T) {
		app := New()
		require.Panics(t, func() { app.AddDependency(nil) })
		require.Panics(t, func() { app.AddDependency((*testDB)(nil)) })
	})
}

func TestInjected_GetDependency(t *testing.T) {
	type config struct{ dsn string }

	t.Run("zero value panics", func(t *testing.T) {
		require.PanicsWithValue(t, "Injected must be supplied by moon", func() {
			Injected{}.GetDependency[config]()
		})
	})

	t.Run("constructor gets registered dependency", func(t *testing.T) {
		app := New()
		cfg := config{dsn: "postgres://localhost"}
		app.AddDependency(cfg)

		app.AddService(func(injected Injected) *stubServiceA {
			require.Equal(t, cfg, injected.GetDependency[config]())
			return newStubA()
		})

		require.NoError(t, app.StartServices())
		t.Cleanup(app.StopServices)
	})

	t.Run("missing dependency panics", func(t *testing.T) {
		app := New()
		app.AddService(func(injected Injected) *stubServiceA {
			require.PanicsWithValue(t, "dependency not found: string", func() {
				injected.GetDependency[string]()
			})
			return newStubA()
		})

		require.NoError(t, app.StartServices())
		t.Cleanup(app.StopServices)
	})
}
