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
// and stop with [App.Shutdown]. Use [App.Test] to exercise handlers without
// listening.
type App struct {
	httpServer       *http.Server
	routesPerMethod  map[string][]RouteEntry // method -> routes
	middlewares      []MiddlewareEntry
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
//	app := New(AppOption_ListenAddress(":3000"))
func New(opts ...AppOption) *App {
	options := appOptions{
		logger:               slog.Default(),
		errorHandler:         DefaultErrorHandler,
		clientPriority:       true,
		preforkChildrenCount: runtime.NumCPU(),
		preforkRetriesCount:  -1,
		readLimit:            4 << 20, // 4MB
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
	rle                         []RequestLoggingEntry
}

// AppOption configures an [App]. Pass them to [New]:
//
//	app := New(AppOption_ListenAddress(":3000"), AppOption_RequestLogging(true))
type AppOption func(*appOptions)

// AppOption_ListenAddress sets the TCP address the server listens on,
// e.g. ":8080" or "127.0.0.1:3000". Pass it to [New].
//
// Default: ":http" (port 80), or ":https" (port 443) when TLS is
// enabled via [AppOption_Tls].
func AppOption_ListenAddress(address string) AppOption {
	return func(o *appOptions) {
		o.listenAddress = address
	}
}

// AppOption_Logger sets the logger used for all internal logging within the app
// and its contexts. It is also used for request logging when
// [AppOption_RequestLogging] is enabled. It panics on a nil logger. Pass it to [New].
//
// Default: [slog.Default]
func AppOption_Logger(l *slog.Logger) AppOption {
	return func(o *appOptions) {
		Assert(l != nil)
		o.logger = l
	}
}

// AppOption_ErrorHandler sets the handler used to handle errors returned by the
// handler chain, including [ErrInvalidEndpoint] for unknown paths and
// [ErrMethodNotAllowed] for unregistered methods, so a custom handler can
// inspect or override them. It panics on a nil handler. Pass it to [New].
//
// Default: [DefaultErrorHandler]
func AppOption_ErrorHandler(eh ErrorHandler) AppOption {
	return func(o *appOptions) {
		Assert(eh != nil)
		o.errorHandler = eh
	}
}

// AppOption_RequestLogging determines whether every request routed through the
// app is logged as a "request handled" record on the logger, with one
// attribute per entry of the request logging list (see
// [AppOption_RequestLoggingEntries] and [App.AddRequestLoggingEntry]).
// Unmatched paths and methods handled as [ErrInvalidEndpoint] or
// [ErrMethodNotAllowed] are logged too.
// The logged status defaults to 200 when no response was written.
// Pass it to [New].
//
// Default: false
func AppOption_RequestLogging(b bool) AppOption {
	return func(o *appOptions) {
		o.enableRequestLogging = b
	}
}

// AppOption_RequestLoggingEntries replaces the request logging list of the app,
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
func AppOption_RequestLoggingEntries(entries []RequestLoggingEntry) AppOption {
	return func(o *appOptions) {
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
		o.rle = validated
	}
}

// AppOption_GeneralOptionsHandler determines whether the server should pass
// general OPTIONS requests to the Handler. If true, the server responds
// with a 200 OK status and a Content-Length of 0.
// Pass it to [New].
//
// Default: false
func AppOption_GeneralOptionsHandler(b bool) AppOption {
	return func(o *appOptions) {
		o.enableGeneralOptionsHandler = b
	}
}

// AppOption_ReadTimeout sets the maximum duration for reading the entire request,
// including the body. Pass it to [New].
//
// Default: no timeout
func AppOption_ReadTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.readTimeout = d
	}
}

// AppOption_ReadHeaderTimeout sets the maximum duration for reading request headers.
// The connection's read deadline is reset after the headers are read,
// allowing the Handler to decide what is considered too slow for the body.
// Pass it to [New].
//
// Default: read timout
func AppOption_ReadHeaderTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.readHeaderTimeout = d
	}
}

// AppOption_WriteTimeout sets the maximum duration for writing the response. It is
// reset whenever a new request's header is read. Like ReadTimeout, it
// does not allow Handlers to make per-request decisions.
// Pass it to [New].
//
// Default: no timeout
func AppOption_WriteTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.writeTimeout = d
	}
}

// AppOption_IdleTimeout sets the maximum amount of time to wait for the next request
// when keep-alives are enabled. Pass it to [New].
//
// Default: read timout
func AppOption_IdleTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.idleTimeout = d
	}
}

// AppOption_MaxHeaderBytes controls the maximum number of bytes the server will read
// while parsing request header keys and values, including the request line.
// It does not limit the size of the request body.
// Pass it to [New].
//
// Default: [http.DefaultMaxHeaderBytes] (1MB)
func AppOption_MaxHeaderBytes(i int) AppOption {
	return func(o *appOptions) {
		Assert(i > 0)
		o.maxHeaderBytes = i
	}
}

// AppOption_MaxHeaderValueCount controls the maximum number of header values that
// the server will parse from a request. Comma-separated values in a
// single header line are counted once, while values sent as multiple
// header lines are counted separately.
// Pass it to [New].
//
// Default: [http.DefaultMaxHeaderValueCount] (500)
func AppOption_MaxHeaderValueCount(i int) AppOption {
	return func(o *appOptions) {
		Assert(i > 0)
		o.maxHeaderValueCount = i
	}
}

