// Package skip provides a middleware that conditionally bypasses another
// handler.
package skip

import "github.com/assaidy/moon"

// New wraps handler so it only runs when predicate returns false for the
// current request. When predicate returns true, handler is bypassed and the
// chain continues via [moon.Context.Next]. It panics if handler or predicate
// is nil.
//
// The predicate runs once per request, before handler; use it to skip work
// (auth, rate limiting, ...) for requests matching some condition.
func New(handler moon.Handler, predicate func(*moon.Context) bool) moon.Handler {
	moon.Assert(handler != nil, "handler cannot be nil")
	moon.Assert(predicate != nil, "predicate cannot be nil")

	return func(ctx *moon.Context) error {
		if predicate(ctx) {
			return ctx.Next()
		}

		return handler(ctx)
	}
}
