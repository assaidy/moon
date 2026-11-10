// Package request_id provides a handler that assigns every request a
// request ID, echoes it in a response header, and stores it in a context local.
// Read it with [moon.Context.GetLocal] using [DefaultLocalKey] or the key
// passed to [WithLocalKey]. Register the handler returned by [New] as a
// middleware or a route handler:
//
//	app.UseAll(request_id.New())
//
// Customize the header, generator, or local key:
//
//	app.UseAll(request_id.New(request_id.WithHeader("X-Correlation-ID")))
//
// As a middleware it runs before the route is resolved, so every matching
// request gets an ID — including requests with no route
// ([moon.ErrNotFound], [moon.ErrMethodNotAllowed]), which still
// carry the echoed header.
package request_id

import (
	"net/http"
	"strings"

	"github.com/assaidy/moon"
)

// DefaultLocalKey is the context local key used when [WithLocalKey] is not
// given. Read the ID with [moon.Context.GetLocal] using this key.
const DefaultLocalKey = "moon.handlers.request_id.local_key"

// options holds the configuration of the handler returned by [New]. All fields
// are private; pass [Option]s to [New] to configure it.
type options struct {
	skip      func(*moon.Context) bool
	header    string
	generator func() string
	localKey  string
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register the returned handler in the chain:
//
//	app.UseAll(request_id.New())
//	app.UseAll(request_id.New(request_id.WithHeader("X-Correlation-ID")))
//
// The incoming request header is reused when it is a valid ID: non-empty
// printable ASCII (0x20-0x7E, inside spaces allowed). It arrives pre-trimmed
// because the HTTP server strips edge whitespace while parsing; otherwise
// an ID is generated (see [WithGenerator]). The ID is echoed in
// the response header and stored in a context local, then the chain runs.
//
// To include the ID in request logs, add a custom
// [moon.RequestLoggingEntry] when request logging is enabled:
//
//	h := request_id.New()
//	app.AddRequestLoggingEntry(moon.RequestLoggingEntry{
//		Key: "request_id",
//		Value: func(ctx *moon.Context, err error) any {
//			id, _ := ctx.GetLocal[string](request_id.DefaultLocalKey)
//			return id
//		},
//	})
//	app.UseAll(h)
//
// Default header: "X-Request-ID" (see [WithHeader]).
// Default local key: [DefaultLocalKey] (see [WithLocalKey]).
// Requests for which the [WithSkip] predicate returns true run the
// chain untouched: no header is set and nothing is stored.
func New(opts ...Option) moon.Handler {
	options := options{
		header:    "X-Request-ID",
		generator: func() string { return moon.GenerateSecureToken() },
		localKey:  DefaultLocalKey,
	}
	for _, opt := range opts {
		opt(&options)
	}

	return func(ctx *moon.Context) error {
		if options.skip != nil && options.skip(ctx) {
			return ctx.Next()
		}

		requestId := sanitizeRequestId(ctx.GetHeader(options.header), options.generator)
		ctx.SetHeader(options.header, requestId)
		ctx.SetLocal(options.localKey, requestId)

		return ctx.Next()
	}
}

// Option configures the handler returned by [New]. Pass them to [New]:
//
//	app.UseAll(request_id.New(request_id.WithHeader("X-Correlation-ID")))
type Option func(*options)

// WithSkip skips ID assignment for requests where f returns true.
// The chain still runs; no header is set and nothing is stored in the context.
// Pass it to [New].
//
// Default: nil (nothing is skipped)
func WithSkip(f func(ctx *moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// WithHeader sets the request/response header carrying the ID.
// Surrounding whitespace is trimmed and the name is canonicalized
// ("x-correlation-id" becomes "X-Correlation-Id"). It panics on an empty name.
// Pass it to [New].
//
// Default: "X-Request-ID"
func WithHeader(s string) Option {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "header cannot be empty or whitespace")
	s = http.CanonicalHeaderKey(s)
	return func(o *options) {
		o.header = s
	}
}

// WithGenerator sets the ID generator. Its output is trimmed and tried up to
// 3 times until it produces a valid ID; afterwards [moon.GenerateSecureToken]
// is used as a fallback. It panics on a nil generator. Pass it to [New].
//
// Default: [moon.GenerateSecureToken].
func WithGenerator(f func() string) Option {
	moon.Assert(f != nil, "generator func cannot be nil")
	return func(o *options) {
		o.generator = f
	}
}

// WithLocalKey sets the context local key under which the request ID is
// stored. The key is used as-is, including surrounding whitespace. It panics
// on an empty key. Read the ID with [moon.Context.GetLocal] using the same key.
// Pass it to [New].
//
// Default: [DefaultLocalKey].
func WithLocalKey(s string) Option {
	moon.Assert(s != "", "local key cannot be empty")
	return func(o *options) {
		o.localKey = s
	}
}

// sanitizeRequestId returns requestId when valid; otherwise it trims and
// validates the generator output up to 3 times, falling back to
// [moon.GenerateSecureToken].
func sanitizeRequestId(requestId string, generator func() string) string {
	if isValidRequestId(requestId) {
		return requestId
	}

	for range 3 {
		id := strings.TrimSpace(generator())
		if isValidRequestId(id) {
			return id
		}
	}

	return moon.GenerateSecureToken()
}

// isValidRequestId reports whether requestId is usable as-is: non-empty
// printable ASCII (0x20-0x7E). Inside spaces are accepted; edge whitespace
// on incoming values is already stripped by the HTTP server, and generated
// values are trimmed by [sanitizeRequestId].
func isValidRequestId(requestId string) bool {
	if requestId == "" {
		return false
	}

	for _, b := range requestId {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}

	return true
}
