package moon

import (
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"sync"
	"time"
)

// App is the HTTP server, router and owner of shared state, typed
// dependencies and managed services. Build it with [New], chain the With*
// methods to configure it, register routes with [App.Map] and [App.Use],
// then serve with [App.Start] and stop with [App.Shutdown]. Use [App.Test]
// to exercise handlers without listening.
type App struct {
	httpServer                      *http.Server
	routes                          []Route
	state                           sync.Map
	dependencies                    map[reflect.Type]any
	services                        []serviceInfo
	registeredRequestLoggingEntries []RequestLoggingEntry
	requestLoggingEntriesMutex      sync.RWMutex

	// options
	listenAddress        string
	logger               *slog.Logger
	errorHandler         ErrorHandler
	enableRequestLogging bool
	preforkIsEnabled     bool
	preforkChildrenCount int
	preforkRetriesCount  int
	useTls               bool
	certFile             string
	keyFile              string
	serviceStartTimeout  time.Duration
	serviceStopTimeout   time.Duration
	serviceStartParallel bool
	serviceStopParallel  bool
	shutdownTimeout      time.Duration
	passLocalsToContext  bool
}

// New creates an App with sensible defaults and registers the root handler.
// Chain the With* methods to configure it, then serve it with [App.Start]:
//
//	app := New().WithListenAddress(":3000")
//	app := New().WithLogger(logger).WithRequestLogging(true)
func New() *App {
	app := &App{
		httpServer:           new(http.Server),
		dependencies:         make(map[reflect.Type]any),
		logger:               slog.Default(),
		errorHandler:         DefaultErrorHandler,
		preforkChildrenCount: runtime.NumCPU(),
		preforkRetriesCount:  -1,
	}

	app.registerReservedRequestLoggingEntries()
	app.registerRootHandler()

	return app
}

// WithListenAddress sets the TCP address the server listens on,
// e.g. ":8080" or "127.0.0.1:3000". It returns the same app for chaining.
//
// Default: ":http" (port 80), or ":https" (port 443) when TLS is
// enabled via [App.WithTls].
func (me *App) WithListenAddress(address string) *App {
	me.listenAddress = address
	return me
}

// WithLogger sets the logger used for all internal logging within the app
// and its contexts. It is also used for request logging when
// [App.WithRequestLogging] is enabled. It panics on a nil logger. It returns
// the same app for chaining.
//
// Default: [slog.Default]
func (me *App) WithLogger(l *slog.Logger) *App {
	Assert(l != nil)
	me.logger = l
	return me
}

// WithErrorHandler sets the handler used to handle errors returned by the
// handler chain, including [ErrInvalidEndpoint] for unknown paths and
// [ErrMethodNotAllowed] for unregistered methods, so a custom handler can
// inspect or override them. It panics on a nil handler. It returns the same
// app for chaining.
//
// Default: [DefaultErrorHandler]
func (me *App) WithErrorHandler(eh ErrorHandler) *App {
	Assert(eh != nil)
	me.errorHandler = eh
	return me
}

// WithRequestLogging determines whether to log request handling results,
// such as response time, status code, remote address, error, etc.
// Every request routed through the app is logged, including unmatched
// paths/methods handled as [ErrInvalidEndpoint]/[ErrMethodNotAllowed].
// The logged status defaults to 200 when no response was written.
// It returns the same app for chaining.
//
// Default: false
func (me *App) WithRequestLogging(b bool) *App {
	me.enableRequestLogging = b
	return me
}

// WithGeneralOptionsHandler determines whether the server should pass
// general OPTIONS requests to the Handler. If true, the server responds
// with a 200 OK status and a Content-Length of 0.
// It returns the same app for chaining.
//
// Default: true
func (me *App) WithGeneralOptionsHandler(b bool) *App {
	me.httpServer.DisableGeneralOptionsHandler = !b
	return me
}

// WithReadTimeout sets the maximum duration for reading the entire request,
// including the body. It returns the same app for chaining.
//
// Default: no timeout
func (me *App) WithReadTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.httpServer.ReadTimeout = d
	return me
}

// WithReadHeaderTimeout sets the maximum duration for reading request headers.
// The connection's read deadline is reset after the headers are read,
// allowing the Handler to decide what is considered too slow for the body.
// It returns the same app for chaining.
//
// Default: read timout
func (me *App) WithReadHeaderTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.httpServer.ReadHeaderTimeout = d
	return me
}

// WithWriteTimeout sets the maximum duration for writing the response. It is
// reset whenever a new request's header is read. Like ReadTimeout, it
// does not allow Handlers to make per-request decisions.
// It returns the same app for chaining.
//
// Default: no timeout
func (me *App) WithWriteTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.httpServer.WriteTimeout = d
	return me
}

// WithIdleTimeout sets the maximum amount of time to wait for the next request
// when keep-alives are enabled. It returns the same app for chaining.
//
// Default: read timout
func (me *App) WithIdleTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.httpServer.IdleTimeout = d
	return me
}

// WithMaxHeaderBytes controls the maximum number of bytes the server will read
// while parsing request header keys and values, including the request line.
// It does not limit the size of the request body.
// It returns the same app for chaining.
//
// Default: [http.DefaultMaxHeaderBytes]
func (me *App) WithMaxHeaderBytes(i int) *App {
	Assert(i > 0)
	me.httpServer.MaxHeaderBytes = i
	return me
}

// WithMaxHeaderValueCount controls the maximum number of header values that
// the server will parse from a request. Comma-separated values in a
// single header line are counted once, while values sent as multiple
// header lines are counted separately.
// It returns the same app for chaining.
//
// Default: [http.DefaultMaxHeaderValueCount]
func (me *App) WithMaxHeaderValueCount(i int) *App {
	Assert(i > 0)
	me.httpServer.MaxHeaderValueCount = i
	return me
}

