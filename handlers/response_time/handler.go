// Package response_time provides a handler that records how long the
// handler chain took and reports it in a response header. Register the
// handler returned by [New] as a middleware or a route handler:
//
//	app.UseAll(response_time.New())
//
// Customize the header or skip timing for some requests:
//
//	app.UseAll(response_time.New(response_time.WithHeader("X-Took")))
//
// As a middleware it runs before the route is resolved, so the timing
// covers the full chain and the header is set even for unmatched requests
// ([moon.ErrNotFound], [moon.ErrMethodNotAllowed]).
package response_time

import (
	"net/http"
	"strings"
	"time"

	"github.com/assaidy/moon"
)

// options holds the configuration of the handler returned by [New]. All fields
// are private; pass [Option]s to [New] to configure it.
type options struct {
	skip   func(ctx *moon.Context) bool
	header string
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register the returned handler in the chain:
//
//	app.UseAll(response_time.New())
//	app.UseAll(response_time.New(response_time.WithHeader("X-Took")))
//
// Default header: "X-Response-Time" (see [WithHeader]).
// Requests for which the [WithSkip] predicate returns true run the
// chain without recording anything. Otherwise the handler measures the chain,
// sets the header even when the chain returns an error, and returns that error.
func New(opts ...Option) moon.Handler {
	options := options{header: "X-Response-Time"}
	for _, opt := range opts {
		opt(&options)
	}

	return func(ctx *moon.Context) error {
		if options.skip != nil && options.skip(ctx) {
			return ctx.Next()
		}

		start := time.Now()
		err := ctx.Next()
		ctx.SetHeader(options.header, time.Since(start).String())
		return err
	}
}

// Option configures the handler returned by [New]. Pass them to [New]:
//
//	app.UseAll(response_time.New(response_time.WithHeader("X-Took")))
type Option func(*options)

// WithSkip skips timing for requests where f returns true.
// The chain still runs; only the header is omitted. Pass it to [New].
//
// Default: nil (nothing is skipped)
func WithSkip(f func(ctx *moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// WithHeader sets the response header carrying the duration.
// Surrounding whitespace is trimmed and the name is canonicalized
// ("x-took" becomes "X-Took"). It panics on an empty name. Pass it to [New].
//
// Default: "X-Response-Time"
func WithHeader(s string) Option {
	return func(o *options) {
		s = strings.TrimSpace(s)
		moon.Assert(s != "", "header cannot be empty or whitespace")
		o.header = http.CanonicalHeaderKey(s)
	}
}
