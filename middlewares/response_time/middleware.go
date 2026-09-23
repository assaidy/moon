// Package response_time provides a middleware that records how long the
// handler chain took and reports it in a response header.
package response_time

import (
	"strings"
	"time"

	"github.com/assaidy/moon"
)

// New returns a middleware that measures the handler chain duration and sets
// it as a response header after [moon.Context.Next] returns.
//
// Default header: "X-Response-Time" (see [WithHeader]).
// Requests for which the [WithSkip] predicate returns true run the chain
// without recording anything.
func New(optionFuncs ...OptionFunc) moon.Handler {
	opts := options{header: "X-Response-Time"}
	for _, of := range optionFuncs {
		of(&opts)
	}

	return func(ctx *moon.Context) error {
		if opts.skip != nil && opts.skip(ctx) {
			return ctx.Next()
		}

		start := time.Now()
		err := ctx.Next()
		ctx.SetHeader(opts.header, time.Since(start).String())
		return err
	}
}

type options struct {
	skip   func(ctx *moon.Context) bool
	header string
}

// OptionFunc configures the middleware. Pass option funcs to [New].
type OptionFunc func(opts *options)

// WithSkip skips timing for requests where f returns true.
// The chain still runs; only the header is omitted.
//
// Default: nil (nothing is skipped)
func WithSkip(f func(ctx *moon.Context) bool) OptionFunc {
	return func(opts *options) {
		opts.skip = f
	}
}

// WithHeader sets the response header carrying the duration.
// Surrounding whitespace is trimmed. It panics on an empty name.
//
// Default: "X-Response-Time"
func WithHeader(s string) OptionFunc {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "header cannot be empty or whitespace")

	return func(opts *options) {
		opts.header = s
	}
}
