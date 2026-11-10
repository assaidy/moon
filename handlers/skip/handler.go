// Package skip provides a handler that conditionally bypasses another
// handler. Register the handler returned by [New] as a middleware or a route
// handler:
//
//	h := skip.New(handler, predicate)
//	app.UseAll(h)
//
// As a middleware it runs before the route is resolved, so the predicate
// runs even for requests with no route: a bypassed request simply continues
// to the route, [moon.ErrMethodNotAllowed] or [moon.ErrNotFound].
package skip

import "github.com/assaidy/moon"

// New wraps handler so it only runs when predicate returns false for the
// current request. When predicate returns true, handler is bypassed and the
// chain continues via [moon.Context.Next]. It panics if handler or predicate
// is nil. Register the returned handler in the chain:
//
//	h := skip.New(handler, predicate)
//	app.UseAll(h)
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
