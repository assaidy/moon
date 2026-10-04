// Package request_id provides a handler that assigns every request a
// request ID, echoes it in a response header, and makes it available to
// handlers via [GetFromContext]. Register [Handler.Handle] as a middleware
// or a route handler:
//
//	app.Use("/*", request_id.New().Handle)
//
// Customize the header or generator:
//
//	app.Use("/*", request_id.New(request_id.NewOptions().WithHeader("X-Correlation-ID")).Handle)
//
// As a middleware it runs before the route is resolved, so every matching
// request gets an ID — including requests with no route
// ([moon.ErrInvalidEndpoint], [moon.ErrMethodNotAllowed]), which still
// carry the echoed header.
package request_id

import (
	"strings"

	"github.com/assaidy/moon"
)

const localKey = "moon.handlers.request_id.local_key"

// Handler ensures every request has a request ID. Use [New] to construct
// it, passing [NewOptions] chained with the With* methods to configure it,
// then register [Handler.Handle] in the chain.
type Handler struct {
	options Options
}

// Options holds the configuration of a [Handler]. All fields are private;
// build one with [NewOptions] for the defaults and chain the With* methods
// to configure it, then pass it to [New].
type Options struct {
	skip      func(*moon.Context) bool
	header    string
	generator func() string
	rleKey    string
	rleValue  moon.RequestLoggingEntryValue
}

// New returns a handler built from the given options, or defaults when none
// are given. Chain the With* methods on [NewOptions] to configure it, then
// register [Handler.Handle] in the chain:
//
//	app.Use("/*", New().Handle)
//	app.Use("/*", New(NewOptions().WithHeader("X-Correlation-ID")).Handle)
//
// The incoming request header is reused when it is a valid ID: non-empty
// printable ASCII (0x20-0x7E, inside spaces allowed). It arrives pre-trimmed
// because the HTTP server strips edge whitespace while parsing; otherwise
// an ID is generated (see [Options.WithGenerator]). The ID is echoed in
// the response header and stored for [GetFromContext], then the chain runs.
//
// To include the ID in request logs, add
// [Handler.GetRequestLoggingEntry] to the app before it starts:
//
//	h := New()
//	app.AddRequestLoggingEntry(h.GetRequestLoggingEntry())
//	app.Use("/*", h.Handle)
//
// Default header: "X-Request-ID" (see [Options.WithHeader]).
// Requests for which the [Options.WithSkip] predicate returns true run the
// chain untouched: no header is set and [GetFromContext] returns "".
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
	return Options{
		header:    "X-Request-ID",
		generator: func() string { return moon.GenerateSecureToken() },
		rleKey:    "request_id",
		rleValue:  func(ctx *moon.Context, err error) any { return GetFromContext(ctx) },
	}
}

// WithSkip skips ID assignment for requests where f returns true.
// The chain still runs; no header is set and [GetFromContext] returns "".
// It returns the same options for chaining.
//
// Default: nil (nothing is skipped)
func (me Options) WithSkip(f func(ctx *moon.Context) bool) Options {
	me.skip = f
	return me
}

// WithHeader sets the request/response header carrying the ID.
// Surrounding whitespace is trimmed. It panics on an empty name. It returns
// the same options for chaining.
//
// Default: "X-Request-ID"
func (me Options) WithHeader(s string) Options {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "header cannot be empty or whitespace")
	me.header = s
	return me
}

// WithGenerator sets the ID generator. Its output is trimmed and tried up to
// 3 times until it produces a valid ID; afterwards [moon.GenerateSecureToken]
// is used as a fallback. It panics on a nil generator. It returns the same
// options for chaining.
//
// Default: [moon.GenerateSecureToken].
func (me Options) WithGenerator(f func() string) Options {
	moon.Assert(f != nil, "generator func cannot be nil")
	me.generator = f
	return me
}

// WithRequestLoggingEntryKey sets the request logging entry key returned by
// [Handler.GetRequestLoggingEntry].
// Surrounding whitespace is trimmed. It panics on an empty key, and
// adding the same key twice to one app panics (see
// [moon.App.AddRequestLoggingEntry]): multiple instances on the same app
// need distinct keys. It returns the same options for chaining.
//
// Default: "request_id".
func (me Options) WithRequestLoggingEntryKey(s string) Options {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "key cannot be empty or whitespace")
	me.rleKey = s
	return me
}

// WithRequestLoggingEntryValueFunc sets the func rendering the request
// logging entry value returned by [Handler.GetRequestLoggingEntry]. It runs
// after the handler chain with the app's [moon.ErrorHandler] result as err,
// nil when the chain succeeded.
// It panics on a nil func. It returns the same options for chaining.
//
// Default: the request ID from [GetFromContext].
func (me Options) WithRequestLoggingEntryValueFunc(f moon.RequestLoggingEntryValue) Options {
	moon.Assert(f != nil, "value func cannot be nil")
	me.rleValue = f
	return me
}

// GetRequestLoggingEntry returns the request logging entry carrying the ID.
// Add it to the app during setup, before [moon.App.Start], to include the ID
// in request logs:
//
//	h := New()
//	app.AddRequestLoggingEntry(h.GetRequestLoggingEntry())
//	app.Use("/*", h.Handle)
//
// Customize the key and value with [Options.WithRequestLoggingEntryKey]
// and [Options.WithRequestLoggingEntryValueFunc].
func (me *Handler) GetRequestLoggingEntry() moon.RequestLoggingEntry {
	return moon.RequestLoggingEntry{
		Key:   me.options.rleKey,
		Value: me.options.rleValue,
	}
}

// Handle ensures every request has a request ID. The incoming request header
// is reused when valid, otherwise an ID is generated (see
// [Options.WithGenerator]). The ID is echoed in the response header and
// stored for [GetFromContext], then the chain runs.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	requestId := sanitizeRequestId(ctx.GetHeader(me.options.header), me.options.generator)
	ctx.SetHeader(me.options.header, requestId)
	ctx.SetLocal(localKey, requestId)

	return ctx.Next()
}

// GetFromContext returns the request ID assigned by the handler,
// or "" when the handler was skipped or never ran.
func GetFromContext(ctx *moon.Context) string {
	v, _ := ctx.GetLocal[string](localKey)
	return v
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
