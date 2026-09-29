// Package skip provides a handler that conditionally bypasses another
// handler. Register [Handler.Handle] as a middleware or a route handler:
//
//	h := skip.New(handler, predicate)
//	app.Use("/*", h.Handle)
package skip

import "github.com/assaidy/moon"

// Handler wraps a handler so it only runs when the predicate returns
// false. Use [New] to construct it, then register [Handler.Handle] in
// the chain.
type Handler struct {
	handler   moon.Handler
	predicate func(*moon.Context) bool
}

// New wraps handler so it only runs when predicate returns false for the
// current request. When predicate returns true, handler is bypassed and the
// chain continues via [moon.Context.Next]. It panics if handler or predicate
// is nil. Register [Handler.Handle] in the chain:
//
//	h := New(handler, predicate)
//	app.Use("/*", h.Handle)
//
// The predicate runs once per request, before handler; use it to skip work
// (auth, rate limiting, ...) for requests matching some condition.
func New(handler moon.Handler, predicate func(*moon.Context) bool) *Handler {
	moon.Assert(handler != nil, "handler cannot be nil")
	moon.Assert(predicate != nil, "predicate cannot be nil")

	return &Handler{
		handler:   handler,
		predicate: predicate,
	}
}

// Handle runs the wrapped handler when the predicate returns false, otherwise
// it bypasses the handler and continues the chain via [moon.Context.Next].
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.predicate(ctx) {
		return ctx.Next()
	}

	return me.handler(ctx)
}
