// Package health_check provides a health probe endpoint handler, typically
// used for liveness, readiness and startup probes.
//
// This is a terminal endpoint: it doesn't continue the chain (it never calls
// [moon.Context.Next]). Register it with an explicit endpoint method rather
// than as a middleware prefix via [moon.App.Use]. If you register it for
// HEAD, never write bytes to the body (HEAD responses must not carry a body).
//
// The most common usage is registering the built-in endpoints:
//
//	app.Map(http.MethodGet, health_check.LivenessEndpoint, health_check.New().Handle)
//	app.Map(http.MethodGet, health_check.ReadinessEndpoint, health_check.New().Handle)
//	app.Map(http.MethodGet, health_check.StartupEndpoint, health_check.New().Handle)
//
// with a probe config deciding when the endpoint reports unhealthy:
//
//	app.Map(http.MethodGet, health_check.ReadinessEndpoint, health_check.New().WithProbe(
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

// Middleware runs a probe and renders its result. Use [New] to construct it
// with defaults, chain [Middleware.WithProbe] and [Middleware.WithResponse]
// to configure it, then register [Middleware.Handle] as a terminal endpoint.
type Middleware struct {
	probe    func(ctx *moon.Context) bool
	response func(ctx *moon.Context, ok bool) error
}

// New returns a middleware that runs a probe and renders its result. Chain
// [Middleware.WithProbe] and [Middleware.WithResponse] to configure it, then
// register [Middleware.Handle] as a terminal endpoint:
//
//	app.Map(http.MethodGet, "/healthz", New().Handle)
//
// It runs the probe (see [Middleware.WithProbe]) and passes the outcome to
// the response func (see [Middleware.WithResponse]).
//
// Default behavior: the probe reports ok, and the response writes
// 200 OK when it succeeds or 503 Service Unavailable when it fails.
func New() *Middleware {
	return &Middleware{
		probe:    defaultProbe,
		response: defaultResponse,
	}
}

// WithProbe sets the probe deciding whether the endpoint reports healthy:
// true means ok, false means unhealthy. It panics if f is nil. It returns
// the same middleware for chaining.
//
// Default: always reports ok (true).
func (me *Middleware) WithProbe(f func(ctx *moon.Context) bool) *Middleware {
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
// It panics if f is nil. It returns the same middleware for chaining.
//
// Default: 200 OK when ok, 503 Service Unavailable otherwise. When writing
// your own response, never write body bytes if the request method is HEAD.
func (me *Middleware) WithResponse(f func(ctx *moon.Context, ok bool) error) *Middleware {
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
func (me *Middleware) Handle(ctx *moon.Context) error {
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
