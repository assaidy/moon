package moon

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// App is the HTTP server, router and owner of shared state, typed
// dependencies and managed services. Build it with [New] plus [AppOption]s,
// register routes with [App.Map] and [App.Use], then serve with [App.Start]
// and stop with [App.Shutdown]. Use [App.RunUntilSignal] to start, wait for a
// signal, and shut down in one call. Use [App.Test] to exercise handlers
// without listening.
type App struct {
	httpServer       *http.Server
	routesPerMethod  map[string][]*routeEntry // method -> routes
	middlewares      []*middlewareEntry
	nextRoutingOrder atomic.Int64

	state        sync.Map
	dependencies map[reflect.Type]any
	services     []serviceInfo

	rleMutex sync.RWMutex

	childProcessesMutex     sync.RWMutex
	childProcesses          map[*os.Process]struct{}
	childProcessesWaitGroup sync.WaitGroup
	childProcessResultChan  chan childProcessResult
	shutdownChan            chan struct{}
	shuttingDown            atomic.Bool
	allChildrenStoppedChan  chan struct{}

	options appOptions
}

// New creates an App with sensible defaults, customized by the given options,
// and registers the root handler.
// Register routes with [App.Map] and [App.Use], then serve with [App.Start]:
//
//	app := New()
//	app := New(WithListenAddress(":3000"))
func New(opts ...AppOption) *App {
	options := appOptions{
		logger:               slog.Default(),
		errorHandler:         DefaultErrorHandler,
		preforkChildrenCount: runtime.NumCPU(),
		preforkRetriesCount:  -1,
		clientPriority:       true,
		readTimeout:          -1,
		writeTimeout:         -1,
		idleTimeout:          -1,
		shutdownTimeout:      -1,
		maxBodyBytes:         4 << 20, // 4MB
		rle:                  defaultRequestLoggingEntries,
	}
	for _, opt := range opts {
		opt(&options)
	}
	// Every app owns its entry list, so [App.AddRequestLoggingEntry] never
	// writes into an app sharing these options, nor into the defaults every
	// app starts from.
	options.rle = slices.Clone(options.rle)

	app := &App{
		routesPerMethod: make(map[string][]*routeEntry),
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
		dependencies:           make(map[reflect.Type]any),
		shutdownChan:           make(chan struct{}),
		allChildrenStoppedChan: make(chan struct{}),
		options:                options,
	}

	app.registerRootHandler()

	return app
}

// appOptions holds the user-configurable settings of an [App].
// All fields are private; pass [AppOption]s to [New] to configure them.
type appOptions struct {
	listenAddress               string
	logger                      *slog.Logger
	errorHandler                Handler
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
	serviceStartParallel        bool
	serviceStopParallel         bool
	shutdownTimeout             time.Duration
	passLocalsToContext         bool
	maxBodyBytes                int
	rle                         []RequestLoggingEntry
}

// AppOption configures an [App]. Pass them to [New]:
//
//	app := New(WithListenAddress(":3000"), WithRequestLogging(true))
type AppOption func(*appOptions)

// WithListenAddress sets the TCP address the server listens on,
// e.g. ":8080" or "127.0.0.1:3000". Pass it to [New].
//
// Default: ":http" (port 80), or ":https" (port 443) when TLS is
// enabled via [WithTls].
func WithListenAddress(address string) AppOption {
	return func(o *appOptions) {
		o.listenAddress = address
	}
}

// WithLogger sets the logger used for all internal logging within the app
// and its contexts. It is also used for request logging when
// [WithRequestLogging] is enabled. It panics on a nil logger. Pass it to [New].
//
// Default: [slog.Default]
func WithLogger(l *slog.Logger) AppOption {
	Assert(l != nil)
	return func(o *appOptions) {
		o.logger = l
	}
}

// WithErrorHandler sets the handler used to handle errors returned by the
// handler chain, including [ErrNotFound] for unknown paths and
// [ErrMethodNotAllowed] for unregistered methods, so a custom handler can
// inspect or override them. It is registered as the first handler in the
// chain, so it wraps every middleware and route handler: it must call
// [Context.Next] and handle the error that comes back.
//
// A nil handler disables the built-in one. The chain still runs, but no
// handler renders errors unless you register one yourself, which lets a
// measuring middleware such as the response_time handler count error
// rendering time by registering [DefaultErrorHandler] after itself.
//
// Default: [DefaultErrorHandler]
func WithErrorHandler(eh Handler) AppOption {
	return func(o *appOptions) {
		o.errorHandler = eh
	}
}

