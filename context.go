package moon

import (
	"bufio"
	"bytes"
	"context"
	"maps"
	"net"
	"net/http"
	"net/url"
	"time"
)

// TODO: add redirection

// Context is the per-request handle passed to every [Handler].
// It carries the request and response, the matched route pattern and
// parameters, per-request locals, app-shared state, and the typed
// dependencies registered on the app and services started on the app
// (see [Context.GetDependency] and [Context.GetService]). A new Context is created for each request, so
// locals never leak between requests.
type Context struct {
	// request is the incoming HTTP request.
	request *http.Request
	// response buffers status, headers and body until the handler chain
	// finishes, then flushes them to the client. This lets middleware set
	// headers or status after calling [Context.Next] (e.g. response-time).
	//
	// Do not mix buffered writes with the raw writer from Unwrap (including
	// via [http.NewResponseController]): any use of the raw writer marks the
	// request as raw and the buffered response is discarded at flush.
	response *httpResponseWriterWrapper

	params           map[string]string
	handlers         []Handler
	nextHandlerIndex int
	pattern          string
	locals           map[string]any
	app              *App
}

func newContext(
	w http.ResponseWriter,
	r *http.Request,
	pattern string,
	params map[string]string,
	handlers []Handler,
	app *App,
) *Context {
	ctx := new(Context)
	ctx.response = new(httpResponseWriterWrapper{
		// TODO: consider using memory pools for buffers and maps
		headers: make(http.Header),
		body:    new(bytes.Buffer),
	})
	ctx.response.tracker = new(httpResponseWriterTracker{
		writer:     w,
		statusCode: &ctx.response.statusCode,
	})
	ctx.request = r
	ctx.pattern = pattern
	ctx.params = params
	ctx.handlers = handlers
	ctx.locals = make(map[string]any)
	ctx.app = app
	return ctx
}

type httpResponseWriterWrapper struct {
	tracker    *httpResponseWriterTracker
	statusCode int
	headers    http.Header
	body       *bytes.Buffer
}

var _ http.ResponseWriter = new(httpResponseWriterWrapper)

// Header implements [http.ResponseWriter]. It returns the buffered headers;
// mutations apply at flush, even after [Context.Next] returns or the status
// code was set.
func (me *httpResponseWriterWrapper) Header() http.Header {
	return me.headers
}

// Write implements [http.ResponseWriter]. It buffers data and defaults the
// status code to 200 when unset. Nothing is sent until flush.
func (me *httpResponseWriterWrapper) Write(data []byte) (int, error) {
	if me.statusCode == 0 {
		me.statusCode = 200
	}
	return me.body.Write(data)
}

// WriteHeader implements [http.ResponseWriter]. It records the status code
// without sending anything; a later call overwrites the previous value.
// Headers and body set before or after still flush together.
func (me *httpResponseWriterWrapper) WriteHeader(statusCode int) {
	me.statusCode = statusCode
}

// Unwrap returns the raw-path writer. Any use of it (Header/Write/WriteHeader/
// Flush/Hijack, directly or via [http.NewResponseController]) marks the request
// as raw: the buffered flush is skipped and the raw response stands.
func (me *httpResponseWriterWrapper) Unwrap() http.ResponseWriter {
	return me.tracker
}

// flush sends the buffered response unless the raw writer was used, in which
// case the buffered response is discarded and the raw one stands.
func (me *httpResponseWriterWrapper) flush() {
	if me.tracker.used {
		return
	}
	maps.Copy(me.tracker.Header(), me.headers)
	if me.statusCode == 0 {
		me.statusCode = 200
	}
	me.tracker.WriteHeader(me.statusCode)
	me.tracker.Write(me.body.Bytes())
}

type httpResponseWriterTracker struct {
	writer     http.ResponseWriter
	used       bool
	statusCode *int
}

var _ http.ResponseWriter = new(httpResponseWriterTracker)
var _ http.Flusher = new(httpResponseWriterTracker)
var _ http.Hijacker = new(httpResponseWriterTracker)

