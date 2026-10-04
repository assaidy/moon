package moon

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// App is the HTTP server, router and owner of shared state, typed
// dependencies and managed services. Build it with [New], chain the With*
// methods to configure it, register routes with [App.Map] and [App.Use],
// then serve with [App.Start] and stop with [App.Shutdown]. Use [App.Test]
// to exercise handlers without listening.
type App struct {
	httpServer      *http.Server
	routesPerMethod map[string][]RouteEntry // method -> routes
	middlewares     []MiddlewareEntry
	nextOrder       atomic.Int64
	state           sync.Map
	dependencies    map[reflect.Type]any
	services        []serviceInfo
	started         bool
	options         AppOptions
}

// New creates an App with the given options, or sensible defaults when
// none are given, and registers the root handler.
// Register routes with [App.Map] and [App.Use], then serve with [App.Start]:
//
//	app := New()
//	app := New(NewAppOptions().WithListenAddress(":3000"))
func New(opts ...AppOptions) *App {
	options := NewAppOptions()
	if len(opts) > 0 {
		Assert(len(opts) == 1)
		options = opts[0]
	}
	// Every app owns its entry list, so AddRequestLoggingEntry never writes
	// into DefaultRequestLoggingEntries or into an app sharing these options.
	options.requestLoggingEntries = slices.Clone(options.requestLoggingEntries)

	app := &App{
		routesPerMethod: make(map[string][]RouteEntry),
		httpServer: &http.Server{
			DisableGeneralOptionsHandler: !options.enableGeneralOptionsHandler,
			ReadTimeout:                  options.readTimeout,
			ReadHeaderTimeout:            options.readHeaderTimeout,
			WriteTimeout:                 options.writeTimeout,
			IdleTimeout:                  options.idleTimeout,
			MaxHeaderBytes:               options.maxHeaderBytes,
			MaxHeaderValueCount:          options.maxHeaderValueCount,
			TLSConfig:                    options.tlsConfig,
			HTTP2:                        options.http2Config,
			Protocols:                    options.protocols,
			DisableClientPriority:        !options.clientPriority,
		},
		dependencies: make(map[reflect.Type]any),
		options:      options,
	}

	app.registerRootHandler()

	return app
}

// AppOptions holds the user-configurable settings of an [App].
// All fields are private; build one with [NewAppOptions] for the defaults,
// chain the With* methods to configure it, and pass it to [New].
type AppOptions struct {
	listenAddress               string
	logger                      *slog.Logger
	errorHandler                ErrorHandler
	enableRequestLogging        bool
	enableGeneralOptionsHandler bool
	readTimeout                 time.Duration
	readHeaderTimeout           time.Duration
	writeTimeout                time.Duration
	idleTimeout                 time.Duration
	maxHeaderBytes              int
	maxHeaderValueCount         int
	preforkIsEnabled            bool
	preforkChildrenCount        int
	preforkRetriesCount         int
	useTls                      bool
	certFile                    string
	keyFile                     string
	tlsConfig                   *tls.Config
	http2Config                 *http.HTTP2Config
	protocols                   *http.Protocols
	clientPriority              bool
	serviceStartTimeout         time.Duration
	serviceStopTimeout          time.Duration
	serviceStartParallel        bool
	serviceStopParallel         bool
	shutdownTimeout             time.Duration
	passLocalsToContext         bool
	readLimit                   int
	requestLoggingEntries       []RequestLoggingEntry
}

// NewAppOptions returns an AppOptions populated with the default values.
// Chain the With* methods to configure it, then pass it to [New]:
//
//	app := New(NewAppOptions().WithListenAddress(":3000"))
//
// See the With* methods for each default.
func NewAppOptions() AppOptions {
	return AppOptions{
		logger:                slog.Default(),
		errorHandler:          DefaultErrorHandler,
		clientPriority:        true,
		preforkChildrenCount:  runtime.NumCPU(),
		preforkRetriesCount:   -1,
		readLimit:             4 << 20, // 4MB
		requestLoggingEntries: DefaultRequestLoggingEntries,
	}
}

// WithListenAddress sets the TCP address the server listens on,
// e.g. ":8080" or "127.0.0.1:3000". It returns the same options for chaining.
//
// Default: ":http" (port 80), or ":https" (port 443) when TLS is
// enabled via [AppOptions.WithTls].
func (me AppOptions) WithListenAddress(address string) AppOptions {
	me.listenAddress = address
	return me
}

