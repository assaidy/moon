package moon

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

// testEntryApp returns an app logging only entry, for the requests sent to
// GET /users/:id, which route serves, plus the handler capturing the
// "request handled" record. A nil route writes 200 with an empty body.
func testEntryApp(t *testing.T, entry RequestLoggingEntry, route Handler) (*App, *captureLogHandler) {
	t.Helper()
	if route == nil {
		route = func(ctx *Context) error { return ctx.Write(http.StatusOK, "ok") }
	}
	logs := &captureLogHandler{}
	app := New(NewAppOptions().
		WithLogger(slog.New(logs)).
		WithRequestLogging(true).
		WithRequestLoggingEntries([]RequestLoggingEntry{entry}))
	app.Map(http.MethodGet, "/users/:id", route)
	return app, logs
}

// loggedAttr returns the logged attribute key asserted to T.
func loggedAttr[T any](t *testing.T, attrs map[string]any, key string) T {
	t.Helper()
	value, ok := attrs[key].(T)
	require.True(t, ok, "attribute %q must be a %T, got %v", key, *new(T), attrs[key])
	return value
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
			requestLoggingEntryKeys(app.options.rle))
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

	require.Contains(t, requestLoggingEntryKeys(first.options.rle), "only-first")
	require.NotContains(t, requestLoggingEntryKeys(second.options.rle), "only-first")
	require.NotContains(t, requestLoggingEntryKeys(DefaultRequestLoggingEntries()), "only-first")
}

// Every call returns a new list, so writing to one result never reaches the
// defaults of another app.
func TestDefaultRequestLoggingEntries_FreshListEachCall(t *testing.T) {
	first := DefaultRequestLoggingEntries()
	second := DefaultRequestLoggingEntries()
	require.Equal(t, requestLoggingEntryKeys(first), requestLoggingEntryKeys(second))

	first[0] = RequestLoggingEntry{Key: "replaced", Value: testEntryValue("v")}

	require.NotEqual(t, requestLoggingEntryKeys(first), requestLoggingEntryKeys(second))
	require.Equal(t,
		[]string{"duration", "remote", "method", "path", "status", "error"},
		requestLoggingEntryKeys(DefaultRequestLoggingEntries()),
	)
}

// A request logs the entries present when it starts, so an entry added while
// a request runs never sees that request.
func TestAddRequestLoggingEntry_DuringRequest(t *testing.T) {
	const preparedLocalKey = "test.prepared_local_key"

	preparedEntry := RequestLoggingEntry{
		Key: "prepared",
		Before: func(ctx *Context) {
			ctx.SetLocal(preparedLocalKey, true)
		},
		Value: func(ctx *Context, err error) any {
			prepared, _ := ctx.GetLocal[bool](preparedLocalKey)
			return prepared
		},
	}

	t.Run("added by a handler logs from the next request", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)

		added := false
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			if !added {
				added = true
				app.AddRequestLoggingEntry(preparedEntry)
			}
			return ctx.Write(http.StatusOK, "ok")
		})

		// the entry joins while this request runs, so its Before missed it
		// and its value must stay out of this request's log
		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.NotContains(t, loggedAttrKeys(t, logs), "prepared")

		// the next request captures it before the Before hooks run
		logs.records = nil
		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		attrs := loggedAttrs(t, logs)
		require.Contains(t, attrs, "prepared")
		require.Equal(t, true, attrs["prepared"])
	})

	t.Run("added by a Before hook does not deadlock", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := testLoggedApp(logs)
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		added := false
		app.AddRequestLoggingEntry(RequestLoggingEntry{
			Key: "hook",
			Before: func(ctx *Context) {
				if added {
					return
				}
				added = true
				app.AddRequestLoggingEntry(RequestLoggingEntry{
					Key:   "from-hook",
					Value: testEntryValue("late"),
				})
			},
			Value: testEntryValue("hook"),
		})

		// hooks run while no lock is held, so adding from one returns
		done := make(chan struct{})
		go func() {
			defer close(done)
			app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("adding an entry from a Before hook deadlocked")
		}

		// the entry added mid-request joins the list but not this request
		require.Equal(t, "hook", loggedAttrs(t, logs)["hook"])
		require.NotContains(t, loggedAttrKeys(t, logs), "from-hook")

		// the next request snapshots it
		logs.records = nil
		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, "late", loggedAttrs(t, logs)["from-hook"])
	})
}

