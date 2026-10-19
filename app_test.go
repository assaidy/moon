package moon

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWithDefaults(t *testing.T) {
	app := New()

	require.Equal(t, "", app.options.listenAddress)
	require.Same(t, slog.Default(), app.options.logger)
	require.NotNil(t, app.options.errorHandler)
	require.False(t, app.options.enableRequestLogging)
	require.False(t, app.options.preforkIsEnabled)
	require.Equal(t, runtime.NumCPU(), app.options.preforkChildrenCount)
	require.Equal(t, -1, app.options.preforkRetriesCount)
	require.False(t, app.options.useTls)
	require.Equal(t, "", app.options.certFile)
	require.Equal(t, "", app.options.keyFile)
	require.False(t, app.options.serviceStartParallel)
	require.False(t, app.options.serviceStopParallel)
	require.Equal(t, time.Duration(0), app.options.shutdownTimeout)
	require.False(t, app.options.passLocalsToContext)
	require.Equal(t, 4*1024*1024, app.options.readLimit)
	require.NotNil(t, app.httpServer)
	require.NotNil(t, app.dependencies)
	require.Empty(t, app.services)
	require.True(t, app.httpServer.DisableGeneralOptionsHandler)
	require.False(t, app.httpServer.DisableClientPriority)
}

func TestWithListenAddress(t *testing.T) {
	app := New(WithListenAddress(":8080"))
	require.Equal(t, ":8080", app.options.listenAddress)
}

func TestWithLogger(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	app := New(WithLogger(logger))
	require.Same(t, logger, app.options.logger)
	require.Panics(t, func() { New(WithLogger(nil)) })
}

func TestWithErrorHandler(t *testing.T) {
	var captured error
	app := New(WithErrorHandler(func(ctx *Context, err error) error {
		captured = err
		ctx.SetStatusCode(http.StatusTeapot)
		return err
	}))
	sentinel := errors.New("boom")
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		return sentinel
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusTeapot, resp.StatusCode)
	require.Equal(t, sentinel, captured)
	require.Panics(t, func() { New(WithErrorHandler(nil)) })
}

func TestWithRequestLogging(t *testing.T) {
	require.True(t, New(WithRequestLogging(true)).options.enableRequestLogging)
	require.False(t, New().options.enableRequestLogging)
}