// WithLogger sets the logger used for all internal logging within the app
// and its contexts. It is also used for request logging when
// [AppOptions.WithRequestLogging] is enabled. It panics on a nil logger. It returns
// the same app for chaining.
//
// Default: [slog.Default]
func (me AppOptions) WithLogger(l *slog.Logger) AppOptions {
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
func (me AppOptions) WithErrorHandler(eh ErrorHandler) AppOptions {
	Assert(eh != nil)
	me.errorHandler = eh
	return me
}

// WithRequestLogging determines whether every request routed through the
// app is logged as a "request handled" record on the logger, with one
// attribute per entry of the request logging list (see
// [AppOptions.WithRequestLoggingEntries] and [App.AddRequestLoggingEntry]).
// Unmatched paths and methods handled as [ErrInvalidEndpoint] or
// [ErrMethodNotAllowed] are logged too.
// The logged status defaults to 200 when no response was written.
// It returns the same options for chaining.
//
// Default: false
func (me AppOptions) WithRequestLogging(b bool) AppOptions {
	me.enableRequestLogging = b
	return me
}

// WithRequestLoggingEntries replaces the request logging list of the app,
// so the attributes logged for every handled request can be reset or built
// from scratch instead of starting from [DefaultRequestLoggingEntries].
// Append to the list afterwards with [App.AddRequestLoggingEntry].
//
// Each key is trimmed. It panics on an empty or whitespace-only key, on a
// nil value, or when the same key appears twice in entries. Keys only
// have to be distinct within entries: a builtin key such as "status" is
// logged only when its entry is part of the list. Passing nil or an empty
// slice logs no attributes, just the "request handled" message.
// It returns the same options for chaining.
//
// Default: [DefaultRequestLoggingEntries]
func (me AppOptions) WithRequestLoggingEntries(entries []RequestLoggingEntry) AppOptions {
	validated := make([]RequestLoggingEntry, len(entries))
	for i, e := range entries {
		e.Key = strings.TrimSpace(e.Key)
		Assert(e.Key != "", "key cannot be empty or whitespace")
		Assert(e.Value != nil, "value func cannot be nil")

		duplicateIndex := slices.IndexFunc(
			validated[:i],
			func(v RequestLoggingEntry) bool { return v.Key == e.Key },
		)
		Assert(
			duplicateIndex == -1,
			fmt.Sprintf("request logging entry key %q is already registered at index %d", e.Key, duplicateIndex),
		)

		validated[i] = e
	}
	me.requestLoggingEntries = validated
	return me
}

// WithGeneralOptionsHandler determines whether the server should pass
// general OPTIONS requests to the Handler. If true, the server responds
// with a 200 OK status and a Content-Length of 0.
// It returns the same options for chaining.
//
// Default: false
func (me AppOptions) WithGeneralOptionsHandler(b bool) AppOptions {
	me.enableGeneralOptionsHandler = b
	return me
}

// WithReadTimeout sets the maximum duration for reading the entire request,
// including the body. It returns the same options for chaining.
//
// Default: no timeout
func (me AppOptions) WithReadTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.readTimeout = d
	return me
}

// WithReadHeaderTimeout sets the maximum duration for reading request headers.
// The connection's read deadline is reset after the headers are read,
// allowing the Handler to decide what is considered too slow for the body.
// It returns the same options for chaining.
//
// Default: read timout
func (me AppOptions) WithReadHeaderTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.readHeaderTimeout = d
	return me
}

// WithWriteTimeout sets the maximum duration for writing the response. It is
// reset whenever a new request's header is read. Like ReadTimeout, it
// does not allow Handlers to make per-request decisions.
// It returns the same options for chaining.
//
// Default: no timeout
func (me AppOptions) WithWriteTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.writeTimeout = d
	return me
}

// WithIdleTimeout sets the maximum amount of time to wait for the next request
// when keep-alives are enabled. It returns the same options for chaining.
//
// Default: read timout
func (me AppOptions) WithIdleTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.idleTimeout = d
	return me
}

// WithMaxHeaderBytes controls the maximum number of bytes the server will read
// while parsing request header keys and values, including the request line.
// It does not limit the size of the request body.
// It returns the same options for chaining.
//
// Default: [http.DefaultMaxHeaderBytes] (1MB)
func (me AppOptions) WithMaxHeaderBytes(i int) AppOptions {
	Assert(i > 0)
	me.maxHeaderBytes = i
	return me
}

// WithMaxHeaderValueCount controls the maximum number of header values that
// the server will parse from a request. Comma-separated values in a
// single header line are counted once, while values sent as multiple
// header lines are counted separately.
// It returns the same options for chaining.
//
// Default: [http.DefaultMaxHeaderValueCount] (500)
func (me AppOptions) WithMaxHeaderValueCount(i int) AppOptions {
	Assert(i > 0)
	me.maxHeaderValueCount = i
	return me
}

// WithReadLimit sets the maximum number of bytes accepted for a request
// body. Reads past the limit fail, both via [Context.Read] and raw reads of
// the request body. Bodies declaring more than the limit are rejected without
// reading them. Over-limit failures are reported as [ErrRequestEntityTooLarge] (413).
// It returns the same options for chaining.
//
// Default: 4MB
func (me AppOptions) WithReadLimit(n int) AppOptions {
	Assert(n > 0)
	me.readLimit = n
	return me
}

