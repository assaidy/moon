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

	require.Equal(t, "", app.listenAddress)
	require.Same(t, slog.Default(), app.logger)
	require.NotNil(t, app.errorHandler)
	require.True(t, app.enableRequestLogging)
	require.False(t, app.preforkIsEnabled)
	require.Equal(t, runtime.NumCPU(), app.preforkChildrenCount)
	require.Equal(t, 5, app.preforkRetriesCount)
	require.False(t, app.useTls)
	require.Equal(t, "", app.certFile)
	require.Equal(t, "", app.keyFile)
	require.Equal(t, time.Duration(0), app.serviceStartTimeout)
	require.Equal(t, time.Duration(0), app.serviceStopTimeout)
	require.False(t, app.serviceStartParallel)
	require.False(t, app.serviceStopParallel)
	require.Equal(t, time.Duration(0), app.shutdownTimeout)
	require.False(t, app.passLocalsToContext)
	require.NotNil(t, app.httpServer)
	require.NotNil(t, app.dependencies)
	require.NotNil(t, app.services)
	require.NotNil(t, app.startedServices)
	require.False(t, app.httpServer.DisableGeneralOptionsHandler)
	require.False(t, app.httpServer.DisableClientPriority)
}

func TestAppOptions_WithListenAddress(t *testing.T) {
	app := New(WithListenAddress(":8080"))
	require.Equal(t, ":8080", app.listenAddress)
}

func TestAppOptions_WithLogger(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	app := New(WithLogger(logger))
	require.Same(t, logger, app.logger)
	require.Panics(t, func() { WithLogger(nil) })
}

