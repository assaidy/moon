// Package response_time provides a handler that records how long the
// handler chain took and reports it in a response header. Register
// [Handler.Handle] as a middleware or a route handler:
//
//	app.Use("/*", response_time.New().Handle)
//
// Customize the header or skip timing for some requests:
//
//	app.Use("/*", response_time.New(response_time.NewOptions().WithHeader("X-Took")).Handle)
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
// response header. Use [New] to construct it, passing [NewOptions] chained
// with the With* methods to configure it, then register [Handler.Handle]
// in the chain.
type Handler struct {
	options Options
}

// Options holds the configuration of a [Handler]. All fields are private;
// build one with [NewOptions] for the defaults and chain the With* methods
// to configure it, then pass it to [New].
type Options struct {
	skip   func(ctx *moon.Context) bool
	header string
}

// New returns a handler built from the given options, or defaults when none
// are given. Chain the With* methods on [NewOptions] to configure it, then
// register [Handler.Handle] in the chain:
//
//	app.Use("/*", New().Handle)
//	app.Use("/*", New(NewOptions().WithHeader("X-Took")).Handle)
//
// Default header: "X-Response-Time" (see [Options.WithHeader]).
// Requests for which the [Options.WithSkip] predicate returns true run the
// chain without recording anything.
func New(opts ...Options) *Handler {
	options := NewOptions()
	if len(opts) > 0 {
		moon.Assert(len(opts) == 1)
		options = opts[0]
	}
	return &Handler{options: options}
}

// NewOptions returns an Options populated with the default values.
// See the With* methods for each default.
func NewOptions() Options {
	return Options{header: "X-Response-Time"}
}

// WithSkip skips timing for requests where f returns true.
// The chain still runs; only the header is omitted. It returns the same
// options for chaining.
//
// Default: nil (nothing is skipped)
func (me Options) WithSkip(f func(ctx *moon.Context) bool) Options {
	me.skip = f
	return me
}

// WithHeader sets the response header carrying the duration.
// Surrounding whitespace is trimmed. It panics on an empty name. It returns
// the same options for chaining.
//
// Default: "X-Response-Time"
func (me Options) WithHeader(s string) Options {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "header cannot be empty or whitespace")
	me.header = s
	return me
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
