// Package response_time provides a middleware that records how long the
// handler chain took and reports it in a response header.
//
// Register [Middleware.Handle] as a middleware:
//
//	app.Use("/", response_time.New().Handle)
//
// Customize the header or skip timing for some requests:
//
//	app.Use("/", response_time.New().WithHeader("X-Took").Handle)
package response_time

import (
	"strings"
	"time"

	"github.com/assaidy/moon"
)

// Middleware measures the handler chain duration and reports it in a
// response header. Use [New] to construct it with defaults, chain
// [Middleware.WithSkip] and [Middleware.WithHeader] to configure it, then
// register [Middleware.Handle] in the chain.
type Middleware struct {
	skip   func(ctx *moon.Context) bool
	header string
}

// New returns a middleware with default options. Chain [Middleware.WithSkip]
// and [Middleware.WithHeader] to configure it, then register
// [Middleware.Handle] in the chain:
//
//	app.Use("/", New().Handle)
//	app.Use("/", New().WithHeader("X-Took").Handle)
//
// Default header: "X-Response-Time" (see [Middleware.WithHeader]).
// Requests for which the [Middleware.WithSkip] predicate returns true run the
// chain without recording anything.
func New() *Middleware {
	return &Middleware{header: "X-Response-Time"}
}

// WithSkip skips timing for requests where f returns true.
// The chain still runs; only the header is omitted. It returns the same
// middleware for chaining.
//
// Default: nil (nothing is skipped)
func (me *Middleware) WithSkip(f func(ctx *moon.Context) bool) *Middleware {
	me.skip = f
	return me
}

// WithHeader sets the response header carrying the duration.
// Surrounding whitespace is trimmed. It panics on an empty name. It returns
// the same middleware for chaining.
//
// Default: "X-Response-Time"
func (me *Middleware) WithHeader(s string) *Middleware {
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
func (me *Middleware) Handle(ctx *moon.Context) error {
	if me.skip != nil && me.skip(ctx) {
		return ctx.Next()
	}

	start := time.Now()
	err := ctx.Next()
	ctx.SetHeader(me.header, time.Since(start).String())
	return err
}
