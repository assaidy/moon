package moon

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	require.False(t, app.options.useTls)
	require.Equal(t, "", app.options.certFile)
	require.Equal(t, "", app.options.keyFile)
	require.Equal(t, time.Duration(-1), app.options.readTimeout)
	require.Equal(t, time.Duration(0), app.options.readHeaderTimeout)
	require.Equal(t, time.Duration(-1), app.options.writeTimeout)
	require.Equal(t, time.Duration(-1), app.options.idleTimeout)
	require.Equal(t, time.Duration(-1), app.options.shutdownTimeout)
	require.False(t, app.options.passLocalsToContext)
	require.Equal(t, 4*1024*1024, app.options.maxBodyBytes)
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
	app := New(WithErrorHandler(func(ctx *Context) error {
		captured = ctx.Next()
		ctx.SetStatusCode(http.StatusTeapot)
		return captured
	}))
	sentinel := errors.New("boom")
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		return sentinel
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusTeapot, resp.StatusCode)
	require.Equal(t, sentinel, captured)
	require.Nil(t, New(WithErrorHandler(nil)).options.errorHandler)
}

func TestWithErrorHandlerNil(t *testing.T) {
	t.Run("disables error rendering", func(t *testing.T) {
		app := New(WithErrorHandler(nil))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			return ErrNotFound
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("lets the default handler run later in the chain", func(t *testing.T) {
		app := New(WithErrorHandler(nil))
		app.UseAll(DefaultErrorHandler)
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			return ErrNotFound
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestErrorHandlerRunsFirst(t *testing.T) {
	var order []string

	app := New(WithErrorHandler(func(ctx *Context) error {
		order = append(order, "error-before")
		err := ctx.Next()
		order = append(order, "error-after")
		return err
	}))
	app.UseAll(func(ctx *Context) error {
		order = append(order, "middleware")
		return ctx.Next()
	})
	app.Map(http.MethodGet, "/x", func(ctx *Context) error {
		order = append(order, "route")
		return nil
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, []string{"error-before", "middleware", "route", "error-after"}, order)
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
	require.NotPanics(t, func() { New(WithReadTimeout(-time.Second)) })
}

func TestWithReadHeaderTimeout(t *testing.T) {
	app := New(WithReadHeaderTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadHeaderTimeout)
	require.Panics(t, func() { New(WithReadHeaderTimeout(0)) })
	require.NotPanics(t, func() { New(WithReadHeaderTimeout(-time.Second)) })
}

func TestWithWriteTimeout(t *testing.T) {
	app := New(WithWriteTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.WriteTimeout)
	require.Panics(t, func() { New(WithWriteTimeout(0)) })
	require.NotPanics(t, func() { New(WithWriteTimeout(-time.Second)) })
}

func TestWithIdleTimeout(t *testing.T) {
	app := New(WithIdleTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.IdleTimeout)
	require.Panics(t, func() { New(WithIdleTimeout(0)) })
	require.NotPanics(t, func() { New(WithIdleTimeout(-time.Second)) })
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
	require.NotPanics(t, func() { New(WithTlsConfig(nil)) })
}

func TestWithHttp2Config(t *testing.T) {
	cfg := &http.HTTP2Config{}
	app := New(WithHttp2Config(cfg))
	require.Same(t, cfg, app.httpServer.HTTP2)
	require.NotPanics(t, func() { New(WithHttp2Config(nil)) })
}

func TestWithProtocols(t *testing.T) {
	p := &http.Protocols{}
	app := New(WithProtocols(p))
	require.Same(t, p, app.httpServer.Protocols)
	require.NotPanics(t, func() { New(WithProtocols(nil)) })
}

func TestWithClientPriority(t *testing.T) {
	require.False(t, New(WithClientPriority(true)).httpServer.DisableClientPriority)
	require.True(t, New(WithClientPriority(false)).httpServer.DisableClientPriority)
}

func TestWithShutdownTimeout(t *testing.T) {
	app := New(WithShutdownTimeout(time.Second))
	require.Equal(t, time.Second, app.options.shutdownTimeout)
	require.Panics(t, func() { New(WithShutdownTimeout(0)) })
	require.NotPanics(t, func() { New(WithShutdownTimeout(-time.Second)) })
}

func TestWithMaxBodyBytes(t *testing.T) {
	app := New(WithMaxBodyBytes(1024))
	require.Equal(t, 1024, app.options.maxBodyBytes)
	require.Equal(t, 0, New(WithMaxBodyBytes(0)).options.maxBodyBytes)
	require.Equal(t, -1, New(WithMaxBodyBytes(-1)).options.maxBodyBytes)
}

func TestWithPassLocalsToContext(t *testing.T) {
	require.True(t, New(WithPassLocalsToContext(true)).options.passLocalsToContext)
	require.False(t, New(WithPassLocalsToContext(false)).options.passLocalsToContext)
}
