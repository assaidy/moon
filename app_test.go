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

func TestAppOptions_Defaults(t *testing.T) {
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
	require.Equal(t, time.Duration(0), app.options.serviceStartTimeout)
	require.Equal(t, time.Duration(0), app.options.serviceStopTimeout)
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

func TestAppOptions_WithListenAddress(t *testing.T) {
	app := New(NewAppOptions().WithListenAddress(":8080"))
	require.Equal(t, ":8080", app.options.listenAddress)
}

func TestAppOptions_WithLogger(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	app := New(NewAppOptions().WithLogger(logger))
	require.Same(t, logger, app.options.logger)
	require.Panics(t, func() { New(NewAppOptions().WithLogger(nil)) })
}

func TestAppOptions_WithErrorHandler(t *testing.T) {
	var captured error
	app := New(NewAppOptions().WithErrorHandler(func(ctx *Context, err error) error {
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
	require.Panics(t, func() { New(NewAppOptions().WithErrorHandler(nil)) })
}

func TestAppOptions_WithRequestLogging(t *testing.T) {
	require.True(t, New(NewAppOptions().WithRequestLogging(true)).options.enableRequestLogging)
	require.False(t, New().options.enableRequestLogging)
}

func TestAppOptions_WithRequestLoggingEntries(t *testing.T) {
	t.Run("defaults are the builtin entries", func(t *testing.T) {
		require.Equal(t,
			requestLoggingEntryKeys(defaultRequestLoggingEntries),
			requestLoggingEntryKeys(New().options.rle),
		)
	})

	t.Run("replaces the defaults and trims keys", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(NewAppOptions().
			WithLogger(slog.New(logs)).
			WithRequestLogging(true).
			WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "  custom  ", Value: testEntryValue("only")},
			}))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, map[string]any{"custom": "only"}, loggedAttrs(t, logs))
		require.Equal(t, []string{"custom"}, requestLoggingEntryKeys(app.options.rle))
	})

	t.Run("keeps the app starting point for appends", func(t *testing.T) {
		app := New(NewAppOptions().WithRequestLoggingEntries([]RequestLoggingEntry{
			{Key: "method", Value: testEntryValue("v")},
		}))
		app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "path", Value: testEntryValue("v")})

		require.Equal(t, []string{"method", "path"},
			requestLoggingEntryKeys(app.options.rle))
	})

	t.Run("empty key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New(NewAppOptions().WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("whitespace key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New(NewAppOptions().WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "   ", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("nil value panics", func(t *testing.T) {
		require.PanicsWithValue(t, "value func cannot be nil", func() {
			New(NewAppOptions().WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "custom", Value: nil},
			}))
		})
	})

	t.Run("duplicate keys panic", func(t *testing.T) {
		require.Panics(t, func() {
			New(NewAppOptions().WithRequestLoggingEntries([]RequestLoggingEntry{
				{Key: "path", Value: testEntryValue("v")},
				{Key: "method", Value: testEntryValue("v")},
				{Key: "path", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("nil logs no attributes", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(NewAppOptions().
			WithLogger(slog.New(logs)).
			WithRequestLogging(true).
			WithRequestLoggingEntries(nil))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Len(t, logs.records, 1)
		require.Empty(t, loggedAttrs(t, logs))
	})
}

func TestAppOptions_WithGeneralOptionsHandler(t *testing.T) {
	require.False(t, New(NewAppOptions().WithGeneralOptionsHandler(true)).httpServer.DisableGeneralOptionsHandler)
	require.True(t, New(NewAppOptions().WithGeneralOptionsHandler(false)).httpServer.DisableGeneralOptionsHandler)
}

func TestAppOptions_WithReadTimeout(t *testing.T) {
	app := New(NewAppOptions().WithReadTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithReadTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithReadTimeout(-time.Second)) })
}

func TestAppOptions_WithReadHeaderTimeout(t *testing.T) {
	app := New(NewAppOptions().WithReadHeaderTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadHeaderTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithReadHeaderTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithReadHeaderTimeout(-time.Second)) })
}

func TestAppOptions_WithWriteTimeout(t *testing.T) {
	app := New(NewAppOptions().WithWriteTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.WriteTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithWriteTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithWriteTimeout(-time.Second)) })
}

func TestAppOptions_WithIdleTimeout(t *testing.T) {
	app := New(NewAppOptions().WithIdleTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.IdleTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithIdleTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithIdleTimeout(-time.Second)) })
}

