// Package health_check provides a health probe endpoint handler, typically
// used for liveness, readiness and startup probes.
//
// This is a terminal endpoint: it doesn't continue the chain (it never calls
// [moon.Context.Next]). It must be registered as a route via [moon.App.Map]
// rather than as a middleware via [moon.App.Use]: as a middleware it would
// answer every matching request itself, including requests with no route,
// and the rest of the chain would never run. If you register it for
// HEAD, never write bytes to the body (HEAD responses must not carry a body).
//
// The most common usage is registering the built-in endpoints:
//
//	app.MapGet(health_check.LivenessEndpoint, health_check.New().Handle)
//	app.MapGet(health_check.ReadinessEndpoint, health_check.New().Handle)
//	app.MapGet(health_check.StartupEndpoint, health_check.New().Handle)
//
// with a probe config deciding when the endpoint reports unhealthy:
//
//	app.MapGet(health_check.ReadinessEndpoint, health_check.New(
//		health_check.WithProbe(
//			func(ctx *moon.Context) bool {
//				err := db.Ping()
//				return err == nil
//			},
//		),
//	).Handle)
package health_check

import (
	"net/http"

	"github.com/assaidy/moon"
)

// Handler runs a probe and renders its result. Use [New] to construct it,
// passing [WithProbe] and [WithResponse] to configure it,
// then register [Handler.Handle] as a terminal endpoint.
type Handler struct {
	options options
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	probe    func(ctx *moon.Context) bool
	response func(ctx *moon.Context, ok bool) error
}

// New returns a handler built from sensible defaults, customized by the given
// options. Pass [WithProbe] and [WithResponse] to
// configure it, then register [Handler.Handle] as a terminal endpoint:
//
//	app.Map(http.MethodGet, "/healthz", health_check.New().Handle)
//
// It runs the probe (see [WithProbe]) and passes the outcome to
// the response func (see [WithResponse]).
//
// Default behavior: the probe reports ok, and the response writes
// 200 OK when it succeeds or 503 Service Unavailable when it fails.
func New(opts ...Option) *Handler {
	options := options{
		probe:    defaultProbe,
		response: defaultResponse,
	}
	for _, opt := range opts {
		opt(&options)
	}
	return &Handler{options: options}
}

// Option configures a [Handler]. Pass them to [New].
type Option func(*options)

// WithProbe sets the probe deciding whether the endpoint reports healthy:
// true means ok, false means unhealthy. It panics if f is nil. Pass it to [New].
//
// Default: always reports ok (true).
func WithProbe(f func(ctx *moon.Context) bool) Option {
	return func(o *options) {
		moon.Assert(f != nil, "probe func cannot be nil")
		o.probe = f
	}
}

// defaultProbe always reports ok (true).
func defaultProbe(_ *moon.Context) bool {
	return true
}

// WithResponse sets the func rendering the probe result; ok is what the
// probe returned. Returning an error hands it to the error handler.
// It panics if f is nil. Pass it to [New].
//
// Default: 200 OK when ok, 503 Service Unavailable otherwise. When writing
// your own response, never write body bytes if the request method is HEAD.
func WithResponse(f func(ctx *moon.Context, ok bool) error) Option {
	return func(o *options) {
		moon.Assert(f != nil, "response func cannot be nil")
		o.response = f
	}
}

// defaultResponse writes 200 OK when ok, 503 Service Unavailable otherwise.
func defaultResponse(ctx *moon.Context, ok bool) error {
	if ok {
		ctx.SetStatusCode(http.StatusOK)
	} else {
		ctx.SetStatusCode(http.StatusServiceUnavailable)
	}
	return nil
}

// Handle runs the probe and renders its result with the response func.
func (me *Handler) Handle(ctx *moon.Context) error {
	return me.options.response(ctx, me.options.probe(ctx))
}

// Built-in endpoint paths for the common probe registrations.
const (
	// LivenessEndpoint is the liveness probe path: "/livez".
	// Checks if the server is running.
	LivenessEndpoint = "/livez"
	// ReadinessEndpoint is the readiness probe path: "/readyz".
	// Checks if the application is ready to handle requests.
	ReadinessEndpoint = "/readyz"
	// StartupEndpoint is the startup probe path: "/startupz".
	// Checks if the application has completed its startup sequence.
	StartupEndpoint = "/startupz"
)