// Header implements [http.ResponseWriter] on the raw path. Any call marks the
// request as raw and the buffered response is discarded at flush.
func (me *httpResponseWriterTracker) Header() http.Header {
	me.used = true
	return me.writer.Header()
}

// Write implements [http.ResponseWriter] on the raw path. It marks the request
// as raw and defaults the shared status code to 200 when unset.
func (me *httpResponseWriterTracker) Write(data []byte) (int, error) {
	me.used = true
	if *me.statusCode == 0 {
		me.WriteHeader(200)
	}
	return me.writer.Write(data)
}

// WriteHeader implements [http.ResponseWriter] on the raw path. It marks the
// request as raw and records the shared status code.
func (me *httpResponseWriterTracker) WriteHeader(statusCode int) {
	me.used = true
	*me.statusCode = statusCode
	me.writer.WriteHeader(statusCode)
}

// Flush implements [http.Flusher] on the raw path. It marks the request as raw
// and defaults the shared status code to 200 when unset.
func (me *httpResponseWriterTracker) Flush() {
	me.used = true
	if *me.statusCode == 0 {
		*me.statusCode = 200
	}
	http.NewResponseController(me.writer).Flush()
}

// Hijack implements [http.Hijacker] on the raw path. It marks the request as
// raw; the buffered response is discarded.
func (me *httpResponseWriterTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	me.used = true
	return http.NewResponseController(me.writer).Hijack()
}

// GetHttpRequest returns the underlying incoming HTTP request. It is an
// escape hatch for stdlib interop; prefer the Context getters when available.
func (me *Context) GetHttpRequest() *http.Request {
	return me.request
}

// GetHttpResponseWriter returns the buffered response writer. Writes are
// buffered until flush: headers or status can be set before or after the
// status code, and after [Context.Next] returns. Use Unwrap on the returned
// writer for the raw path; any raw use discards the buffer at flush.
func (me *Context) GetHttpResponseWriter() *httpResponseWriterWrapper {
	return me.response
}

// GetRemoteAddress returns the client address (host:port) the request came from.
func (me *Context) GetRemoteAddress() string {
	return me.request.RemoteAddr
}

// TODO: implement ctx.RealIp() with trusted proxies and IP validations

// GetMethod returns the request HTTP method (GET, POST, ...).
func (me *Context) GetMethod() string {
	return me.request.Method
}

// GetPattern returns the route pattern that matched the request
// (e.g. "/users/:id"). It is empty for middleware-only requests and for
// unmatched requests ([ErrInvalidEndpoint], [ErrMethodNotAllowed]),
// since the pattern is only set for routes registered by [App.Handle].
func (me *Context) GetPattern() string {
	return me.pattern
}

// GetPath returns the request URL path without the query string.
func (me *Context) GetPath() string {
	return me.request.URL.Path
}

// GetUrl returns the full request URL, including path and query.
func (me *Context) GetUrl() *url.URL {
	return me.request.URL
}

// GetQueryString returns the raw encoded query string,
// or "" when the request has no query.
func (me *Context) GetQueryString() string {
	return me.request.URL.RawQuery
}

// GetQuery returns the first value of the query key,
// or "" when the key is missing.
func (me *Context) GetQuery(key string) string {
	return me.request.URL.Query().Get(key)
}

// GetAllQueries returns every query value grouped by key.
func (me *Context) GetAllQueries() map[string][]string {
	return me.request.URL.Query()
}

// GetParam returns the path parameter value for key,
// or "" when the key is missing.
func (me *Context) GetParam(key string) string {
	return me.params[key]
}

// GetAllParams returns every path parameter of the matched route.
func (me *Context) GetAllParams() map[string]string {
	return me.params
}

// GetHeader returns the first value of the request header key,
// or "" when the key is missing.
func (me *Context) GetHeader(key string) string {
	return me.request.Header.Get(key)
}

// GetAllHeaders returns every value of the request header key,
// or nil when the key is missing.
func (me *Context) GetAllHeaders(key string) []string {
	return me.request.Header.Values(key)
}

