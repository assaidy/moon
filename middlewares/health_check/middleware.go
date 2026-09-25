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
//	app.MapGet(health_check.LivenessEndpoint, health_check.New())
//	app.MapGet(health_check.ReadinessEndpoint, health_check.New())
//	app.MapGet(health_check.StartupEndpoint, health_check.New())
//
// with a probe config deciding when the endpoint reports unhealthy:
//
//	app.MapGet(health_check.ReadinessEndpoint, health_check.New(
//		health_check.WithProbe(func(ctx *moon.Context) bool {
//			err := db.Ping()
//			return err == nil
//		}),
//	))
package health_check

import (
	"net/http"

	"github.com/assaidy/moon"
)

// New returns a handler that runs a probe and renders its result. It runs
// the probe (see [WithProbe]) and passes the outcome to the response func
// (see [WithResponse]).
//
// Default behavior: the probe reports ok, and the response writes
// 200 OK when it succeeds or 503 Service Unavailable when it fails.
func New(optionFuncs ...OptionFunc) moon.Handler {
	opts := options{
		probe:    defaultProbe,
		response: defaultResponse,
	}
	for _, of := range optionFuncs {
		of(&opts)
	}

	return func(ctx *moon.Context) error {
		return opts.response(ctx, opts.probe(ctx))
	}
}

type options struct {
	probe    func(ctx *moon.Context) bool
	response func(ctx *moon.Context, ok bool) error
}

// OptionFunc configures the middleware. Pass option funcs to [New].
type OptionFunc func(opts *options)

// WithProbe sets the probe deciding whether the endpoint reports healthy:
// true means ok, false means unhealthy. It panics if f is nil.
//
// Default: always reports ok (true).
func WithProbe(f func(ctx *moon.Context) bool) OptionFunc {
	moon.Assert(f != nil, "probe func cannot be nil")

	return func(opts *options) {
		opts.probe = f
	}
}

// defaultProbe always reports ok (true).
func defaultProbe(_ *moon.Context) bool {
	return true
}

// WithResponse sets the func rendering the probe result; ok is what the
// probe returned. Returning an error hands it to the error handler.
// It panics if f is nil.
//
// Default: 200 OK when ok, 503 Service Unavailable otherwise. When writing
// your own response, never write body bytes if the request method is HEAD.
func WithResponse(f func(ctx *moon.Context, ok bool) error) OptionFunc {
	moon.Assert(f != nil, "response func cannot be nil")

	return func(opts *options) {
		opts.response = f
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