// WithPrefork determines whether to use a prefork listener for the server.
// If enabled, the app starts multiple child processes that listen on the same
// port by enabling the SO_REUSEPORT socket option.
// It returns the same options for chaining.
//
// Default: false
func (me AppOptions) WithPrefork(b bool) AppOptions {
	me.preforkIsEnabled = b
	return me
}

// WithPreforkChildrenCount sets the number of child processes to start when
// prefork is enabled. It returns the same options for chaining.
//
// Default: number of logical CPUs from [runtime.NumCPU]
func (me AppOptions) WithPreforkChildrenCount(i int) AppOptions {
	Assert(i > 0)
	me.preforkChildrenCount = i
	return me
}

// WithPreforkRetriesCount sets the number of times the prefork parent will
// respawn a worker child process after it stops or crashes before giving up.
// Once this limit is reached, [ErrPreforkRetriesExceeded] is returned from
// [App.Start]. It returns the same options for chaining.
//
// Default: infinite retries
func (me AppOptions) WithPreforkRetriesCount(i int) AppOptions {
	Assert(i >= 0)
	me.preforkRetriesCount = i
	return me
}

// WithTls allows the server to use TLS when listening.
// certFile specifies the path to the TLS certificate file.
// keyFile specifies the path to the TLS private key file.
// It returns the same options for chaining.
//
// Default: no TLS
func (me AppOptions) WithTls(certFile, keyFile string) AppOptions {
	Assert(certFile != "" && keyFile != "")
	me.useTls = true
	me.certFile = certFile
	me.keyFile = keyFile
	return me
}

// WithTlsConfig provides a TLS configuration for the server.
// The configuration is cloned before use by the server.
// It returns the same options for chaining.
func (me AppOptions) WithTlsConfig(c *tls.Config) AppOptions {
	Assert(c != nil)
	me.tlsConfig = c
	return me
}

// WithHttp2Config configures HTTP/2 connections.
// It returns the same options for chaining.
func (me AppOptions) WithHttp2Config(c *http.HTTP2Config) AppOptions {
	Assert(c != nil)
	me.http2Config = c
	return me
}

// WithProtocols specifies the set of protocols accepted by the server.
//
// If Protocols includes unencrypted HTTP/2, the server accepts
// unencrypted HTTP/2 connections. The server can serve both HTTP/1
// and unencrypted HTTP/2 on the same address and port.
// It returns the same options for chaining.
//
// Default: HTTP/1 and HTTP/2
func (me AppOptions) WithProtocols(p *http.Protocols) AppOptions {
	Assert(p != nil)
	me.protocols = p
	return me
}

// WithClientPriority determines whether client-specified HTTP/2
// priorities, as specified in RFC 9218, are respected.
//
// This field only takes effect when using HTTP/2 and when no custom
// write scheduler is configured. If false, requests are served in
// round-robin order without prioritization.
// It returns the same options for chaining.
//
// Default: true
func (me AppOptions) WithClientPriority(b bool) AppOptions {
	me.clientPriority = b
	return me
}

// WithServiceStartTimeout sets the maximum duration for starting each service.
// The timeout is enforced per service through the context passed to [Service.Start].
// A service that does not finish in time fails with a context error and is skipped.
// It returns the same options for chaining.
//
// Default: no timeout
func (me AppOptions) WithServiceStartTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.serviceStartTimeout = d
	return me
}

// WithServiceStopTimeout sets the maximum duration for stopping each service.
// The timeout is enforced per service through the context passed to [Service.Stop].
// A service that does not finish in time fails with a context error, which is logged.
// It returns the same options for chaining.
//
// Default: no timeout
func (me AppOptions) WithServiceStopTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.serviceStopTimeout = d
	return me
}

// WithParallelServiceStart determines whether services are started concurrently
// instead of one after another. It returns the same options for chaining.
//
// Default: false
func (me AppOptions) WithParallelServiceStart() AppOptions {
	me.serviceStartParallel = true
	return me
}

// WithParallelServiceStop determines whether services are stopped concurrently
// instead of one after another. It returns the same options for chaining.
//
// Default: false
func (me AppOptions) WithParallelServiceStop() AppOptions {
	me.serviceStopParallel = true
	return me
}

// WithShutdownTimeout sets the maximum duration for http server shutdown.
// It returns the same options for chaining.
//
// Default: no timeout
func (me AppOptions) WithShutdownTimeout(d time.Duration) AppOptions {
	Assert(d > 0)
	me.shutdownTimeout = d
	return me
}

// WithPassLocalsToContext mirrors request-scoped locals into [http.Request] context.
//
// Locals themselves live in a per-request map[string]any (no locking:
// handlers run linearly via [Context.Next] in one goroutine).
// If true, [Context.SetLocal] also calls [context.WithValue], and
// [Context.DeleteLocal] shadows the key with nil (contexts can't delete).
// Enable only for interop with stdlib/middleware reading r.Context().Value().
// It returns the same options for chaining.
//
// Default: false
func (me AppOptions) WithPassLocalsToContext(b bool) AppOptions {
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
