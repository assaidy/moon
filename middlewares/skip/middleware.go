// Package skip provides a middleware that conditionally bypasses another
// handler.
//
// Register [Middleware.Handle] as a middleware:
//
//	mw := skip.New(handler, predicate)
//	app.Use("/", mw.Handle)
package skip

import "github.com/assaidy/moon"

// Middleware wraps a handler so it only runs when the predicate returns
// false. Use [New] to construct it, then register [Middleware.Handle] in
// the chain.
type Middleware struct {
	handler   moon.Handler
	predicate func(*moon.Context) bool
}

// New wraps handler so it only runs when predicate returns false for the
// current request. When predicate returns true, handler is bypassed and the
// chain continues via [moon.Context.Next]. It panics if handler or predicate
// is nil. Register [Middleware.Handle] in the chain:
//
//	mw := New(handler, predicate)
//	app.Use("/", mw.Handle)
//
// The predicate runs once per request, before handler; use it to skip work
// (auth, rate limiting, ...) for requests matching some condition.
func New(handler moon.Handler, predicate func(*moon.Context) bool) *Middleware {
	moon.Assert(handler != nil, "handler cannot be nil")
	moon.Assert(predicate != nil, "predicate cannot be nil")

	return &Middleware{
		handler:   handler,
		predicate: predicate,
	}
}

// Handle runs the wrapped handler when the predicate returns false, otherwise
// it bypasses the handler and continues the chain via [moon.Context.Next].
func (me *Middleware) Handle(ctx *moon.Context) error {
	if me.predicate(ctx) {
		return ctx.Next()
	}

	return me.handler(ctx)
}
