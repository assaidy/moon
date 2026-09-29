// Package response_time provides a handler that records how long the
// handler chain took and reports it in a response header. Register
// [Handler.Handle] as a middleware or a route handler:
//
//	app.Use("/*", response_time.New().Handle)
//
// Customize the header or skip timing for some requests:
//
//	app.Use("/*", response_time.New().WithHeader("X-Took").Handle)
package response_time

import (
	"strings"
	"time"

	"github.com/assaidy/moon"
)

// Handler measures the handler chain duration and reports it in a
// response header. Use [New] to construct it with defaults, chain
// [Handler.WithSkip] and [Handler.WithHeader] to configure it, then
// register [Handler.Handle] in the chain.
type Handler struct {
	skip   func(ctx *moon.Context) bool
	header string
}

// New returns a handler with default options. Chain [Handler.WithSkip]
// and [Handler.WithHeader] to configure it, then register
// [Handler.Handle] in the chain:
//
//	app.Use("/*", New().Handle)
//	app.Use("/*", New().WithHeader("X-Took").Handle)
//
// Default header: "X-Response-Time" (see [Handler.WithHeader]).
// Requests for which the [Handler.WithSkip] predicate returns true run the
// chain without recording anything.
func New() *Handler {
	return &Handler{header: "X-Response-Time"}
}

// WithSkip skips timing for requests where f returns true.
// The chain still runs; only the header is omitted. It returns the same
// handler for chaining.
//
// Default: nil (nothing is skipped)
func (me *Handler) WithSkip(f func(ctx *moon.Context) bool) *Handler {
	me.skip = f
	return me
}

// WithHeader sets the response header carrying the duration.
// Surrounding whitespace is trimmed. It panics on an empty name. It returns
// the same handler for chaining.
//
// Default: "X-Response-Time"
func (me *Handler) WithHeader(s string) *Handler {
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
	if me.skip != nil && me.skip(ctx) {
		return ctx.Next()
	}

	start := time.Now()
	err := ctx.Next()
	ctx.SetHeader(me.header, time.Since(start).String())
	return err
}