// SetHeader sets a buffered response header, overwriting any previous values.
// Buffered until flush: it can be called before or after the status code is
// set, and after [Context.Next] returns.
func (me *Context) SetHeader(key, value string) {
	me.response.Header().Set(key, value)
}

// AddHeader appends a buffered response header value, keeping previous values.
// Buffered until flush: it can be called before or after the status code is
// set, and after [Context.Next] returns.
func (me *Context) AddHeader(key, value string) {
	me.response.Header().Add(key, value)
}

// Read returns the full request body. It is empty when the request has no
// body, and a second call returns empty because the body is consumed.
func (me *Context) Read() ([]byte, error) {
	// TODO: buffer body to allow multiple reads
	var buffer bytes.Buffer
	_, err := buffer.ReadFrom(me.request.Body)
	return buffer.Bytes(), err
}

// ReadAs reads the body and decodes it into out. It returns the decode
// error when the body does not match the codec.
func (me *Context) ReadAs(codec Codec, out any) error {
	raw, err := me.Read()
	if err != nil {
		return err
	}
	return codec.Decode(raw, out)
}

// GetFormValue returns the first form value for key, searching the body
// before the URL query, or "" when the key is missing.
func (me *Context) GetFormValue(key string) string {
	return me.request.FormValue(key)
}

// GetAllFormValues returns query and body form values merged by key.
// Body values come before query values.
func (me *Context) GetAllFormValues() map[string][]string {
	me.request.ParseForm()
	return me.request.Form
}

// SetCookie appends a Set-Cookie header to the buffered response.
func (me *Context) SetCookie(c *http.Cookie) {
	http.SetCookie(me.response, c)
}

// GetCookie returns the value of the request cookie key,
// or "" when the cookie is missing.
func (me *Context) GetCookie(key string) string {
	c, err := me.request.Cookie(key)
	if err != nil {
		return ""
	}
	return c.Value
}

// GetManyCookies returns every value of the request cookies named key,
// or nil when no cookie with that name was sent.
func (me *Context) GetManyCookies(key string) []string {
	cookies := me.request.CookiesNamed(key)
	if len(cookies) == 0 {
		return nil
	}
	values := make([]string, 0, len(cookies))
	for _, c := range cookies {
		values = append(values, c.Value)
	}
	return values
}

// GetAllCookies returns every request cookie value grouped by name,
// or nil when the request has no cookies.
func (me *Context) GetAllCookies() map[string][]string {
	cookies := me.request.Cookies()
	if len(cookies) == 0 {
		return nil
	}
	groups := make(map[string][]string, len(cookies))
	for _, c := range cookies {
		groups[c.Name] = append(groups[c.Name], c.Value)
	}
	return groups
}

// ClearCookie expires every request cookie named name by buffering an empty
// cookie with Max-Age=0 and a past Expires date. It buffers nothing when no
// cookie with that name was sent.
func (me *Context) ClearCookie(name string) {
	cookies := me.request.Cookies()
	for _, c := range cookies {
		if c.Name == name {
			c.Value = ""
			c.MaxAge = -1
			c.Expires = time.Unix(0, 0)
			http.SetCookie(me.response, c)
		}
	}
}

// GetStatusCode returns the response status code recorded so far, shared by
// the buffered and raw paths, or 0 when nothing was written yet.
func (me *Context) GetStatusCode() int {
	return me.response.statusCode
}

// SetStatusCode records the response status code; it is sent at flush.
// A later call overwrites the previous value, and headers or body set before
// or after still flush together.
//
// The status code starts at 0 (unwritten); if the handler chain finishes
// without writing anything, 200 is sent on the wire automatically.
func (me *Context) SetStatusCode(statusCode int) {
	me.response.WriteHeader(statusCode)
}

// Write buffers the status code with the raw string or bytes body; both are
// sent at flush. It returns the buffer write error (always nil).
func (me *Context) Write[T ~[]byte | ~string](statusCode int, raw T) error {
	me.response.WriteHeader(statusCode)
	_, err := me.response.Write([]byte(raw))
	return err
}