// AppOption_ReadLimit sets the maximum number of bytes accepted for a request
// body. Reads past the limit fail, both via [Context.Read] and raw reads of
// the request body. Bodies declaring more than the limit are rejected without
// reading them. Over-limit failures are reported as [ErrRequestEntityTooLarge] (413).
// Pass it to [New].
//
// Default: 4MB
func AppOption_ReadLimit(n int) AppOption {
	return func(o *appOptions) {
		Assert(n > 0)
		o.readLimit = n
	}
}

// AppOption_Prefork determines whether to use a prefork listener for the server.
// If enabled, the app starts multiple child processes that listen on the same
// port by enabling the SO_REUSEPORT socket option.
// Pass it to [New].
//
// Default: false
func AppOption_Prefork(b bool) AppOption {
	return func(o *appOptions) {
		o.preforkIsEnabled = b
	}
}

// AppOption_PreforkChildrenCount sets the number of child processes to start when
// prefork is enabled. Pass it to [New].
//
// Default: number of logical CPUs from [runtime.NumCPU]
func AppOption_PreforkChildrenCount(i int) AppOption {
	return func(o *appOptions) {
		Assert(i > 0)
		o.preforkChildrenCount = i
	}
}

// AppOption_PreforkRetriesCount sets the number of times the prefork parent will
// respawn a worker child process after it stops or crashes before giving up.
// Once this limit is reached, [ErrPreforkRetriesExceeded] is returned from
// [App.Start]. Pass it to [New].
//
// Default: infinite retries
func AppOption_PreforkRetriesCount(i int) AppOption {
	return func(o *appOptions) {
		Assert(i >= 0)
		o.preforkRetriesCount = i
	}
}

// AppOption_Tls allows the server to use TLS when listening.
// certFile specifies the path to the TLS certificate file.
// keyFile specifies the path to the TLS private key file.
// Pass it to [New].
//
// Default: no TLS
func AppOption_Tls(certFile, keyFile string) AppOption {
	return func(o *appOptions) {
		Assert(certFile != "" && keyFile != "")
		o.useTls = true
		o.certFile = certFile
		o.keyFile = keyFile
	}
}

// AppOption_TlsConfig provides a TLS configuration for the server.
// The configuration is cloned before use by the server.
// Pass it to [New].
func AppOption_TlsConfig(c *tls.Config) AppOption {
	return func(o *appOptions) {
		Assert(c != nil)
		o.tlsConfig = c
	}
}

// AppOption_Http2Config configures HTTP/2 connections.
// Pass it to [New].
func AppOption_Http2Config(c *http.HTTP2Config) AppOption {
	return func(o *appOptions) {
		Assert(c != nil)
		o.http2Config = c
	}
}

// AppOption_Protocols specifies the set of protocols accepted by the server.
//
// If Protocols includes unencrypted HTTP/2, the server accepts
// unencrypted HTTP/2 connections. The server can serve both HTTP/1
// and unencrypted HTTP/2 on the same address and port.
// Pass it to [New].
//
// Default: HTTP/1 and HTTP/2
func AppOption_Protocols(p *http.Protocols) AppOption {
	return func(o *appOptions) {
		Assert(p != nil)
		o.protocols = p
	}
}

// AppOption_ClientPriority determines whether client-specified HTTP/2
// priorities, as specified in RFC 9218, are respected.
//
// This field only takes effect when using HTTP/2 and when no custom
// write scheduler is configured. If false, requests are served in
// round-robin order without prioritization.
// Pass it to [New].
//
// Default: true
func AppOption_ClientPriority(b bool) AppOption {
	return func(o *appOptions) {
		o.clientPriority = b
	}
}

// AppOption_ServiceStartTimeout sets the maximum duration for starting each service.
// The timeout is enforced per service through the context passed to [Service.Start].
// A service that does not finish in time fails with a context error and is skipped.
// Pass it to [New].
//
// Default: no timeout
func AppOption_ServiceStartTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.serviceStartTimeout = d
	}
}

// AppOption_ServiceStopTimeout sets the maximum duration for stopping each service.
// The timeout is enforced per service through the context passed to [Service.Stop].
// A service that does not finish in time fails with a context error, which is logged.
// Pass it to [New].
//
// Default: no timeout
func AppOption_ServiceStopTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.serviceStopTimeout = d
	}
}

// AppOption_ParallelServiceStart determines whether services are started concurrently
// instead of one after another. Pass it to [New].
//
// Default: false
func AppOption_ParallelServiceStart(b bool) AppOption {
	return func(o *appOptions) {
		o.serviceStartParallel = b
	}
}

// AppOption_ParallelServiceStop determines whether services are stopped concurrently
// instead of one after another. Pass it to [New].
//
// Default: false
func AppOption_ParallelServiceStop(b bool) AppOption {
	return func(o *appOptions) {
		o.serviceStopParallel = b
	}
}

// AppOption_ShutdownTimeout sets the maximum duration for http server shutdown.
// Pass it to [New].
//
// Default: no timeout
func AppOption_ShutdownTimeout(d time.Duration) AppOption {
	return func(o *appOptions) {
		Assert(d > 0)
		o.shutdownTimeout = d
	}
}

// AppOption_PassLocalsToContext mirrors request-scoped locals into [http.Request] context.
//
// Locals themselves live in a per-request map[string]any (no locking:
// handlers run linearly via [Context.Next] in one goroutine).
// If true, [Context.SetLocal] also calls [context.WithValue], and
// [Context.DeleteLocal] shadows the key with nil (contexts can't delete).
// Enable only for interop with stdlib/middleware reading r.Context().Value().
// Pass it to [New].
//
// Default: false
func AppOption_PassLocalsToContext(b bool) AppOption {
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
