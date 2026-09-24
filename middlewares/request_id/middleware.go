// Package request_id provides a middleware that assigns every request a
// request ID, echoes it in a response header, and makes it available to
// handlers via [GetFromContext].
package request_id

import (
	"strings"
	"sync"

	"github.com/assaidy/moon"
)

const localKey = "moon.middlewares.request_id.local_key"

// New returns a middleware that ensures every request has a request ID. The
// incoming request header is reused when it is a valid ID: non-empty
// printable ASCII (0x20-0x7E, inside spaces allowed). It arrives pre-trimmed
// because the HTTP server strips edge whitespace while parsing; otherwise
// an ID is generated (see [WithGenerator]). The ID is echoed in the response
// header and stored for [GetFromContext], then the chain runs.
//
// It can also register a request logging entry once per New() so the ID
// appears in request logs (see [WithRequestLoggingEntry],
// [WithRequestLoggingEntryKey] and [WithRequestLoggingEntryValueFunc]).
// Registering the same key twice on one app panics, so multiple instances
// on the same app need distinct keys.
//
// Default header: "X-Request-ID" (see [WithHeader]).
// Requests for which the [WithSkip] predicate returns true run the chain
// untouched: no header is set and [GetFromContext] returns "".
func New(optionFuncs ...OptionFunc) moon.Handler {
	opts := options{
		header:                       "X-Request-ID",
		generator:                    func() string { return moon.GenerateSecureToken() },
		requestLoggingEntryKey:       "request_id",
		requestLoggingEntryValueFunc: GetFromContext,
	}
	for _, of := range optionFuncs {
		of(&opts)
	}

	var registerRequestLoggingEntryOnce sync.Once

	return func(ctx *moon.Context) error {
		registerRequestLoggingEntryOnce.Do(func() {
			if opts.enableRequestLoggingEntry {
				ctx.RegisterRequestLoggingEntry(moon.RequestLoggingEntry{
					Key:       opts.requestLoggingEntryKey,
					ValueFunc: opts.requestLoggingEntryValueFunc,
				})
			}
		})

		if opts.skip != nil && opts.skip(ctx) {
			return ctx.Next()
		}

		requestId := sanitizeRequestId(ctx.GetHeader(opts.header), opts.generator)
		ctx.SetHeader(opts.header, requestId)
		ctx.SetLocal(localKey, requestId)

		return ctx.Next()
	}
}

// GetFromContext returns the request ID assigned by the middleware,
// or "" when the middleware was skipped or never ran.
func GetFromContext(ctx *moon.Context) string {
	return moon.IgnoreSecond(ctx.GetLocal[string](localKey))
}

type options struct {
	skip                         func(*moon.Context) bool
	header                       string
	generator                    func() string
	enableRequestLoggingEntry    bool
	requestLoggingEntryKey       string
	requestLoggingEntryValueFunc moon.RequestLoggingEntryValueFunc
}

// OptionFunc configures the middleware. Pass option funcs to [New].
type OptionFunc func(opts *options)

// WithSkip skips ID assignment for requests where f returns true.
// The chain still runs; no header is set and [GetFromContext] returns "".
//
// Default: nil (nothing is skipped)
func WithSkip(f func(ctx *moon.Context) bool) OptionFunc {
	return func(opts *options) {
		opts.skip = f
	}
}

// WithHeader sets the request/response header carrying the ID.
// Surrounding whitespace is trimmed. It panics on an empty name.
//
// Default: "X-Request-ID"
func WithHeader(s string) OptionFunc {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "header cannot be empty or whitespace")

	return func(opts *options) {
		opts.header = s
	}
}

// WithGenerator sets the ID generator. Its output is trimmed and tried up to
// 3 times until it produces a valid ID; afterwards [moon.GenerateSecureToken]
// is used as a fallback. It panics on a nil generator.
//
// Default: [moon.GenerateSecureToken].
func WithGenerator(f func() string) OptionFunc {
	moon.Assert(f != nil, "generator func cannot be nil")

	return func(opts *options) {
		opts.generator = f
	}
}

// WithRequestLoggingEntry enables the request logging entry carrying the ID.
// Enable it only when the entry is not registered elsewhere on the app to
// avoid a duplicate-key panic.
//
// Default: false.
func WithRequestLoggingEntry(b bool) OptionFunc {
	return func(opts *options) {
		opts.enableRequestLoggingEntry = b
	}
}

// WithRequestLoggingEntryKey sets the request logging entry key.
// Surrounding whitespace is trimmed. It panics on an empty key, and
// registering the same key twice on one app panics: multiple instances on
// the same app need distinct keys.
//
// Default: "request_id".
func WithRequestLoggingEntryKey(s string) OptionFunc {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "key cannot be empty or whitespace")

	return func(opts *options) {
		opts.requestLoggingEntryKey = s
	}
}

// WithRequestLoggingEntryValueFunc sets the func rendering the request
// logging entry value. It panics on a nil func.
//
// Default: [GetFromContext].
func WithRequestLoggingEntryValueFunc(f moon.RequestLoggingEntryValueFunc) OptionFunc {
	moon.Assert(f != nil, "value func cannot be nil")

	return func(opts *options) {
		opts.requestLoggingEntryValueFunc = f
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
