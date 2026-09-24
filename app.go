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
// dependencies and managed services. Build it with [New], register routes
// with [App.Handle] and [App.Use], then serve with [App.Start] and stop
// with [App.Shutdown]. Use [App.Test] to exercise handlers without listening.
type App struct {
	httpServer                      *http.Server
	routes                          []Route
	state                           sync.Map
	dependencies                    map[reflect.Type]any
	services                        map[reflect.Type]any
	startedServices                 map[reflect.Type]any
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

// New creates an App with sensible defaults, applies optionFuncs and registers
// the root handler. Serve it with [App.Start].
func New(optionFuncs ...AppOptionFunc) *App {
	app := &App{
		httpServer:           new(http.Server),
		dependencies:         make(map[reflect.Type]any),
		services:             make(map[reflect.Type]any),
		startedServices:      make(map[reflect.Type]any),
		logger:               slog.Default(),
		errorHandler:         DefaultErrorHandler,
		enableRequestLogging: true,
		preforkChildrenCount: runtime.NumCPU(),
		preforkRetriesCount:  5,
	}

	for _, optFunc := range optionFuncs {
		optFunc(app)
	}

	app.registerReservedRequestLoggingEntries()
	app.registerRootHandler()

	return app
}

// AppOptionFunc configures an [App]. Pass option funcs to [New].
type AppOptionFunc func(app *App)

// WithListenAddress sets the TCP address the server listens on,
// e.g. ":8080" or "127.0.0.1:3000".
//
// Default: ":http" (port 80), or ":https" (port 443) when TLS is
// enabled via [WithTls].
func WithListenAddress(address string) AppOptionFunc {
	return func(app *App) {
		app.listenAddress = address
	}
}

// Logger is used for all internal logging within the app and its contexts.
// It is also used for request logging when [EnableRequestLogging] is true.
//
// Default: [slog.Default]
func WithLogger(l *slog.Logger) AppOptionFunc {
	Assert(l != nil)
	return func(app *App) {
		app.logger = l
	}
}

// ErrorHandler is used to handle errors returned by the handler chain,
// including [ErrInvalidEndpoint] for unknown paths and
// [ErrMethodNotAllowed] for unregistered methods, so a custom handler can
// inspect or override them.
//
// Default: [DefaultErrorHandler]
func WithErrorHandler(eh ErrorHandler) AppOptionFunc {
	Assert(eh != nil)
	return func(app *App) {
		app.errorHandler = eh
	}
}

// WithRequestLogging determines whether to log request handling results,
// such as response time, status code, remote address, error, etc.
// Every request routed through the app is logged, including unmatched
// paths/methods handled as [ErrInvalidEndpoint]/[ErrMethodNotAllowed].
// The logged status defaults to 200 when no response was written.
//
// Default: true
func WithRequestLogging(b bool) AppOptionFunc {
	return func(app *App) {
		app.enableRequestLogging = b
	}
}

// WithGeneralOptionsHandler determines whether the server should pass
// general OPTIONS requests to the Handler. If true, the server responds
// with a 200 OK status and a Content-Length of 0.
//
// Default: true
func WithGeneralOptionsHandler(b bool) AppOptionFunc {
	return func(app *App) {
		app.httpServer.DisableGeneralOptionsHandler = !b
	}
}

// WithReadTimeout sets the maximum duration for reading the entire request,
// including the body.
//
// Default: no timeout
func WithReadTimeout(d time.Duration) AppOptionFunc {
	Assert(d > 0)
	return func(app *App) {
		app.httpServer.ReadTimeout = d
	}
}

// WithReadHeaderTimeout sets the maximum duration for reading request headers.
// The connection's read deadline is reset after the headers are read,
// allowing the Handler to decide what is considered too slow for the body.
//
// Default: read timout
func WithReadHeaderTimeout(d time.Duration) AppOptionFunc {
	Assert(d > 0)
	return func(app *App) {
		app.httpServer.ReadHeaderTimeout = d
	}
}

// WithWriteTimeout sets the maximum duration for writing the response. It is
// reset whenever a new request's header is read. Like ReadTimeout, it
// does not allow Handlers to make per-request decisions.
//
// Default: no timeout
func WithWriteTimeout(d time.Duration) AppOptionFunc {
	Assert(d > 0)
	return func(app *App) {
		app.httpServer.WriteTimeout = d
	}
}

// WithIdleTimeout sets the maximum amount of time to wait for the next request
// when keep-alives are enabled.
//
// Default: read timout
func WithIdleTimeout(d time.Duration) AppOptionFunc {
	return func(app *App) {
		Assert(d > 0)
		app.httpServer.IdleTimeout = d
	}
}

// WithMaxHeaderBytes controls the maximum number of bytes the server will read
// while parsing request header keys and values, including the request line.
// It does not limit the size of the request body.
//
// Default: [http.DefaultMaxHeaderBytes]
func WithMaxHeaderBytes(i int) AppOptionFunc {
	Assert(i > 0)
	return func(app *App) {
		app.httpServer.MaxHeaderBytes = i
	}
}

// WithMaxHeaderValueCount controls the maximum number of header values that
// the server will parse from a request. Comma-separated values in a
// single header line are counted once, while values sent as multiple
// header lines are counted separately.
//
// Default: [http.DefaultMaxHeaderValueCount]
func WithMaxHeaderValueCount(i int) AppOptionFunc {
	Assert(i > 0)
	return func(app *App) {
		app.httpServer.MaxHeaderValueCount = i
	}
}

// WithPrefork determines whether to use a prefork listener for the server.
// If enabled, the app starts multiple child processes that listen on the same
// port by enabling the SO_REUSEPORT socket option.
//
// Default: false
func WithPrefork(b bool) AppOptionFunc {
	return func(app *App) {
		app.preforkIsEnabled = b
	}
}

// WithPreforkChildrenCount sets the number of child processes to start when
// prefork is enabled.
//
// Default: number of logical CPUs from [runtime.NumCPU]
func WithPreforkChildrenCount(i int) AppOptionFunc {
	Assert(i > 0)
	return func(app *App) {
		app.preforkChildrenCount = i
	}
}

// WithPreforkRetriesCount sets the number of times the prefork parent will
// respawn a worker child process after it stops or crashes before giving up.
// Once this limit is reached, [ErrPreforkRetriesExceeded] is returned from
// [App.Start].
//
// Default: 5
func WithPreforkRetriesCount(i int) AppOptionFunc {
	Assert(i >= 0)
	return func(app *App) {
		app.preforkRetriesCount = i
	}
}

// WithTls allows the server to use TLS when listening.
// certFile specifies the path to the TLS certificate file.
// keyFile specifies the path to the TLS private key file.
//
// Default: no TLS
func WithTls(certFile, keyFile string) AppOptionFunc {
	Assert(certFile != "" && keyFile != "")
	return func(app *App) {
		app.useTls = true
		app.certFile = certFile
		app.keyFile = keyFile
	}
}

// WithTlsConfig provides a TLS configuration for the server.
// The configuration is cloned before use by the server.
func WithTlsConfig(c *tls.Config) AppOptionFunc {
	Assert(c != nil)
	return func(app *App) {
		app.httpServer.TLSConfig = c
	}
}

// WithHttp2Config configures HTTP/2 connections.
func WithHttp2Config(c *http.HTTP2Config) AppOptionFunc {
	Assert(c != nil)
	return func(app *App) {
		app.httpServer.HTTP2 = c
	}
}

// WithProtocols specifies the set of protocols accepted by the server.
//
// If Protocols includes unencrypted HTTP/2, the server accepts
// unencrypted HTTP/2 connections. The server can serve both HTTP/1
// and unencrypted HTTP/2 on the same address and port.
//
// Default: HTTP/1 and HTTP/2
func WithProtocols(p *http.Protocols) AppOptionFunc {
	Assert(p != nil)
	return func(app *App) {
		app.httpServer.Protocols = p
	}
}

// WithClientPriority determines whether client-specified HTTP/2
// priorities, as specified in RFC 9218, are respected.
//
// This field only takes effect when using HTTP/2 and when no custom
// write scheduler is configured. If false, requests are served in
// round-robin order without prioritization.
//
// Default: true
func WithClientPriority(b bool) AppOptionFunc {
	return func(app *App) {
		app.httpServer.DisableClientPriority = !b
	}
}

// WithServiceStartTimeout sets the maximum duration for starting each service.
// The timeout is enforced per service through the context passed to [Service.Start].
// A service that does not finish in time fails with a context error and is skipped.
//
// Default: no timeout
func WithServiceStartTimeout(d time.Duration) AppOptionFunc {
	Assert(d > 0)
	return func(app *App) {
		app.serviceStartTimeout = d
	}
}

// WithServiceStopTimeout sets the maximum duration for stopping each service.
// The timeout is enforced per service through the context passed to [Service.Stop].
// A service that does not finish in time fails with a context error, which is logged.
//
// Default: no timeout
func WithServiceStopTimeout(d time.Duration) AppOptionFunc {
	Assert(d > 0)
	return func(app *App) {
		app.serviceStopTimeout = d
	}
}

// WithParallelServiceStart determines whether services are started concurrently
// instead of one after another.
//
// Default: false
func WithParallelServiceStart() AppOptionFunc {
	return func(app *App) {
		app.serviceStartParallel = true
	}
}

// WithParallelServiceStop determines whether services are stopped concurrently
// instead of one after another.
//
// Default: false
func WithParallelServiceStop() AppOptionFunc {
	return func(app *App) {
		app.serviceStopParallel = true
	}
}

// WithShutdownTimeout sets the maximum duration for http server shutdown.
//
// Default: no timeout
func WithShutdownTimeout(d time.Duration) AppOptionFunc {
	Assert(d > 0)
	return func(app *App) {
		app.shutdownTimeout = d
	}
}

// WithPassLocalsToContext mirrors request-scoped locals into [http.Request] context.
//
// Locals themselves live in a per-request map[string]any (no locking:
// handlers run linearly via [Context.Next] in one goroutine).
// If true, [Context.SetLocal] also calls context.WithValue, and
// [Context.DeleteLocal] shadows the key with nil (contexts can't delete).
// Enable only for interop with stdlib/middleware reading r.Context().Value().
//
// Default: false
func WithPassLocalsToContext(b bool) AppOptionFunc {
	return func(app *App) {
		app.passLocalsToContext = b
	}
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
