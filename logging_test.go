package moon

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func loggedAttrs(t *testing.T, h *captureLogHandler) map[string]any {
	t.Helper()
	require.Len(t, h.records, 1)
	attrs := make(map[string]any)
	h.records[0].Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

func loggedAttrKeys(t *testing.T, h *captureLogHandler) []string {
	t.Helper()
	require.Len(t, h.records, 1)
	var keys []string
	h.records[0].Attrs(func(a slog.Attr) bool {
		keys = append(keys, a.Key)
		return true
	})
	return keys
}

func testLoggedApp(logs *captureLogHandler) *App {
	return New(NewAppOptions().WithLogger(slog.New(logs)).WithRequestLogging(true))
}

func testEntryValue(v string) RequestLoggingEntryValue {
	return func(ctx *Context, err error) any { return v }
}

func requestLoggingEntryKeys(entries []RequestLoggingEntry) []string {
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = e.Key
	}
	return keys
}

func TestAddRequestLoggingEntry(t *testing.T) {
	t.Run("empty key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New().AddRequestLoggingEntry(RequestLoggingEntry{Key: "", Value: testEntryValue("v")})
		})
	})

	t.Run("whitespace key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New().AddRequestLoggingEntry(RequestLoggingEntry{Key: "   ", Value: testEntryValue("v")})
		})
	})

	t.Run("nil value panics", func(t *testing.T) {
		require.PanicsWithValue(t, "value func cannot be nil", func() {
			New().AddRequestLoggingEntry(RequestLoggingEntry{Key: "custom", Value: nil})
		})
	})

	t.Run("duplicate key panics", func(t *testing.T) {
		app := New()
		app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "custom", Value: testEntryValue("v")})

		require.Panics(t, func() {
			app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "custom", Value: testEntryValue("v")})
		})
	})

	t.Run("default key panics while it is in the list", func(t *testing.T) {
		require.Panics(t, func() {
			New().AddRequestLoggingEntry(RequestLoggingEntry{Key: "status", Value: testEntryValue("v")})
		})
	})

	t.Run("key is trimmed", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)
		app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "  custom  ", Value: testEntryValue("v")})
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, "v", loggedAttrs(t, logs)["custom"])
	})

	t.Run("default key is allowed after a reset", func(t *testing.T) {
		app := New(NewAppOptions().WithRequestLoggingEntries([]RequestLoggingEntry{
			{Key: "method", Value: testEntryValue("v")},
		}))

		require.NotPanics(t, func() {
			app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "status", Value: testEntryValue("v")})
		})
		require.Equal(t, []string{"method", "status"},
			requestLoggingEntryKeys(app.options.requestLoggingEntries))
	})
}

func TestAddRequestLoggingEntry_AfterStartPanics(t *testing.T) {
	addr := freePort(t)
	app := New(NewAppOptions().WithListenAddress(addr))
	app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })
	require.False(t, app.started)

	errCh := make(chan error, 1)
	go func() { errCh <- app.Start() }()
	waitServing(t, addr)

	require.NoError(t, app.Shutdown())
	require.NoError(t, <-errCh)

	require.True(t, app.started)
	require.PanicsWithValue(t, "cannot add request logging entries after the app started", func() {
		app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "custom", Value: testEntryValue("v")})
	})
}

func TestAddRequestLoggingEntry_PerApp(t *testing.T) {
	newApp := func(logs *captureLogHandler, value string) *App {
		app := testLoggedApp(logs)
		app.AddRequestLoggingEntry(RequestLoggingEntry{
			Key:   "shared",
			Value: testEntryValue(value),
		})
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })
		return app
	}

	logs1 := &captureLogHandler{}
	logs2 := &captureLogHandler{}
	app1 := newApp(logs1, "one")
	app2 := newApp(logs2, "two")

	require.NotPanics(t, func() {
		app1.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		app2.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	})
	require.Equal(t, "one", loggedAttrs(t, logs1)["shared"])
	require.Equal(t, "two", loggedAttrs(t, logs2)["shared"])
}

func TestAddRequestLoggingEntry_KeepsAppsAndDefaultsIsolated(t *testing.T) {
	opts := NewAppOptions()
	first := New(opts)
	second := New(opts)

	first.AddRequestLoggingEntry(RequestLoggingEntry{Key: "only-first", Value: testEntryValue("v")})

	require.Contains(t, requestLoggingEntryKeys(first.options.requestLoggingEntries), "only-first")
	require.NotContains(t, requestLoggingEntryKeys(second.options.requestLoggingEntries), "only-first")
	require.NotContains(t, requestLoggingEntryKeys(DefaultRequestLoggingEntries), "only-first")
}

func TestLogRequest_Entries(t *testing.T) {
	logs := &captureLogHandler{}
	app := testLoggedApp(logs)
	app.AddRequestLoggingEntry(RequestLoggingEntry{
		Key:   "custom",
		Value: testEntryValue("v"),
	})
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		return ctx.Write(http.StatusCreated, "hello")
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	require.Equal(t,
		[]string{"duration", "remote", "method", "path", "status", "error", "custom"},
		loggedAttrKeys(t, logs),
	)

	attrs := loggedAttrs(t, logs)
	require.Equal(t, http.MethodGet, attrs["method"])
	require.Equal(t, "/x", attrs["path"])
	require.Equal(t, int64(http.StatusCreated), attrs["status"])
	require.Equal(t, "v", attrs["custom"])
	require.NotEmpty(t, attrs["remote"])
	require.IsType(t, time.Duration(0), attrs["duration"])
	require.Greater(t, attrs["duration"].(time.Duration), time.Duration(0))
	require.Nil(t, attrs["error"])
}

func TestLogRequest_Error(t *testing.T) {
	t.Run("logs the error the error handler returned", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			return ErrNotFound.WithDetails("id 123")
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, ErrNotFound.WithDetails("id 123"), loggedAttrs(t, logs)["error"])
	})

	t.Run("logs nil when the chain succeeds", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Nil(t, loggedAttrs(t, logs)["error"])
	})

	t.Run("logs a non-Error failure as internal_server_error", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return errors.New("boom") })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		logged, ok := loggedAttrs(t, logs)["error"].(Error)
		require.True(t, ok)
		require.Equal(t, ErrInternalServerError.Kind, logged.Kind)

		details, ok := logged.Details.(error)
		require.True(t, ok)
		require.EqualError(t, details, "boom")
	})

	t.Run("logs what a custom error handler returns", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(NewAppOptions().
			WithLogger(slog.New(logs)).
			WithRequestLogging(true).
			WithErrorHandler(func(ctx *Context, err error) error {
				ctx.WriteStatus(http.StatusTeapot)
				return nil
			}))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return ErrNotFound })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		attrs := loggedAttrs(t, logs)
		require.Nil(t, attrs["error"])
		require.Equal(t, int64(http.StatusTeapot), attrs["status"])
	})
}
