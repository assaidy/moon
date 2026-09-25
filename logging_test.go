package moon

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

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

func testLoggedApp(logs *captureLogHandler) *App {
	return New(WithLogger(slog.New(logs)), WithRequestLogging(true))
}

func TestRegisterRequestLoggingEntry(t *testing.T) {
	t.Run("empty key panics", func(t *testing.T) {
		app := New()
		require.Panics(t, func() {
			app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "", ValueFunc: func(ctx *Context) string { return "" }})
		})
	})

	t.Run("whitespace key panics", func(t *testing.T) {
		app := New()
		require.Panics(t, func() {
			app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "   ", ValueFunc: func(ctx *Context) string { return "" }})
		})
	})

	t.Run("nil value func panics", func(t *testing.T) {
		app := New()
		require.Panics(t, func() {
			app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "custom", ValueFunc: nil})
		})
	})

	t.Run("duplicate key panics", func(t *testing.T) {
		app := New()
		valueFunc := func(ctx *Context) string { return "" }
		app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "custom", ValueFunc: valueFunc})
		require.Panics(t, func() {
			app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "custom", ValueFunc: valueFunc})
		})
	})

	t.Run("reserved key panics", func(t *testing.T) {
		app := New()
		require.Panics(t, func() {
			app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "status", ValueFunc: func(ctx *Context) string { return "" }})
		})
	})

	t.Run("key is trimmed", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)
		app.RegisterRequestLoggingEntry(RequestLoggingEntry{Key: "  custom  ", ValueFunc: func(ctx *Context) string { return "v" }})
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, "v", loggedAttrs(t, logs)["custom"])
	})
}

func TestRegisterRequestLoggingEntry_Concurrent(t *testing.T) {
	app := New()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.RegisterRequestLoggingEntry(RequestLoggingEntry{
				Key:       "concurrent-" + strconv.Itoa(i),
				ValueFunc: func(ctx *Context) string { return "" },
			})
		}()
	}
	wg.Wait()

	me := app
	me.requestLoggingEntriesMutex.RLock()
	count := len(me.registeredRequestLoggingEntries)
	me.requestLoggingEntriesMutex.RUnlock()
	require.Equal(t, 6+8, count)
}

func TestRegisterRequestLoggingEntry_PerApp(t *testing.T) {
	newApp := func(logs *captureLogHandler, value string) *App {
		app := testLoggedApp(logs)
		app.RegisterRequestLoggingEntry(RequestLoggingEntry{
			Key:       "shared",
			ValueFunc: func(ctx *Context) string { return value },
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

func TestLogRequest_Entries(t *testing.T) {
	logs := &captureLogHandler{}
	app := testLoggedApp(logs)
	app.RegisterRequestLoggingEntry(RequestLoggingEntry{
		Key:       "custom",
		ValueFunc: func(ctx *Context) string { return "v" },
	})
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		return ctx.Write(http.StatusCreated, "hello")
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	attrs := loggedAttrs(t, logs)
	for _, key := range []string{"duration", "client", "method", "path", "status", "error", "custom"} {
		require.Contains(t, attrs, key)
	}
	require.Equal(t, http.MethodGet, attrs["method"])
	require.Equal(t, "/x", attrs["path"])
	require.Equal(t, "201", attrs["status"])
	require.Equal(t, "v", attrs["custom"])
}

func TestContextRegisterRequestLoggingEntry(t *testing.T) {
	logs := &captureLogHandler{}
	app := testLoggedApp(logs)
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		ctx.RegisterRequestLoggingEntry(RequestLoggingEntry{
			Key:       "from-ctx",
			ValueFunc: func(ctx *Context) string { return "v" },
		})
		return nil
	})

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, "v", loggedAttrs(t, logs)["from-ctx"])
}
