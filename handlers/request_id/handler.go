// Package request_id provides a handler that assigns every request a
// request ID, echoes it in a response header, and makes it available to
// handlers via [GetFromContext]. Register [Handler.Handle] as a middleware
// or a route handler:
//
//	app.Use("/*", request_id.New().Handle)
//
// Customize the header or generator:
//
//	app.Use("/*", request_id.New().WithHeader("X-Correlation-ID").Handle)
package request_id

import (
	"strings"

	"github.com/assaidy/moon"
)

const localKey = "moon.handlers.request_id.local_key"

// Handler ensures every request has a request ID. Use [New] to construct
// it with defaults, chain the With* methods to configure it, then register
// [Handler.Handle] in the chain.
type Handler struct {
	skip                         func(*moon.Context) bool
	header                       string
	generator                    func() string
	requestLoggingEntryKey       string
	requestLoggingEntryValueFunc moon.RequestLoggingEntryValueFunc
}

// New returns a handler with default options. Chain the With* methods to
// configure it, then register [Handler.Handle] in the chain:
//
//	app.Use("/*", New().Handle)
//	app.Use("/*", New().WithHeader("X-Correlation-ID").Handle)
//
// The incoming request header is reused when it is a valid ID: non-empty
// printable ASCII (0x20-0x7E, inside spaces allowed). It arrives pre-trimmed
// because the HTTP server strips edge whitespace while parsing; otherwise
// an ID is generated (see [Handler.WithGenerator]). The ID is echoed in
// the response header and stored for [GetFromContext], then the chain runs.
//
// To include the ID in request logs, register [Handler.GetRequestLoggingEntry]
// on the app:
//
//	h := New()
//	app.RegisterRequestLoggingEntry(h.GetRequestLoggingEntry())
//	app.Use("/*", h.Handle)
//
// Default header: "X-Request-ID" (see [Handler.WithHeader]).
// Requests for which the [Handler.WithSkip] predicate returns true run the
// chain untouched: no header is set and [GetFromContext] returns "".
func New() *Handler {
	return &Handler{
		header:                       "X-Request-ID",
		generator:                    func() string { return moon.GenerateSecureToken() },
		requestLoggingEntryKey:       "request_id",
		requestLoggingEntryValueFunc: GetFromContext,
	}
}

// WithSkip skips ID assignment for requests where f returns true.
// The chain still runs; no header is set and [GetFromContext] returns "".
// It returns the same handler for chaining.
//
// Default: nil (nothing is skipped)
func (me *Handler) WithSkip(f func(ctx *moon.Context) bool) *Handler {
	me.skip = f
	return me
}

// WithHeader sets the request/response header carrying the ID.
// Surrounding whitespace is trimmed. It panics on an empty name. It returns
// the same handler for chaining.
//
// Default: "X-Request-ID"
func (me *Handler) WithHeader(s string) *Handler {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "header cannot be empty or whitespace")
	me.header = s
	return me
}

// WithGenerator sets the ID generator. Its output is trimmed and tried up to
// 3 times until it produces a valid ID; afterwards [moon.GenerateSecureToken]
// is used as a fallback. It panics on a nil generator. It returns the same
// handler for chaining.
//
// Default: [moon.GenerateSecureToken].
func (me *Handler) WithGenerator(f func() string) *Handler {
	moon.Assert(f != nil, "generator func cannot be nil")
	me.generator = f
	return me
}

// WithRequestLoggingEntryKey sets the request logging entry key returned by
// [Handler.GetRequestLoggingEntry].
// Surrounding whitespace is trimmed. It panics on an empty key, and
// registering the same key twice on one app panics: multiple instances on
// the same app need distinct keys. It returns the same handler for
// chaining.
//
// Default: "request_id".
func (me *Handler) WithRequestLoggingEntryKey(s string) *Handler {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "key cannot be empty or whitespace")
	me.requestLoggingEntryKey = s
	return me
}

// WithRequestLoggingEntryValueFunc sets the func rendering the request
// logging entry value returned by [Handler.GetRequestLoggingEntry].
// It panics on a nil func. It returns the same handler for chaining.
//
// Default: [GetFromContext].
func (me *Handler) WithRequestLoggingEntryValueFunc(f moon.RequestLoggingEntryValueFunc) *Handler {
	moon.Assert(f != nil, "value func cannot be nil")
	me.requestLoggingEntryValueFunc = f
	return me
}

// GetRequestLoggingEntry returns the request logging entry carrying the ID.
// Register it on the app to include the ID in request logs:
//
//	h := New()
//	app.RegisterRequestLoggingEntry(h.GetRequestLoggingEntry())
//	app.Use("/*", h.Handle)
//
// Customize the key and value with [Handler.WithRequestLoggingEntryKey]
// and [Handler.WithRequestLoggingEntryValueFunc].
func (me *Handler) GetRequestLoggingEntry() moon.RequestLoggingEntry {
	return moon.RequestLoggingEntry{
		Key:       me.requestLoggingEntryKey,
		ValueFunc: me.requestLoggingEntryValueFunc,
	}
}

// Handle ensures every request has a request ID. The incoming request header
// is reused when valid, otherwise an ID is generated (see
// [Handler.WithGenerator]). The ID is echoed in the response header and
// stored for [GetFromContext], then the chain runs.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.skip != nil && me.skip(ctx) {
		return ctx.Next()
	}

	requestId := sanitizeRequestId(ctx.GetHeader(me.header), me.generator)
	ctx.SetHeader(me.header, requestId)
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
