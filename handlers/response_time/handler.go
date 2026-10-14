// Package response_time provides a handler that records how long the
// handler chain took and reports it in a response header. Register
// [Handler.Handle] as a middleware or a route handler:
//
//	app.UseAll(response_time.New().Handle)
//
// Customize the header or skip timing for some requests:
//
//	app.UseAll(response_time.New(response_time.Option_Header("X-Took")).Handle)
//
// As a middleware it runs before the route is resolved, so the timing
// covers the full chain and the header is set even for unmatched requests
// ([moon.ErrInvalidEndpoint], [moon.ErrMethodNotAllowed]).
package response_time

import (
	"strings"
	"time"

	"github.com/assaidy/moon"
)

// Handler measures the handler chain duration and reports it in a
// response header. Use [New] to construct it, passing [Option]s
// to configure it, then register [Handler.Handle]
// in the chain.
type Handler struct {
	options options
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	skip   func(ctx *moon.Context) bool
	header string
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register [Handler.Handle] in the chain:
//
//	app.UseAll(response_time.New().Handle)
//	app.UseAll(response_time.New(response_time.Option_Header("X-Took")).Handle)
//
// Default header: "X-Response-Time" (see [Option_Header]).
// Requests for which the [Option_Skip] predicate returns true run the
// chain without recording anything.
func New(opts ...Option) *Handler {
	options := options{header: "X-Response-Time"}
	for _, opt := range opts {
		opt(&options)
	}
	return &Handler{options: options}
}

// Option configures a [Handler]. Pass them to [New]:
//
//	app.UseAll(response_time.New(response_time.Option_Header("X-Took")).Handle)
type Option func(*options)

// Option_Skip skips timing for requests where f returns true.
// The chain still runs; only the header is omitted. Pass it to [New].
//
// Default: nil (nothing is skipped)
func Option_Skip(f func(ctx *moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// Option_Header sets the response header carrying the duration.
// Surrounding whitespace is trimmed. It panics on an empty name. Pass it to [New].
//
// Default: "X-Response-Time"
func Option_Header(s string) Option {
	return func(o *options) {
		s = strings.TrimSpace(s)
		moon.Assert(s != "", "header cannot be empty or whitespace")
		o.header = s
	}
}

// Handle measures the handler chain duration and sets it as a response
// header after [moon.Context.Next] returns. The header value is formatted
// with [time.Duration.String], so it parses with [time.ParseDuration]. The
// header is set even when the chain returns an error, and the error is
// passed through unchanged.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	start := time.Now()
	err := ctx.Next()
	ctx.SetHeader(me.options.header, time.Since(start).String())
	return err
}
