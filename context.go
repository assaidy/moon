package moon

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"time"
)

// Context is the per-request handle passed to every [Handler].
// It carries the request and response, the matched route pattern and
// parameters, per-request locals, app-shared state, and the typed
// dependencies registered on the app and services started on the app
// (see [Context.GetDependency] and [Context.GetService]). A new Context is created for each request, so
// locals never leak between requests.
type Context struct {
	// Request is the incoming HTTP request.
	Request *http.Request
	// Response is the response writer. Prefer the Write methods and
	// SetHeader/AddHeader over writing to it directly.
	Response *httpResponseWriterWrapper

	params              map[string]string
	handlers            []Handler
	nextHandlerIndex    int
	pattern             string
	locals              map[string]any
	passLocalsToContext bool
	state               *sync.Map
	dependencies        map[reflect.Type]any
	services            map[reflect.Type]any
}

func newContext(
	w http.ResponseWriter,
	r *http.Request,
	pattern string,
	params map[string]string,
	handlers []Handler,
	state *sync.Map,
	dependencies map[reflect.Type]any,
	services map[reflect.Type]any,
	passLocalsToContext bool,
) *Context {
	ctx := new(Context)
	ctx.Response = &httpResponseWriterWrapper{writer: w}
	ctx.Request = r
	ctx.pattern = pattern
	ctx.params = params
	ctx.handlers = handlers
	ctx.locals = make(map[string]any)
	ctx.passLocalsToContext = passLocalsToContext
	ctx.state = state
	ctx.dependencies = dependencies
	ctx.services = services
	return ctx
}

var _ http.ResponseWriter = &httpResponseWriterWrapper{}

type httpResponseWriterWrapper struct {
	writer     http.ResponseWriter
	statusCode int
}

// Header implements [http.ResponseWriter]
func (me *httpResponseWriterWrapper) Header() http.Header {
	return me.writer.Header()
}

// Write implements [http.ResponseWriter]
func (me *httpResponseWriterWrapper) Write(data []byte) (int, error) {
	if me.statusCode == 0 {
		me.WriteHeader(http.StatusOK)
	}
	return me.writer.Write(data)
}

// WriteHeader() implements [http.ResponseWriter]
func (me *httpResponseWriterWrapper) WriteHeader(statusCode int) {
	me.statusCode = statusCode
	me.writer.WriteHeader(statusCode)
}

func (me *httpResponseWriterWrapper) Unwrap() http.ResponseWriter {
	return me.writer
}

// GetRemoteAddress returns the client address (host:port) the request came from.
func (me *Context) GetRemoteAddress() string {
	return me.Request.RemoteAddr
}

// TODO: implement ctx.RealIp() with trusted proxies and IP validations

// GetMethod returns the request HTTP method (GET, POST, ...).
func (me *Context) GetMethod() string {
	return me.Request.Method
}

// GetPattern returns the route pattern that matched the request
// (e.g. "/users/:id"). For middleware-only requests it is the request path.
func (me *Context) GetPattern() string {
	return me.pattern
}

// GetPath returns the request URL path without the query string.
func (me *Context) GetPath() string {
	return me.Request.URL.Path
}

// GetUrl returns the full request URL, including path and query.
func (me *Context) GetUrl() *url.URL {
	return me.Request.URL
}

// GetQueryString returns the raw encoded query string,
// or "" when the request has no query.
func (me *Context) GetQueryString() string {
	return me.Request.URL.RawQuery
}

// GetQuery returns the first value of the query key,
// or "" when the key is missing.
func (me *Context) GetQuery(key string) string {
	return me.Request.URL.Query().Get(key)
}

// GetAllQueries returns every query value grouped by key.
func (me *Context) GetAllQueries() map[string][]string {
	return me.Request.URL.Query()
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
	return me.Request.Header.Get(key)
}

// GetAllHeaders returns every value of the request header key,
// or nil when the key is missing.
func (me *Context) GetAllHeaders(key string) []string {
	return me.Request.Header.Values(key)
}

// SetHeader sets a response header, overwriting any previous values.
// Call it before writing the status code.
func (me *Context) SetHeader(key, value string) {
	me.Response.Header().Set(key, value)
}

// AddHeader appends a response header value, keeping previous values.
// Call it before writing the status code.
func (me *Context) AddHeader(key, value string) {
	me.Response.Header().Add(key, value)
}

// Read returns the full request body. It is empty when the request has no
// body, and a second call returns empty because the body is consumed.
func (me *Context) Read() ([]byte, error) {
	var buffer bytes.Buffer
	_, err := buffer.ReadFrom(me.Request.Body)
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
	return me.Request.FormValue(key)
}

// GetAllFormValues returns query and body form values merged by key.
// Body values come before query values.
func (me *Context) GetAllFormValues() map[string][]string {
	me.Request.ParseForm()
	return me.Request.Form
}