// WithRequestLogging determines whether every request routed through the
// app is logged as a "request handled" record on the logger, with one
// attribute per entry of the request logging list (see
// [WithRequestLoggingEntries] and [App.AddRequestLoggingEntry]).
// Unmatched paths and methods handled as [ErrNotFound] or
// [ErrMethodNotAllowed] are logged too.
// The logged status defaults to 200 when no response was written.
// Pass it to [New].
//
// Default: false
func WithRequestLogging(b bool) AppOption {
	return func(o *appOptions) {
		o.enableRequestLogging = b
	}
}

// WithRequestLoggingEntries replaces the request logging list of the app,
// so the attributes logged for every handled request can be reset or built
// from scratch instead of starting from the defaults: [RleDuration],
// [RleRemote], [RleMethod], [RlePath], [RleStatus] and [RleError], in that
// order. Append to the list afterwards with [App.AddRequestLoggingEntry].
// Every other builtin entry, any Rle* outside that list, is opt-in: it is
// logged only when it is appended to the list.
//
// Each key is trimmed. It panics on an empty or whitespace-only key, on a
// nil value, or when the same key appears twice in entries. Keys only
// have to be distinct within entries: a builtin key such as "status" is
// logged only when its entry is part of the list. Passing nil or an empty
// slice logs no attributes, just the "request handled" message.
// Pass it to [New].
//
// Default: [RleDuration], [RleRemote], [RleMethod], [RlePath], [RleStatus]
// and [RleError]
func WithRequestLoggingEntries(entries []RequestLoggingEntry) AppOption {
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

	return func(o *appOptions) {
		o.rle = validated
	}
}

// WithGeneralOptionsHandler determines whether the server should pass
// general OPTIONS requests to the Handler. If true, the server responds
// with a 200 OK status and a Content-Length of 0.
// Pass it to [New].
//
// Default: false
func WithGeneralOptionsHandler(b bool) AppOption {
	return func(o *appOptions) {
		o.enableGeneralOptionsHandler = b
	}
}

// WithReadTimeout sets the maximum duration for reading the entire request,
// including the body. A negative value disables the timeout. It panics on a
// zero duration. Pass it to [New].
//
// Default: no timeout
func WithReadTimeout(d time.Duration) AppOption {
	Assert(d != 0)
	return func(o *appOptions) {
		o.readTimeout = d
	}
}

// WithReadHeaderTimeout sets the maximum duration for reading request headers.
// The connection's read deadline is reset after the headers are read,
// allowing the Handler to decide what is considered too slow for the body.
// A negative value disables the timeout. It panics on a zero duration.
// Pass it to [New].
//
// Default: the value of [WithReadTimeout]
func WithReadHeaderTimeout(d time.Duration) AppOption {
	Assert(d != 0)
	return func(o *appOptions) {
		o.readHeaderTimeout = d
	}
}

// WithWriteTimeout sets the maximum duration for writing the response. It is
// reset whenever a new request's header is read. Like ReadTimeout, it
// does not allow Handlers to make per-request decisions. A negative value
// disables the timeout. It panics on a zero duration.
// Pass it to [New].
//
// Default: no timeout
func WithWriteTimeout(d time.Duration) AppOption {
	Assert(d != 0)
	return func(o *appOptions) {
		o.writeTimeout = d
	}
}

// WithIdleTimeout sets the maximum amount of time to wait for the next request
// when keep-alives are enabled. A negative value disables the timeout. It
// panics on a zero duration. Pass it to [New].
//
// Default: no timeout
func WithIdleTimeout(d time.Duration) AppOption {
	Assert(d != 0)
	return func(o *appOptions) {
		o.idleTimeout = d
	}
}

// WithMaxHeaderBytes controls the maximum number of bytes the server will read
// while parsing request header keys and values, including the request line.
// It does not limit the size of the request body.
// Pass it to [New].
//
// Default: [http.DefaultMaxHeaderBytes] (1 MiB)
func WithMaxHeaderBytes(i int) AppOption {
	Assert(i > 0)
	return func(o *appOptions) {
		o.maxHeaderBytes = i
	}
}

// WithMaxHeaderValueCount controls the maximum number of header values that
// the server will parse from a request. Comma-separated values in a
// single header line are counted once, while values sent as multiple
// header lines are counted separately.
// Pass it to [New].
//
// Default: [http.DefaultMaxHeaderValueCount] (500)
func WithMaxHeaderValueCount(i int) AppOption {
	Assert(i > 0)
	return func(o *appOptions) {
		o.maxHeaderValueCount = i
	}
}

// WithMaxBodyBytes sets the maximum number of bytes accepted for a request
// body. Reads past the limit fail, both via [Context.Read] and raw reads of
// the request body. Bodies declaring more than the limit are rejected without
// reading them. Over-limit failures are reported as [ErrRequestEntityTooLarge] (413).
// A negative value disables the limit, zero rejects every request body, and a
// positive value sets the limit. Pass it to [New].
//
// Default: 4 MiB
func WithMaxBodyBytes(n int) AppOption {
	return func(o *appOptions) {
		o.maxBodyBytes = n
	}
}