// Adding entries from another goroutine while requests snapshot the list is
// race free.
func TestAddRequestLoggingEntry_WhileRequestsRun(t *testing.T) {
	logs := &captureLogHandler{}
	app := testLoggedApp(logs)
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		return ctx.Write(http.StatusOK, "ok")
	})

	const extras = 50
	added := make(chan struct{})
	go func() {
		defer close(added)
		for i := range extras {
			app.AddRequestLoggingEntry(RequestLoggingEntry{
				Key:   "extra-" + strconv.Itoa(i),
				Value: testEntryValue("v"),
			})
			time.Sleep(time.Millisecond)
		}
	}()

	for range extras {
		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}
	<-added

	require.Len(t, requestLoggingEntryKeys(app.options.rle),
		len(DefaultRequestLoggingEntries())+extras)
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

// Every builtin request logging entry gets a case; an entry logging more than
// one shape gets cases inside its case.
func TestRle_BuiltinEntries(t *testing.T) {
	t.Run("time", func(t *testing.T) {
		t.Run("logs the arrival time", func(t *testing.T) {
			app, logs := testEntryApp(t, RleTime, nil)

			before := time.Now()
			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))
			after := time.Now()

			got := loggedAttr[time.Time](t, loggedAttrs(t, logs), "time")
			require.False(t, got.IsZero())
			require.False(t, got.Before(before))
			require.False(t, got.After(after))
		})

		t.Run("is captured before the handler chain runs", func(t *testing.T) {
			var handledAt time.Time
			app, logs := testEntryApp(t, RleTime, func(ctx *Context) error {
				handledAt = time.Now()
				return nil
			})

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			got := loggedAttr[time.Time](t, loggedAttrs(t, logs), "time")
			require.False(t, got.After(handledAt))
		})
	})

	t.Run("duration", func(t *testing.T) {
		t.Run("is a positive time.Duration", func(t *testing.T) {
			app, logs := testEntryApp(t, RleDuration, nil)

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			got := loggedAttr[time.Duration](t, loggedAttrs(t, logs), "duration")
			require.Greater(t, got, time.Duration(0))
		})

		t.Run("includes the time spent in the handler chain", func(t *testing.T) {
			const delay = 20 * time.Millisecond
			app, logs := testEntryApp(t, RleDuration, func(ctx *Context) error {
				time.Sleep(delay)
				return ctx.Write(http.StatusOK, "ok")
			})

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			got := loggedAttr[time.Duration](t, loggedAttrs(t, logs), "duration")
			require.GreaterOrEqual(t, got, delay)
		})
	})

	t.Run("remote", func(t *testing.T) {
		app, logs := testEntryApp(t, RleRemote, nil)
		request := httptest.NewRequest(http.MethodGet, "/users/42", nil)

		app.Test(request)

		require.Equal(t, request.RemoteAddr,
			loggedAttr[string](t, loggedAttrs(t, logs), "remote"))
	})

	t.Run("method", func(t *testing.T) {
		app, logs := testEntryApp(t, RleMethod, nil)

		app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

		require.Equal(t, http.MethodGet,
			loggedAttr[string](t, loggedAttrs(t, logs), "method"))
	})

	t.Run("url", func(t *testing.T) {
		t.Run("logs the path and query", func(t *testing.T) {
			app, logs := testEntryApp(t, RleUrl, nil)

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42?page=2", nil))

			require.Equal(t, "/users/42?page=2",
				loggedAttr[string](t, loggedAttrs(t, logs), "url"))
		})

		t.Run("logs the path when there is no query", func(t *testing.T) {
			app, logs := testEntryApp(t, RleUrl, nil)

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Equal(t, "/users/42",
				loggedAttr[string](t, loggedAttrs(t, logs), "url"))
		})
	})

	t.Run("path", func(t *testing.T) {
		app, logs := testEntryApp(t, RlePath, nil)

		app.Test(httptest.NewRequest(http.MethodGet, "/users/42?page=2", nil))

		require.Equal(t, "/users/42",
			loggedAttr[string](t, loggedAttrs(t, logs), "path"))
	})

	t.Run("pattern", func(t *testing.T) {
		t.Run("logs the pattern that matched the route", func(t *testing.T) {
			app, logs := testEntryApp(t, RlePattern, nil)

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Equal(t, "/users/:id",
				loggedAttr[string](t, loggedAttrs(t, logs), "pattern"))
		})

		t.Run("empty when no route matched", func(t *testing.T) {
			app, logs := testEntryApp(t, RlePattern, nil)

			app.Test(httptest.NewRequest(http.MethodGet, "/missing", nil))

			require.Equal(t, "",
				loggedAttr[string](t, loggedAttrs(t, logs), "pattern"))
		})
	})

	t.Run("status", func(t *testing.T) {
		t.Run("logs the status written by the handler chain", func(t *testing.T) {
			app, logs := testEntryApp(t, RleStatus, func(ctx *Context) error {
				return ctx.Write(http.StatusCreated, "hello")
			})

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Equal(t, int64(http.StatusCreated),
				loggedAttr[int64](t, loggedAttrs(t, logs), "status"))
		})

		t.Run("defaults to 200 when the chain wrote nothing", func(t *testing.T) {
			app, logs := testEntryApp(t, RleStatus, func(ctx *Context) error { return nil })

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Equal(t, int64(http.StatusOK),
				loggedAttr[int64](t, loggedAttrs(t, logs), "status"))
		})

		t.Run("logs the status set by the error handler", func(t *testing.T) {
			app, logs := testEntryApp(t, RleStatus, func(ctx *Context) error { return ErrNotFound })

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Equal(t, int64(resp.StatusCode),
				loggedAttr[int64](t, loggedAttrs(t, logs), "status"))
		})
	})

	t.Run("error", func(t *testing.T) {
		t.Run("nil when the handler chain succeeded", func(t *testing.T) {
			app, logs := testEntryApp(t, RleError, nil)

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Nil(t, loggedAttrs(t, logs)["error"])
		})

		t.Run("logs the error the error handler returned", func(t *testing.T) {
			app, logs := testEntryApp(t, RleError, func(ctx *Context) error { return ErrNotFound })

			app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

			require.Equal(t, ErrNotFound, loggedAttrs(t, logs)["error"])
		})
	})

	t.Run("pid", func(t *testing.T) {
		app, logs := testEntryApp(t, RlePid, nil)

		app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

		require.Equal(t, int64(os.Getpid()),
			loggedAttr[int64](t, loggedAttrs(t, logs), "pid"))
	})

	t.Run("user_agent", func(t *testing.T) {
		t.Run("logs the User-Agent header", func(t *testing.T) {
			app, logs := testEntryApp(t, RleUserAgent, nil)
			request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
			request.Header.Set("User-Agent", "test-agent")

			app.Test(request)

			require.Equal(t, "test-agent",
				loggedAttr[string](t, loggedAttrs(t, logs), "user_agent"))
		})

		t.Run("empty when the request carries none", func(t *testing.T) {
			app, logs := testEntryApp(t, RleUserAgent, nil)
			request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
			request.Header.Del("User-Agent")

			app.Test(request)

			require.Equal(t, "",
				loggedAttr[string](t, loggedAttrs(t, logs), "user_agent"))
		})
	})
}