func TestAppOptions_WithMaxHeaderBytes(t *testing.T) {
	app := New(NewAppOptions().WithMaxHeaderBytes(1024))
	require.Equal(t, 1024, app.httpServer.MaxHeaderBytes)
	require.Panics(t, func() { New(NewAppOptions().WithMaxHeaderBytes(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithMaxHeaderBytes(-1)) })
}

func TestAppOptions_WithMaxHeaderValueCount(t *testing.T) {
	app := New(NewAppOptions().WithMaxHeaderValueCount(10))
	require.Equal(t, 10, app.httpServer.MaxHeaderValueCount)
	require.Panics(t, func() { New(NewAppOptions().WithMaxHeaderValueCount(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithMaxHeaderValueCount(-1)) })
}

func TestAppOptions_WithPrefork(t *testing.T) {
	require.True(t, New(NewAppOptions().WithPrefork(true)).options.preforkIsEnabled)
	require.False(t, New(NewAppOptions().WithPrefork(false)).options.preforkIsEnabled)
}

func TestAppOptions_WithPreforkChildrenCount(t *testing.T) {
	app := New(NewAppOptions().WithPreforkChildrenCount(3))
	require.Equal(t, 3, app.options.preforkChildrenCount)
	require.Panics(t, func() { New(NewAppOptions().WithPreforkChildrenCount(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithPreforkChildrenCount(-1)) })
}

func TestAppOptions_WithPreforkRetriesCount(t *testing.T) {
	require.Equal(t, 0, New(NewAppOptions().WithPreforkRetriesCount(0)).options.preforkRetriesCount)
	require.Equal(t, 7, New(NewAppOptions().WithPreforkRetriesCount(7)).options.preforkRetriesCount)
	require.Panics(t, func() { New(NewAppOptions().WithPreforkRetriesCount(-1)) })
	require.Panics(t, func() { New(NewAppOptions().WithPreforkRetriesCount(-2)) })
}

func TestAppOptions_WithTls(t *testing.T) {
	app := New(NewAppOptions().WithTls("cert.pem", "key.pem"))
	require.True(t, app.options.useTls)
	require.Equal(t, "cert.pem", app.options.certFile)
	require.Equal(t, "key.pem", app.options.keyFile)
	require.Panics(t, func() { New(NewAppOptions().WithTls("", "key.pem")) })
	require.Panics(t, func() { New(NewAppOptions().WithTls("cert.pem", "")) })
}

func TestAppOptions_WithTlsConfig(t *testing.T) {
	cfg := &tls.Config{}
	app := New(NewAppOptions().WithTlsConfig(cfg))
	require.Same(t, cfg, app.httpServer.TLSConfig)
	require.Panics(t, func() { New(NewAppOptions().WithTlsConfig(nil)) })
}

func TestAppOptions_WithHttp2Config(t *testing.T) {
	cfg := &http.HTTP2Config{}
	app := New(NewAppOptions().WithHttp2Config(cfg))
	require.Same(t, cfg, app.httpServer.HTTP2)
	require.Panics(t, func() { New(NewAppOptions().WithHttp2Config(nil)) })
}

func TestAppOptions_WithProtocols(t *testing.T) {
	p := &http.Protocols{}
	app := New(NewAppOptions().WithProtocols(p))
	require.Same(t, p, app.httpServer.Protocols)
	require.Panics(t, func() { New(NewAppOptions().WithProtocols(nil)) })
}

func TestAppOptions_WithClientPriority(t *testing.T) {
	require.False(t, New(NewAppOptions().WithClientPriority(true)).httpServer.DisableClientPriority)
	require.True(t, New(NewAppOptions().WithClientPriority(false)).httpServer.DisableClientPriority)
}

func TestAppOptions_WithServiceStartTimeout(t *testing.T) {
	app := New(NewAppOptions().WithServiceStartTimeout(time.Second))
	require.Equal(t, time.Second, app.options.serviceStartTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithServiceStartTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithServiceStartTimeout(-time.Second)) })
}

func TestAppOptions_WithServiceStopTimeout(t *testing.T) {
	app := New(NewAppOptions().WithServiceStopTimeout(time.Second))
	require.Equal(t, time.Second, app.options.serviceStopTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithServiceStopTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithServiceStopTimeout(-time.Second)) })
}

func TestAppOptions_WithParallelServiceStart(t *testing.T) {
	require.True(t, New(NewAppOptions().WithParallelServiceStart()).options.serviceStartParallel)
}

func TestAppOptions_WithParallelServiceStop(t *testing.T) {
	require.True(t, New(NewAppOptions().WithParallelServiceStop()).options.serviceStopParallel)
}

func TestAppOptions_WithShutdownTimeout(t *testing.T) {
	app := New(NewAppOptions().WithShutdownTimeout(time.Second))
	require.Equal(t, time.Second, app.options.shutdownTimeout)
	require.Panics(t, func() { New(NewAppOptions().WithShutdownTimeout(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithShutdownTimeout(-time.Second)) })
}

func TestAppOptions_WithReadLimit(t *testing.T) {
	app := New(NewAppOptions().WithReadLimit(1024))
	require.Equal(t, 1024, app.options.readLimit)
	require.Panics(t, func() { New(NewAppOptions().WithReadLimit(0)) })
	require.Panics(t, func() { New(NewAppOptions().WithReadLimit(-1)) })
}

func TestAppOptions_WithPassLocalsToContext(t *testing.T) {
	require.True(t, New(NewAppOptions().WithPassLocalsToContext(true)).options.passLocalsToContext)
	require.False(t, New(NewAppOptions().WithPassLocalsToContext(false)).options.passLocalsToContext)
}