// WithPrefork determines whether to use a prefork listener for the server.
// If enabled, the app starts multiple child processes that listen on the same
// port by enabling the SO_REUSEPORT socket option.
// It returns the same app for chaining.
//
// Default: false
func (me *App) WithPrefork(b bool) *App {
	me.preforkIsEnabled = b
	return me
}

// WithPreforkChildrenCount sets the number of child processes to start when
// prefork is enabled. It returns the same app for chaining.
//
// Default: number of logical CPUs from [runtime.NumCPU]
func (me *App) WithPreforkChildrenCount(i int) *App {
	Assert(i > 0)
	me.preforkChildrenCount = i
	return me
}

// WithPreforkRetriesCount sets the number of times the prefork parent will
// respawn a worker child process after it stops or crashes before giving up.
// Once this limit is reached, [ErrPreforkRetriesExceeded] is returned from
// [App.Start]. It returns the same app for chaining.
//
// Default: infinite retries
func (me *App) WithPreforkRetriesCount(i int) *App {
	Assert(i >= 0)
	me.preforkRetriesCount = i
	return me
}

// WithTls allows the server to use TLS when listening.
// certFile specifies the path to the TLS certificate file.
// keyFile specifies the path to the TLS private key file.
// It returns the same app for chaining.
//
// Default: no TLS
func (me *App) WithTls(certFile, keyFile string) *App {
	Assert(certFile != "" && keyFile != "")
	me.useTls = true
	me.certFile = certFile
	me.keyFile = keyFile
	return me
}

// WithTlsConfig provides a TLS configuration for the server.
// The configuration is cloned before use by the server.
// It returns the same app for chaining.
func (me *App) WithTlsConfig(c *tls.Config) *App {
	Assert(c != nil)
	me.httpServer.TLSConfig = c
	return me
}

// WithHttp2Config configures HTTP/2 connections.
// It returns the same app for chaining.
func (me *App) WithHttp2Config(c *http.HTTP2Config) *App {
	Assert(c != nil)
	me.httpServer.HTTP2 = c
	return me
}

// WithProtocols specifies the set of protocols accepted by the server.
//
// If Protocols includes unencrypted HTTP/2, the server accepts
// unencrypted HTTP/2 connections. The server can serve both HTTP/1
// and unencrypted HTTP/2 on the same address and port.
// It returns the same app for chaining.
//
// Default: HTTP/1 and HTTP/2
func (me *App) WithProtocols(p *http.Protocols) *App {
	Assert(p != nil)
	me.httpServer.Protocols = p
	return me
}

// WithClientPriority determines whether client-specified HTTP/2
// priorities, as specified in RFC 9218, are respected.
//
// This field only takes effect when using HTTP/2 and when no custom
// write scheduler is configured. If false, requests are served in
// round-robin order without prioritization.
// It returns the same app for chaining.
//
// Default: true
func (me *App) WithClientPriority(b bool) *App {
	me.httpServer.DisableClientPriority = !b
	return me
}

// WithServiceStartTimeout sets the maximum duration for starting each service.
// The timeout is enforced per service through the context passed to [Service.Start].
// A service that does not finish in time fails with a context error and is skipped.
// It returns the same app for chaining.
//
// Default: no timeout
func (me *App) WithServiceStartTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.serviceStartTimeout = d
	return me
}

// WithServiceStopTimeout sets the maximum duration for stopping each service.
// The timeout is enforced per service through the context passed to [Service.Stop].
// A service that does not finish in time fails with a context error, which is logged.
// It returns the same app for chaining.
//
// Default: no timeout
func (me *App) WithServiceStopTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.serviceStopTimeout = d
	return me
}

// WithParallelServiceStart determines whether services are started concurrently
// instead of one after another. It returns the same app for chaining.
//
// Default: false
func (me *App) WithParallelServiceStart() *App {
	me.serviceStartParallel = true
	return me
}

// WithParallelServiceStop determines whether services are stopped concurrently
// instead of one after another. It returns the same app for chaining.
//
// Default: false
func (me *App) WithParallelServiceStop() *App {
	me.serviceStopParallel = true
	return me
}

// WithShutdownTimeout sets the maximum duration for http server shutdown.
// It returns the same app for chaining.
//
// Default: no timeout
func (me *App) WithShutdownTimeout(d time.Duration) *App {
	Assert(d > 0)
	me.shutdownTimeout = d
	return me
}

// WithPassLocalsToContext mirrors request-scoped locals into [http.Request] context.
//
// Locals themselves live in a per-request map[string]any (no locking:
// handlers run linearly via [Context.Next] in one goroutine).
// If true, [Context.SetLocal] also calls context.WithValue, and
// [Context.DeleteLocal] shadows the key with nil (contexts can't delete).
// Enable only for interop with stdlib/middleware reading r.Context().Value().
// It returns the same app for chaining.
//
// Default: false
func (me *App) WithPassLocalsToContext(b bool) *App {
	me.passLocalsToContext = b
	return me
}

// Test dispatches request through the app without listening on a port.
//
// Services are not started automatically. When handlers depend on services,
// start them explicitly with [App.StartServices] and stop them with
// [App.StopServices], e.g. TestMain or t.Cleanup setups. [Context.GetService]
// panics for services that were registered but never successfully started:
//
//	if err := app.StartServices(); err != nil {
//		t.Fatalf("failed to start services: %v", err)
//	}
//	t.Cleanup(app.StopServices)
//	resp := app.Test(req)
func (me *App) Test(request *http.Request) *http.Response {
	recorder := httptest.NewRecorder()
	me.httpServer.Handler.ServeHTTP(recorder, request)
	return recorder.Result()
}