func TestAppOptions_WithErrorHandler(t *testing.T) {
	var captured error
	app := New(
		WithRequestLogging(false),
		WithErrorHandler(func(ctx *Context, err error) {
			captured = err
			ctx.SetStatusCode(http.StatusTeapot)
		}),
	)
	sentinel := errors.New("boom")
	app.Handle(http.MethodGet, "/x", func(ctx *Context) error {
		return sentinel
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	require.Equal(t, http.StatusTeapot, resp.StatusCode)
	require.Equal(t, sentinel, captured)
	require.Panics(t, func() { WithErrorHandler(nil) })
}

func TestAppOptions_WithRequestLogging(t *testing.T) {
	require.True(t, New(WithRequestLogging(true)).enableRequestLogging)
	require.False(t, New(WithRequestLogging(false)).enableRequestLogging)
}

func TestAppOptions_WithGeneralOptionsHandler(t *testing.T) {
	require.False(t, New(WithGeneralOptionsHandler(true)).httpServer.DisableGeneralOptionsHandler)
	require.True(t, New(WithGeneralOptionsHandler(false)).httpServer.DisableGeneralOptionsHandler)
}

func TestAppOptions_WithReadTimeout(t *testing.T) {
	app := New(WithReadTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadTimeout)
	require.Panics(t, func() { WithReadTimeout(0) })
	require.Panics(t, func() { WithReadTimeout(-time.Second) })
}

func TestAppOptions_WithReadHeaderTimeout(t *testing.T) {
	app := New(WithReadHeaderTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.ReadHeaderTimeout)
	require.Panics(t, func() { WithReadHeaderTimeout(0) })
	require.Panics(t, func() { WithReadHeaderTimeout(-time.Second) })
}

func TestAppOptions_WithWriteTimeout(t *testing.T) {
	app := New(WithWriteTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.WriteTimeout)
	require.Panics(t, func() { WithWriteTimeout(0) })
	require.Panics(t, func() { WithWriteTimeout(-time.Second) })
}

func TestAppOptions_WithIdleTimeout(t *testing.T) {
	app := New(WithIdleTimeout(time.Second))
	require.Equal(t, time.Second, app.httpServer.IdleTimeout)
	require.Panics(t, func() { New(WithIdleTimeout(0)) })
	require.Panics(t, func() { New(WithIdleTimeout(-time.Second)) })
}

func TestAppOptions_WithMaxHeaderBytes(t *testing.T) {
	app := New(WithMaxHeaderBytes(1024))
	require.Equal(t, 1024, app.httpServer.MaxHeaderBytes)
	require.Panics(t, func() { WithMaxHeaderBytes(0) })
	require.Panics(t, func() { WithMaxHeaderBytes(-1) })
}

func TestAppOptions_WithMaxHeaderValueCount(t *testing.T) {
	app := New(WithMaxHeaderValueCount(10))
	require.Equal(t, 10, app.httpServer.MaxHeaderValueCount)
	require.Panics(t, func() { WithMaxHeaderValueCount(0) })
	require.Panics(t, func() { WithMaxHeaderValueCount(-1) })
}

func TestAppOptions_WithPrefork(t *testing.T) {
	require.True(t, New(WithPrefork(true)).preforkIsEnabled)
	require.False(t, New(WithPrefork(false)).preforkIsEnabled)
}

func TestAppOptions_WithPreforkChildrenCount(t *testing.T) {
	app := New(WithPreforkChildrenCount(3))
	require.Equal(t, 3, app.preforkChildrenCount)
	require.Panics(t, func() { WithPreforkChildrenCount(0) })
	require.Panics(t, func() { WithPreforkChildrenCount(-1) })
}

func TestAppOptions_WithPreforkRetriesCount(t *testing.T) {
	require.Equal(t, 0, New(WithPreforkRetriesCount(0)).preforkRetriesCount)
	require.Equal(t, 7, New(WithPreforkRetriesCount(7)).preforkRetriesCount)
	require.Panics(t, func() { WithPreforkRetriesCount(-1) })
}

func TestAppOptions_WithTls(t *testing.T) {
	app := New(WithTls("cert.pem", "key.pem"))
	require.True(t, app.useTls)
	require.Equal(t, "cert.pem", app.certFile)
	require.Equal(t, "key.pem", app.keyFile)
	require.Panics(t, func() { WithTls("", "key.pem") })
	require.Panics(t, func() { WithTls("cert.pem", "") })
}

func TestAppOptions_WithTlsConfig(t *testing.T) {
	cfg := &tls.Config{}
	app := New(WithTlsConfig(cfg))
	require.Same(t, cfg, app.httpServer.TLSConfig)
	require.Panics(t, func() { WithTlsConfig(nil) })
}

func TestAppOptions_WithHttp2Config(t *testing.T) {
	cfg := &http.HTTP2Config{}
	app := New(WithHttp2Config(cfg))
	require.Same(t, cfg, app.httpServer.HTTP2)
	require.Panics(t, func() { WithHttp2Config(nil) })
}

func TestAppOptions_WithProtocols(t *testing.T) {
	p := &http.Protocols{}
	app := New(WithProtocols(p))
	require.Same(t, p, app.httpServer.Protocols)
	require.Panics(t, func() { WithProtocols(nil) })
}

func TestAppOptions_WithClientPriority(t *testing.T) {
	require.False(t, New(WithClientPriority(true)).httpServer.DisableClientPriority)
	require.True(t, New(WithClientPriority(false)).httpServer.DisableClientPriority)
}

func TestAppOptions_WithServiceStartTimeout(t *testing.T) {
	app := New(WithServiceStartTimeout(time.Second))
	require.Equal(t, time.Second, app.serviceStartTimeout)
	require.Panics(t, func() { WithServiceStartTimeout(0) })
	require.Panics(t, func() { WithServiceStartTimeout(-time.Second) })
}

func TestAppOptions_WithServiceStopTimeout(t *testing.T) {
	app := New(WithServiceStopTimeout(time.Second))
	require.Equal(t, time.Second, app.serviceStopTimeout)
	require.Panics(t, func() { WithServiceStopTimeout(0) })
	require.Panics(t, func() { WithServiceStopTimeout(-time.Second) })
}

func TestAppOptions_WithParallelServiceStart(t *testing.T) {
	require.True(t, New(WithParallelServiceStart()).serviceStartParallel)
}

func TestAppOptions_WithParallelServiceStop(t *testing.T) {
	require.True(t, New(WithParallelServiceStop()).serviceStopParallel)
}

func TestAppOptions_WithShutdownTimeout(t *testing.T) {
	app := New(WithShutdownTimeout(time.Second))
	require.Equal(t, time.Second, app.shutdownTimeout)
	require.Panics(t, func() { WithShutdownTimeout(0) })
	require.Panics(t, func() { WithShutdownTimeout(-time.Second) })
}

func TestAppOptions_WithPassLocalsToContext(t *testing.T) {
	require.True(t, New(WithPassLocalsToContext(true)).passLocalsToContext)
	require.False(t, New(WithPassLocalsToContext(false)).passLocalsToContext)
}