// WriteStatus buffers the status code with its standard status text as body
// (e.g. 404 with "Not Found").
func (me *Context) WriteStatus(statusCode int) error {
	return me.Write(statusCode, http.StatusText(statusCode))
}

// WriteAs encodes value with the codec, sets the matching Content-Type
// header in the buffer, and buffers the status code with the encoded body.
// It returns the encode error without buffering anything when encoding fails.
func (me *Context) WriteAs(statusCode int, codec Codec, value any) error {
	data, err := codec.Encode(value)
	if err != nil {
		return err
	}
	me.SetHeader("Content-Type", codec.ContentType())
	return me.Write(statusCode, data)
}

// Next invokes the next handler in the chain and returns its error.
// It returns nil when the chain is exhausted.
func (me *Context) Next() error {
	if me.IsFinal() {
		return nil
	}
	index := me.nextHandlerIndex
	me.nextHandlerIndex += 1
	return me.handlers[index](me)
}

// IsFinal reports whether no handlers remain in the chain. It is true inside
// the last handler and after the chain is exhausted; [Context.Next] then
// returns nil without invoking anything.
func (me *Context) IsFinal() bool {
	return me.nextHandlerIndex == len(me.handlers)
}

var _ context.Context = new(Context)

// Deadline implements [context.Context].
func (me *Context) Deadline() (deadline time.Time, ok bool) {
	return me.request.Context().Deadline()
}

// Done implements [context.Context].
func (me *Context) Done() <-chan struct{} {
	return me.request.Context().Done()
}

// Err implements [context.Context].
func (me *Context) Err() error {
	return me.request.Context().Err()
}

// Value implements [context.Context].
func (me *Context) Value(key any) any {
	return me.request.Context().Value(key)
}

// SetLocal stores a per-request value. It panics on an empty key or a nil
// value. When the [WithPassLocalsToContext] app option is enabled the value
// is also mirrored into the request context readable via [Context.Value].
func (me *Context) SetLocal(key string, value any) {
	Assert(key != "", "key cannot be empty")
	Assert(value != nil, "value cannot be nil")
	me.locals[key] = value
	if me.app.passLocalsToContext {
		me.request = me.request.WithContext(context.WithValue(me.request.Context(), key, value))
	}
}

// GetAnyLocal returns the per-request value for key and whether it exists.
func (me *Context) GetAnyLocal[T any](key string) (any, bool) {
	v, ok := me.locals[key]
	return v, ok
}

// GetLocal returns the per-request value for key asserted to T.
// ok is false when the key is missing or the value has another type.
func (me *Context) GetLocal[T any](key string) (T, bool) {
	v, ok := me.locals[key].(T)
	return v, ok
}

// DeleteLocal removes the per-request value for key. Deleting a missing key
// is a no-op. When the [WithPassLocalsToContext] app option is enabled the
// key is shadowed with nil in the request context.
func (me *Context) DeleteLocal(key string) {
	delete(me.locals, key)
	if me.app.passLocalsToContext {
		me.request = me.request.WithContext(context.WithValue(me.request.Context(), key, nil))
	}
}

// SetState stores an app-shared value visible to every request.
// It panics on an empty key or a nil value.
func (me *Context) SetState(key, value any) {
	Assert(key != "", "key cannot be empty")
	Assert(value != nil, "value cannot be nil")
	me.app.state.Store(key, value)
}

// GetAnyState returns the app-shared value for key and whether it exists.
func (me *Context) GetAnyState(key string) (any, bool) {
	return me.app.state.Load(key)
}

// GetState returns the app-shared value for key asserted to T.
// ok is false when the key is missing or the value has another type.
func (me *Context) GetState[T any](key string) (T, bool) {
	var t T
	v, ok := me.app.state.Load(key)
	if !ok {
		return t, false
	}
	t, ok = v.(T)
	return t, ok
}

// DeleteState removes the app-shared value for key.
func (me *Context) DeleteState(key string) {
	me.app.state.Delete(key)
}
