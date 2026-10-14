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

func TestAppOption_Defaults(t *testing.T) {
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

func TestAppOption_ListenAddress(t *testing.T) {
	app := New(AppOption_ListenAddress(":8080"))
	require.Equal(t, ":8080", app.options.listenAddress)
}

func TestAppOption_Logger(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	app := New(AppOption_Logger(logger))
	require.Same(t, logger, app.options.logger)
	require.Panics(t, func() { New(AppOption_Logger(nil)) })
}

func TestAppOption_ErrorHandler(t *testing.T) {
	var captured error
	app := New(AppOption_ErrorHandler(func(ctx *Context, err error) error {
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
	require.Panics(t, func() { New(AppOption_ErrorHandler(nil)) })
}

func TestAppOption_RequestLogging(t *testing.T) {
	require.True(t, New(AppOption_RequestLogging(true)).options.enableRequestLogging)
	require.False(t, New().options.enableRequestLogging)
}

func TestAppOption_RequestLoggingEntries(t *testing.T) {
	t.Run("defaults are the builtin entries", func(t *testing.T) {
		require.Equal(t,
			requestLoggingEntryKeys(defaultRequestLoggingEntries),
			requestLoggingEntryKeys(New().options.rle),
		)
	})

	t.Run("replaces the defaults and trims keys", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(
			AppOption_Logger(slog.New(logs)),
			AppOption_RequestLogging(true),
			AppOption_RequestLoggingEntries([]RequestLoggingEntry{
				{Key: "  custom  ", Value: testEntryValue("only")},
			}))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, map[string]any{"custom": "only"}, loggedAttrs(t, logs))
		require.Equal(t, []string{"custom"}, requestLoggingEntryKeys(app.options.rle))
	})

	t.Run("keeps the app starting point for appends", func(t *testing.T) {
		app := New(AppOption_RequestLoggingEntries([]RequestLoggingEntry{
			{Key: "method", Value: testEntryValue("v")},
		}))
		app.AddRequestLoggingEntry(RequestLoggingEntry{Key: "path", Value: testEntryValue("v")})

		require.Equal(t, []string{"method", "path"},
			requestLoggingEntryKeys(app.options.rle))
	})

	t.Run("empty key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New(AppOption_RequestLoggingEntries([]RequestLoggingEntry{
				{Key: "", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("whitespace key panics", func(t *testing.T) {
		require.PanicsWithValue(t, "key cannot be empty or whitespace", func() {
			New(AppOption_RequestLoggingEntries([]RequestLoggingEntry{
				{Key: "   ", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("nil value panics", func(t *testing.T) {
		require.PanicsWithValue(t, "value func cannot be nil", func() {
			New(AppOption_RequestLoggingEntries([]RequestLoggingEntry{
				{Key: "custom", Value: nil},
			}))
		})
	})

	t.Run("duplicate keys panic", func(t *testing.T) {
		require.Panics(t, func() {
			New(AppOption_RequestLoggingEntries([]RequestLoggingEntry{
				{Key: "path", Value: testEntryValue("v")},
				{Key: "method", Value: testEntryValue("v")},
				{Key: "path", Value: testEntryValue("v")},
			}))
		})
	})

	t.Run("nil logs no attributes", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(
			AppOption_Logger(slog.New(logs)),
			AppOption_RequestLogging(true),
			AppOption_RequestLoggingEntries(nil))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Len(t, logs.records, 1)
		require.Empty(t, loggedAttrs(t, logs))
	})
}

func TestAppOption_GeneralOptionsHandler(t *testing.T) {
	require.False(t, New(AppOption_GeneralOptionsHandler(true)).httpServer.DisableGeneralOptionsHandler)
	require.True(t, New(AppOption_GeneralOptionsHandler(false)).httpServer.DisableGeneralOptionsHandler)
}

func TestAppOption_ReadTimeout(t *testing.T) {
	app := New(AppOption_ReadTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadTimeout)
	require.Panics(t, func() { New(AppOption_ReadTimeout(0)) })
	require.Panics(t, func() { New(AppOption_ReadTimeout(-time.Second)) })
}

func TestAppOption_ReadHeaderTimeout(t *testing.T) {
	app := New(AppOption_ReadHeaderTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadHeaderTimeout)
	require.Panics(t, func() { New(AppOption_ReadHeaderTimeout(0)) })
	require.Panics(t, func() { New(AppOption_ReadHeaderTimeout(-time.Second)) })
}

func TestAppOption_WriteTimeout(t *testing.T) {
	app := New(AppOption_WriteTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.WriteTimeout)
	require.Panics(t, func() { New(AppOption_WriteTimeout(0)) })
	require.Panics(t, func() { New(AppOption_WriteTimeout(-time.Second)) })
}

func TestAppOption_IdleTimeout(t *testing.T) {
	app := New(AppOption_IdleTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.IdleTimeout)
	require.Panics(t, func() { New(AppOption_IdleTimeout(0)) })
	require.Panics(t, func() { New(AppOption_IdleTimeout(-time.Second)) })
}

func TestAppOption_MaxHeaderBytes(t *testing.T) {
	app := New(AppOption_MaxHeaderBytes(1024))
	require.Equal(t, 1024, app.httpServer.MaxHeaderBytes)
	require.Panics(t, func() { New(AppOption_MaxHeaderBytes(0)) })
	require.Panics(t, func() { New(AppOption_MaxHeaderBytes(-1)) })
}

func TestAppOption_MaxHeaderValueCount(t *testing.T) {
	app := New(AppOption_MaxHeaderValueCount(10))
	require.Equal(t, 10, app.httpServer.MaxHeaderValueCount)
	require.Panics(t, func() { New(AppOption_MaxHeaderValueCount(0)) })
	require.Panics(t, func() { New(AppOption_MaxHeaderValueCount(-1)) })
}

func TestAppOption_Prefork(t *testing.T) {
	require.True(t, New(AppOption_Prefork(true)).options.preforkIsEnabled)
	require.False(t, New(AppOption_Prefork(false)).options.preforkIsEnabled)
}

func TestAppOption_PreforkChildrenCount(t *testing.T) {
	app := New(AppOption_PreforkChildrenCount(3))
	require.Equal(t, 3, app.options.preforkChildrenCount)
	require.Panics(t, func() { New(AppOption_PreforkChildrenCount(0)) })
	require.Panics(t, func() { New(AppOption_PreforkChildrenCount(-1)) })
}

func TestAppOption_PreforkRetriesCount(t *testing.T) {
	require.Equal(t, 0, New(AppOption_PreforkRetriesCount(0)).options.preforkRetriesCount)
	require.Equal(t, 7, New(AppOption_PreforkRetriesCount(7)).options.preforkRetriesCount)
	require.Panics(t, func() { New(AppOption_PreforkRetriesCount(-1)) })
	require.Panics(t, func() { New(AppOption_PreforkRetriesCount(-2)) })
}

func TestAppOption_Tls(t *testing.T) {
	app := New(AppOption_Tls("cert.pem", "key.pem"))
	require.True(t, app.options.useTls)
	require.Equal(t, "cert.pem", app.options.certFile)
	require.Equal(t, "key.pem", app.options.keyFile)
	require.Panics(t, func() { New(AppOption_Tls("", "key.pem")) })
	require.Panics(t, func() { New(AppOption_Tls("cert.pem", "")) })
}

func TestAppOption_TlsConfig(t *testing.T) {
	cfg := &tls.Config{}
	app := New(AppOption_TlsConfig(cfg))
	require.Same(t, cfg, app.httpServer.TLSConfig)
	require.Panics(t, func() { New(AppOption_TlsConfig(nil)) })
}

func TestAppOption_Http2Config(t *testing.T) {
	cfg := &http.HTTP2Config{}
	app := New(AppOption_Http2Config(cfg))
	require.Same(t, cfg, app.httpServer.HTTP2)
	require.Panics(t, func() { New(AppOption_Http2Config(nil)) })
}

func TestAppOption_Protocols(t *testing.T) {
	p := &http.Protocols{}
	app := New(AppOption_Protocols(p))
	require.Same(t, p, app.httpServer.Protocols)
	require.Panics(t, func() { New(AppOption_Protocols(nil)) })
}

func TestAppOption_ClientPriority(t *testing.T) {
	require.False(t, New(AppOption_ClientPriority(true)).httpServer.DisableClientPriority)
	require.True(t, New(AppOption_ClientPriority(false)).httpServer.DisableClientPriority)
}

func TestAppOption_ServiceStartTimeout(t *testing.T) {
	app := New(AppOption_ServiceStartTimeout(time.Second))
	require.Equal(t, time.Second, app.options.serviceStartTimeout)
	require.Panics(t, func() { New(AppOption_ServiceStartTimeout(0)) })
	require.Panics(t, func() { New(AppOption_ServiceStartTimeout(-time.Second)) })
}

func TestAppOption_ServiceStopTimeout(t *testing.T) {
	app := New(AppOption_ServiceStopTimeout(time.Second))
	require.Equal(t, time.Second, app.options.serviceStopTimeout)
	require.Panics(t, func() { New(AppOption_ServiceStopTimeout(0)) })
	require.Panics(t, func() { New(AppOption_ServiceStopTimeout(-time.Second)) })
}

func TestAppOption_ParallelServiceStart(t *testing.T) {
	require.True(t, New(AppOption_ParallelServiceStart(true)).options.serviceStartParallel)
}

func TestAppOption_ParallelServiceStop(t *testing.T) {
	require.True(t, New(AppOption_ParallelServiceStop(true)).options.serviceStopParallel)
}

func TestAppOption_ShutdownTimeout(t *testing.T) {
	app := New(AppOption_ShutdownTimeout(time.Second))
	require.Equal(t, time.Second, app.options.shutdownTimeout)
	require.Panics(t, func() { New(AppOption_ShutdownTimeout(0)) })
	require.Panics(t, func() { New(AppOption_ShutdownTimeout(-time.Second)) })
}

func TestAppOption_ReadLimit(t *testing.T) {
	app := New(AppOption_ReadLimit(1024))
	require.Equal(t, 1024, app.options.readLimit)
	require.Panics(t, func() { New(AppOption_ReadLimit(0)) })
	require.Panics(t, func() { New(AppOption_ReadLimit(-1)) })
}

func TestAppOption_PassLocalsToContext(t *testing.T) {
	require.True(t, New(AppOption_PassLocalsToContext(true)).options.passLocalsToContext)
	require.False(t, New(AppOption_PassLocalsToContext(false)).options.passLocalsToContext)
}
