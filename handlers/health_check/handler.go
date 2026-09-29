// Package health_check provides a health probe endpoint handler, typically
// used for liveness, readiness and startup probes.
//
// This is a terminal endpoint: it doesn't continue the chain (it never calls
// [moon.Context.Next]). It must be registered as a route via [moon.App.Map]
// rather than as a middleware via [moon.App.Use]. If you register it for
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
//	app.MapGet(health_check.ReadinessEndpoint, health_check.New().WithProbe(
//		func(ctx *moon.Context) bool {
//			err := db.Ping()
//			return err == nil
//		},
//	).Handle)
package health_check

import (
	"net/http"

	"github.com/assaidy/moon"
)

// Handler runs a probe and renders its result. Use [New] to construct it
// with defaults, chain [Handler.WithProbe] and [Handler.WithResponse]
// to configure it, then register [Handler.Handle] as a terminal endpoint.
type Handler struct {
	probe    func(ctx *moon.Context) bool
	response func(ctx *moon.Context, ok bool) error
}

// New returns a handler that runs a probe and renders its result. Chain
// [Handler.WithProbe] and [Handler.WithResponse] to configure it, then
// register [Handler.Handle] as a terminal endpoint:
//
//	app.Map(http.MethodGet, "/healthz", New().Handle)
//
// It runs the probe (see [Handler.WithProbe]) and passes the outcome to
// the response func (see [Handler.WithResponse]).
//
// Default behavior: the probe reports ok, and the response writes
// 200 OK when it succeeds or 503 Service Unavailable when it fails.
func New() *Handler {
	return &Handler{
		probe:    defaultProbe,
		response: defaultResponse,
	}
}

// WithProbe sets the probe deciding whether the endpoint reports healthy:
// true means ok, false means unhealthy. It panics if f is nil. It returns
// the same handler for chaining.
//
// Default: always reports ok (true).
func (me *Handler) WithProbe(f func(ctx *moon.Context) bool) *Handler {
	moon.Assert(f != nil, "probe func cannot be nil")
	me.probe = f
	return me
}

// defaultProbe always reports ok (true).
func defaultProbe(_ *moon.Context) bool {
	return true
}

// WithResponse sets the func rendering the probe result; ok is what the
// probe returned. Returning an error hands it to the error handler.
// It panics if f is nil. It returns the same handler for chaining.
//
// Default: 200 OK when ok, 503 Service Unavailable otherwise. When writing
// your own response, never write body bytes if the request method is HEAD.
func (me *Handler) WithResponse(f func(ctx *moon.Context, ok bool) error) *Handler {
	moon.Assert(f != nil, "response func cannot be nil")
	me.response = f
	return me
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
	return me.response(ctx, me.probe(ctx))
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