// WithPrefork determines whether to use a prefork listener for the server.
// If enabled, the app starts multiple child processes that listen on the same
// port by enabling the SO_REUSEPORT socket option.
// Pass it to [New].
//
// Default: false
func WithPrefork(b bool) AppOption {
	return func(o *appOptions) {
		o.preforkIsEnabled = b
	}
}

// WithPreforkChildrenCount sets the number of child processes to start when
// prefork is enabled. Pass it to [New].
//
// Default: number of logical CPUs from [runtime.NumCPU]
func WithPreforkChildrenCount(i int) AppOption {
	Assert(i > 0)
	return func(o *appOptions) {
		o.preforkChildrenCount = i
	}
}

// WithPreforkRetriesCount sets the number of times the prefork parent will
// respawn a worker child process after it stops or crashes before giving up.
// Once this limit is reached, [ErrPreforkRetriesExceeded] is returned from
// [App.Start]. A negative value disables the retry limit, while zero allows no
// retries. Pass it to [New].
//
// Default: unlimited retries
func WithPreforkRetriesCount(i int) AppOption {
	return func(o *appOptions) {
		o.preforkRetriesCount = i
	}
}

// WithTls allows the server to use TLS when listening.
// certFile specifies the path to the TLS certificate file.
// keyFile specifies the path to the TLS private key file.
// Pass it to [New].
//
// Default: no TLS
func WithTls(certFile, keyFile string) AppOption {
	Assert(certFile != "" && keyFile != "")
	return func(o *appOptions) {
		o.useTls = true
		o.certFile = certFile
		o.keyFile = keyFile
	}
}

// WithTlsConfig provides a TLS configuration for the server.
// The configuration is cloned before use by the server. A nil config
// leaves the server's default TLS configuration. Pass it to [New].
func WithTlsConfig(c *tls.Config) AppOption {
	return func(o *appOptions) {
		o.tlsConfig = c
	}
}

// WithHttp2Config configures HTTP/2 connections. A nil config leaves the
// default HTTP/2 configuration. Pass it to [New].
func WithHttp2Config(c *http.HTTP2Config) AppOption {
	return func(o *appOptions) {
		o.http2Config = c
	}
}

// WithProtocols specifies the set of protocols accepted by the server.
//
// If Protocols includes unencrypted HTTP/2, the server accepts
// unencrypted HTTP/2 connections. The server can serve both HTTP/1
// and unencrypted HTTP/2 on the same address and port.
// A nil value leaves the default protocol set. Pass it to [New].
//
// Default: HTTP/1 and HTTP/2
func WithProtocols(p *http.Protocols) AppOption {
	return func(o *appOptions) {
		o.protocols = p
	}
}

// WithClientPriority determines whether client-specified HTTP/2
// priorities, as specified in RFC 9218, are respected.
//
// This field only takes effect when using HTTP/2 and when no custom
// write scheduler is configured. If false, requests are served in
// round-robin order without prioritization.
// Pass it to [New].
//
// Default: true
func WithClientPriority(b bool) AppOption {
	return func(o *appOptions) {
		o.clientPriority = b
	}
}

// WithParallelServiceStart determines whether services are started concurrently
// instead of one after another. Pass it to [New].
//
// Default: false
func WithParallelServiceStart(b bool) AppOption {
	return func(o *appOptions) {
		o.serviceStartParallel = b
	}
}

// WithParallelServiceStop determines whether services are stopped concurrently
// instead of one after another. Pass it to [New].
//
// Default: false
func WithParallelServiceStop(b bool) AppOption {
	return func(o *appOptions) {
		o.serviceStopParallel = b
	}
}

// WithShutdownTimeout sets the maximum duration for http server shutdown.
// A negative value disables the timeout. It panics on a zero duration.
// Pass it to [New].
//
// Default: no timeout
func WithShutdownTimeout(d time.Duration) AppOption {
	Assert(d != 0)
	return func(o *appOptions) {
		o.shutdownTimeout = d
	}
}

// WithPassLocalsToContext mirrors request-scoped locals into [http.Request] context.
//
// Locals themselves live in a per-request map[string]any (no locking:
// handlers run linearly via [Context.Next] in one goroutine).
// If true, [Context.SetLocal] also calls [context.WithValue], and
// [Context.DeleteLocal] shadows the key with nil (contexts can't delete).
// Enable only for interop with stdlib/middleware reading r.Context().Value().
// Pass it to [New].
//
// Default: false
func WithPassLocalsToContext(b bool) AppOption {
	return func(o *appOptions) {
		o.passLocalsToContext = b
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
