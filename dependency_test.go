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

		app.Use("/", func(ctx *Context) error {
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
		app.Use("/", func(ctx *Context) error {
			seen = append(seen, ctx.GetDependency[string]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, []string{"shared", "shared"}, seen)
	})

	t.Run("replace same type", func(t *testing.T) {
		app := New()
		app.AddDependency("a")
		app.AddDependency("b")

		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "b", ctx.GetDependency[string]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("pointer identity", func(t *testing.T) {
		app := New()
		db := &testDB{dsn: "postgres://localhost"}
		app.AddDependency(db)

		app.Use("/", func(ctx *Context) error {
			require.Same(t, db, ctx.GetDependency[*testDB]())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("missing panics", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.GetDependency[string]() })
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("wrong type panics", func(t *testing.T) {
		app := New()
		app.AddDependency(42)

		app.Use("/", func(ctx *Context) error {
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