func TestWithRequestLoggingEntries(t *testing.T) {
	t.Run("defaults are the builtin entries", func(t *testing.T) {
		require.Equal(t,
			requestLoggingEntryKeys(defaultRequestLoggingEntries),
			requestLoggingEntryKeys(New().options.rle),
		)
	})

	t.Run("replaces the defaults and trims keys", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(
			WithLogger(slog.New(logs)),
			WithRequestLogging(true),
			WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "  custom  ", Value: testEntryValue("only")},
			}))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, map[string]any{"custom": "only"}, loggedAttrs(t, logs))
		require.Equal(t, []string{"custom"}, requestLoggingEntryKeys(app.options.rle))
	})

	t.Run("keeps the app starting point for appends", func(t *testing.T) {
		app := New(WithRequestLoggingEntries([]RequestLoggingEntry{
			{Key: "method", Value: testEntryValue("v")},
		}))
		app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "path", Value: testEntryValue("v")})

		require.Equal(t, []string{"method", "path"},
			requestLoggingEntryKeys(app.options.rle))
	})

	t.Run("empty key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New(WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("whitespace key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New(WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "   ", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("nil value panics", func(t *testing.T) {
		require.PanicsWithValue(t, "value func cannot be nil", func() {
			New(WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "custom", Value: nil},
			}))
		})
	})

	t.Run("duplicate keys panic", func(t *testing.T) {
		require.Panics(t, func() {
			New(WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "path", Value: testEntryValue("v")},
				{Key: "method", Value: testEntryValue("v")},
				{Key: "path", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("nil logs no attributes", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(
			WithLogger(slog.New(logs)),
			WithRequestLogging(true),
			WithRequestLoggingEntries(nil))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Len(t, logs.records, 1)
		require.Empty(t, loggedAttrs(t, logs))
	})
}

func TestWithGeneralOptionsHandler(t *testing.T) {
	require.False(t, New(WithGeneralOptionsHandler(true)).httpServer.DisableGeneralOptionsHandler)
	require.True(t, New(WithGeneralOptionsHandler(false)).httpServer.DisableGeneralOptionsHandler)
}

func TestWithReadTimeout(t *testing.T) {
	app := New(WithReadTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadTimeout)
	require.Panics(t, func() { New(WithReadTimeout(0)) })
	require.Panics(t, func() { New(WithReadTimeout(-time.Second)) })
}

func TestWithReadHeaderTimeout(t *testing.T) {
	app := New(WithReadHeaderTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadHeaderTimeout)
	require.Panics(t, func() { New(WithReadHeaderTimeout(0)) })
	require.Panics(t, func() { New(WithReadHeaderTimeout(-time.Second)) })
}

func TestWithWriteTimeout(t *testing.T) {
	app := New(WithWriteTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.WriteTimeout)
	require.Panics(t, func() { New(WithWriteTimeout(0)) })
	require.Panics(t, func() { New(WithWriteTimeout(-time.Second)) })
}

func TestWithIdleTimeout(t *testing.T) {
	app := New(WithIdleTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.IdleTimeout)
	require.Panics(t, func() { New(WithIdleTimeout(0)) })
	require.Panics(t, func() { New(WithIdleTimeout(-time.Second)) })
}

func TestWithMaxHeaderBytes(t *testing.T) {
	app := New(WithMaxHeaderBytes(1024))
	require.Equal(t, 1024, app.httpServer.MaxHeaderBytes)
	require.Panics(t, func() { New(WithMaxHeaderBytes(0)) })
	require.Panics(t, func() { New(WithMaxHeaderBytes(-1)) })
}

func TestWithMaxHeaderValueCount(t *testing.T) {
	app := New(WithMaxHeaderValueCount(10))
	require.Equal(t, 10, app.httpServer.MaxHeaderValueCount)
	require.Panics(t, func() { New(WithMaxHeaderValueCount(0)) })
	require.Panics(t, func() { New(WithMaxHeaderValueCount(-1)) })
}

func TestWithPrefork(t *testing.T) {
	require.True(t, New(WithPrefork(true)).options.preforkIsEnabled)
	require.False(t, New(WithPrefork(false)).options.preforkIsEnabled)
}

func TestWithPreforkChildrenCount(t *testing.T) {
	app := New(WithPreforkChildrenCount(3))
	require.Equal(t, 3, app.options.preforkChildrenCount)
	require.Panics(t, func() { New(WithPreforkChildrenCount(0)) })
	require.Panics(t, func() { New(WithPreforkChildrenCount(-1)) })
}

func TestWithPreforkRetriesCount(t *testing.T) {
	require.Equal(t, 0, New(WithPreforkRetriesCount(0)).options.preforkRetriesCount)
	require.Equal(t, 7, New(WithPreforkRetriesCount(7)).options.preforkRetriesCount)
	require.Panics(t, func() { New(WithPreforkRetriesCount(-1)) })
	require.Panics(t, func() { New(WithPreforkRetriesCount(-2)) })
}

func TestWithTls(t *testing.T) {
	app := New(WithTls("cert.pem", "key.pem"))
	require.True(t, app.options.useTls)
	require.Equal(t, "cert.pem", app.options.certFile)
	require.Equal(t, "key.pem", app.options.keyFile)
	require.Panics(t, func() { New(WithTls("", "key.pem")) })
	require.Panics(t, func() { New(WithTls("cert.pem", "")) })
}

func TestWithTlsConfig(t *testing.T) {
	cfg := &tls.Config{}
	app := New(WithTlsConfig(cfg))
	require.Same(t, cfg, app.httpServer.TLSConfig)
	require.Panics(t, func() { New(WithTlsConfig(nil)) })
}

func TestWithHttp2Config(t *testing.T) {
	cfg := &http.HTTP2Config{}
	app := New(WithHttp2Config(cfg))
	require.Same(t, cfg, app.httpServer.HTTP2)
	require.Panics(t, func() { New(WithHttp2Config(nil)) })
}

func TestWithProtocols(t *testing.T) {
	p := &http.Protocols{}
	app := New(WithProtocols(p))
	require.Same(t, p, app.httpServer.Protocols)
	require.Panics(t, func() { New(WithProtocols(nil)) })
}

func TestWithClientPriority(t *testing.T) {
	require.False(t, New(WithClientPriority(true)).httpServer.DisableClientPriority)
	require.True(t, New(WithClientPriority(false)).httpServer.DisableClientPriority)
}

func TestWithParallelServiceStart(t *testing.T) {
	require.True(t, New(WithParallelServiceStart(true)).options.serviceStartParallel)
}

func TestWithParallelServiceStop(t *testing.T) {
	require.True(t, New(WithParallelServiceStop(true)).options.serviceStopParallel)
}

func TestWithShutdownTimeout(t *testing.T) {
	app := New(WithShutdownTimeout(time.Second))
	require.Equal(t, time.Second, app.options.shutdownTimeout)
	require.Panics(t, func() { New(WithShutdownTimeout(0)) })
	require.Panics(t, func() { New(WithShutdownTimeout(-time.Second)) })
}

func TestWithReadLimit(t *testing.T) {
	app := New(WithReadLimit(1024))
	require.Equal(t, 1024, app.options.readLimit)
	require.Panics(t, func() { New(WithReadLimit(0)) })
	require.Panics(t, func() { New(WithReadLimit(-1)) })
}

func TestWithPassLocalsToContext(t *testing.T) {
	require.True(t, New(WithPassLocalsToContext(true)).options.passLocalsToContext)
	require.False(t, New(WithPassLocalsToContext(false)).options.passLocalsToContext)
}