// SetCookie appends a Set-Cookie header to the response.
func (me *Context) SetCookie(c *http.Cookie) {
	http.SetCookie(me.Response, c)
}

// GetCookie returns the value of the request cookie key,
// or "" when the cookie is missing.
func (me *Context) GetCookie(key string) string {
	c, err := me.Request.Cookie(key)
	if err != nil {
		return ""
	}
	return c.Value
}

// GetManyCookies returns every value of the request cookies named key,
// or nil when no cookie with that name was sent.
func (me *Context) GetManyCookies(key string) []string {
	cookies := me.Request.CookiesNamed(key)
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
	cookies := me.Request.Cookies()
	if len(cookies) == 0 {
		return nil
	}
	groups := make(map[string][]string, len(cookies))
	for _, c := range cookies {
		groups[c.Name] = append(groups[c.Name], c.Value)
	}
	return groups
}

// ClearCookie expires every request cookie named name by writing an empty
// cookie with Max-Age=0 and a past Expires date. It writes nothing when no
// cookie with that name was sent.
func (me *Context) ClearCookie(name string) {
	cookies := me.Request.Cookies()
	for _, c := range cookies {
		if c.Name == name {
			c.Value = ""
			c.MaxAge = -1
			c.Expires = time.Unix(0, 0)
			http.SetCookie(me.Response, c)
		}
	}
}

// GetStatusCode returns the response status code written so far,
// or 0 when nothing was written yet (headers are still writable).
func (me *Context) GetStatusCode() int {
	return me.Response.statusCode
}

// SetStatusCode sends the response status code along with all headers set
// so far. It must be called after setting all headers: headers modified
// afterwards are not sent to the client.
//
// The status code starts at 0 (unwritten); if the handler chain finishes
// without writing anything, 200 is sent on the wire automatically.
func (me *Context) SetStatusCode(statusCode int) {
	me.Response.WriteHeader(statusCode)
}

// Write sends the status code and the raw string or bytes body.
// It returns the underlying write error.
func (me *Context) Write[T ~[]byte | ~string](statusCode int, raw T) error {
	me.Response.WriteHeader(statusCode)
	_, err := me.Response.Write([]byte(raw))
	return err
}

// WriteStatus sends the status code with its standard status text as body
// (e.g. 404 with "Not Found").
func (me *Context) WriteStatus(statusCode int) error {
	return me.Write(statusCode, http.StatusText(statusCode))
}

// WriteAs encodes value with the codec, sets the matching Content-Type
// header, and writes the status code with the encoded body. It returns the
// encode error without writing anything when encoding fails.
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
	if me.nextHandlerIndex == len(me.handlers) {
		return nil
	}
	index := me.nextHandlerIndex
	me.nextHandlerIndex += 1
	return me.handlers[index](me)
}

var _ context.Context = new(Context)

// Deadline implements [context.Context].
func (me *Context) Deadline() (deadline time.Time, ok bool) {
	return me.Request.Context().Deadline()
}

// Done implements [context.Context].
func (me *Context) Done() <-chan struct{} {
	return me.Request.Context().Done()
}

// Err implements [context.Context].
func (me *Context) Err() error {
	return me.Request.Context().Err()
}

// Value implements [context.Context].
func (me *Context) Value(key any) any {
	return me.Request.Context().Value(key)
}

// SetLocal stores a per-request value. It panics on an empty key or a nil
// value. When the [WithPassLocalsToContext] app option is enabled the value
// is also mirrored into the request context readable via [Context.Value].
func (me *Context) SetLocal(key string, value any) {
	Assert(key != "", "key cannot be empty")
	Assert(value != nil, "value cannot be nil")
	me.locals[key] = value
	if me.passLocalsToContext {
		me.Request = me.Request.WithContext(context.WithValue(me.Request.Context(), key, value))
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
	if me.passLocalsToContext {
		me.Request = me.Request.WithContext(context.WithValue(me.Request.Context(), key, nil))
	}
}

// SetState stores an app-shared value visible to every request.
// It panics on an empty key or a nil value.
func (me *Context) SetState(key, value any) {
	Assert(key != "", "key cannot be empty")
	Assert(value != nil, "value cannot be nil")
	me.state.Store(key, value)
}

// GetAnyState returns the app-shared value for key and whether it exists.
func (me *Context) GetAnyState(key string) (any, bool) {
	return me.state.Load(key)
}

// GetState returns the app-shared value for key asserted to T.
// ok is false when the key is missing or the value has another type.
func (me *Context) GetState[T any](key string) (T, bool) {
	var t T
	v, ok := me.state.Load(key)
	if !ok {
		return t, false
	}
	t, ok = v.(T)
	return t, ok
}

// DeleteState removes the app-shared value for key.
func (me *Context) DeleteState(key string) {
	me.state.Delete(key)
}
