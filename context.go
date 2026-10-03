package moon

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Context is the per-request handle passed to every [Handler].
// It carries the request and response, the matched route pattern and
// parameters, per-request locals, app-shared state, and the typed
// dependencies registered on the app and services started on the app
// (see [Context.GetDependency] and [Context.GetService]). A new Context is created for each request, so
// locals never leak between requests.
type Context struct {
	// request is the incoming HTTP request.
	request *http.Request
	// response buffers status and body until the handler chain finishes,
	// then flushes them to the client. Headers go directly to the underlying
	// writer. This lets middleware set headers or status after calling
	// [Context.Next] (e.g. response-time).
	//
	// Do not mix buffered writes with the raw writer from Unwrap (including
	// via [http.NewResponseController]): any use of the raw writer marks the
	// request as raw and the buffered response is discarded at flush.
	response *httpResponseWriterWrapper

	readRequestBodyOnce  sync.Once
	requestBodyReadError error
	requestBodyBuffer    *bytes.Buffer
	pattern              string
	params               map[string]string
	handlers             []Handler
	nextHandlerIndex     int
	middlewareCount      int
	locals               map[string]any
	app                  *App
}

func (me *App) newContext(
	w http.ResponseWriter,
	r *http.Request,
) *Context {
	ctx := new(Context)
	ctx.app = me
	ctx.response = &httpResponseWriterWrapper{bodyBuffer: bodyBufferPool.Get().(*bytes.Buffer)}
	ctx.response.tracker = &httpResponseWriterTracker{writer: w, statusCode: &ctx.response.statusCode}
	ctx.request = r
	ctx.request.Body = httpRequestBodyReaderWrapper{body: http.MaxBytesReader(w, r.Body, int64(me.options.readLimit))}
	ctx.requestBodyBuffer = bodyBufferPool.Get().(*bytes.Buffer)
	ctx.locals = make(map[string]any)
	return ctx
}

var bodyBufferPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

type httpRequestBodyReaderWrapper struct {
	body io.ReadCloser
}

var _ io.ReadCloser = httpRequestBodyReaderWrapper{}

func (me httpRequestBodyReaderWrapper) Close() error {
	return me.body.Close()
}

func (me httpRequestBodyReaderWrapper) Read(p []byte) (n int, err error) {
	n, err = me.body.Read(p)
	if err == nil || err == io.EOF {
		return n, err
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return n, ErrRequestEntityTooLarge
	}
	if err, ok := errors.AsType[net.Error](err); ok && err.Timeout() {
		return n, ErrRequestTimeout
	}
	return n, err
}

type httpResponseWriterWrapper struct {
	tracker    *httpResponseWriterTracker
	statusCode int
	bodyBuffer *bytes.Buffer
}

var _ http.ResponseWriter = (*httpResponseWriterWrapper)(nil)

// Header implements [http.ResponseWriter]. It writes directly to the
// underlying writer; headers alone never flush, so it is safe to set them
// before or after the status code, after [Context.Next] returns, or before
// switching to the raw path (e.g. SSE via [http.NewResponseController]).
func (me *httpResponseWriterWrapper) Header() http.Header {
	return me.tracker.writer.Header()
}

// Write implements [http.ResponseWriter]. It buffers data and defaults the
// status code to 200 when unset. Nothing is sent until flush.
func (me *httpResponseWriterWrapper) Write(data []byte) (int, error) {
	if me.statusCode == 0 {
		me.statusCode = 200
	}
	return me.bodyBuffer.Write(data)
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

// flush sends the buffered status and body unless the raw writer was used,
// in which case the buffered response is discarded and the raw one stands.
// Headers were already written directly to the underlying writer. The status
// defaults to 200 before the raw check so request logging, the only context
// use after flush, logs 200 for a raw use without an explicit status.
func (me *httpResponseWriterWrapper) flush() {
	if me.statusCode == 0 {
		me.statusCode = 200
	}
	if me.tracker.used {
		return
	}
	me.tracker.WriteHeader(me.statusCode)
	me.tracker.Write(me.bodyBuffer.Bytes())
}

type httpResponseWriterTracker struct {
	writer     http.ResponseWriter
	used       bool
	statusCode *int
}

var _ http.ResponseWriter = (*httpResponseWriterTracker)(nil)
var _ http.Flusher = (*httpResponseWriterTracker)(nil)
var _ http.Hijacker = (*httpResponseWriterTracker)(nil)

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

// GetHttpResponseWriter returns the response writer. Status and body are
// buffered until flush, while headers go directly to the underlying writer:
// they can be set before or after the status code, and after [Context.Next]
// returns. Use Unwrap on the returned writer for the raw path; any raw use
// discards the buffer at flush.
func (me *Context) GetHttpResponseWriter() *httpResponseWriterWrapper {
	return me.response
}

// GetRemoteAddress returns the client address (host:port) the request came from.
func (me *Context) GetRemoteAddress() string {
	return me.request.RemoteAddr
}

// TODO: add ctx.RealIp() with trusted proxies and IP validations

// GetMethod returns the request HTTP method (GET, POST, ...).
func (me *Context) GetMethod() string {
	return me.request.Method
}

// GetPattern returns the route pattern that matched the request
// (e.g. "/users/:id"). It is empty when no route matches, including
// middleware-only requests and unmatched requests ([ErrInvalidEndpoint],
// [ErrMethodNotAllowed]). See [App.Map].
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

// GetAllHeaders returns every request header value grouped by key.
func (me *Context) GetAllHeaders() map[string][]string {
	return me.request.Header
}

// SetHeader sets a response header directly on the underlying writer,
// overwriting any previous values. Headers alone never flush, so it can be
// called before or after the status code is set, and after [Context.Next]
// returns.
func (me *Context) SetHeader(key, value string) {
	me.response.Header().Set(key, value)
}

// AddHeader appends a response header value directly on the underlying
// writer, keeping previous values. Headers alone never flush, so it can be
// called before or after the status code is set, and after [Context.Next]
// returns.
func (me *Context) AddHeader(key, value string) {
	me.response.Header().Add(key, value)
}

// Read returns the full request body. Unlike a raw read of the request body,
// it consumes the entire body and caches it, so repeated calls return the
// same bytes. It is empty when the request has no body. This is enough for
// most cases.
//
// Because the whole body is buffered in memory, do not use it for unbounded
// or very large bodies: an infinite stream hangs forever waiting for EOF,
// and a large upload spikes memory. Stream such bodies directly from the
// raw request body instead (see [Context.GetHttpRequest]).
//
// Bodies larger than [App.WithReadLimit] fail with [ErrRequestEntityTooLarge].
// Reads that time out fail with [ErrRequestTimeout].
//
// The returned slice aliases a pooled buffer that is recycled after the
// handler chain finishes: copy it first if you need it afterwards.
func (me *Context) Read() ([]byte, error) {
	me.readRequestBodyOnce.Do(func() {
		_, err := me.requestBodyBuffer.ReadFrom(me.request.Body)
		me.requestBodyReadError = err
	})
	return me.requestBodyBuffer.Bytes(), me.requestBodyReadError
}

// ReadAs reads the body and decodes it into out. It returns the decode
// error when the body does not match the codec, [ErrRequestEntityTooLarge]
// when the body exceeds [App.WithReadLimit], or [ErrRequestTimeout] when
// the read times out.
func (me *Context) ReadAs(codec Codec, out any) error {
	raw, err := me.Read()
	if err != nil {
		return err
	}
	return codec.Decode(raw, out)
}

// ReadJson reads the body and decodes it from JSON into out.
// See [Context.ReadAs].
func (me *Context) ReadJson(out any) error {
	return me.ReadAs(CodecJson, out)
}

// ReadXml reads the body and decodes it from XML into out.
// See [Context.ReadAs].
func (me *Context) ReadXml(out any) error {
	return me.ReadAs(CodecXml, out)
}

// ReadMessagePack reads the body and decodes it from MessagePack into out.
// See [Context.ReadAs].
func (me *Context) ReadMessagePack(out any) error {
	return me.ReadAs(CodecMessagePack, out)
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

// SetCookie appends a Set-Cookie header to the response.
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

// ClearCookie expires every request cookie named name by setting an empty
// cookie with Max-Age=0 and a past Expires date. It sets nothing when no
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
// header, and buffers the status code with the encoded body.
// It returns the encode error without buffering anything when encoding fails.
func (me *Context) WriteAs(statusCode int, codec Codec, value any) error {
	data, err := codec.Encode(value)
	if err != nil {
		return err
	}
	me.SetHeader("Content-Type", codec.ContentType())
	return me.Write(statusCode, data)
}

// WriteJson encodes value as JSON, sets the matching Content-Type header,
// and buffers the status code with the encoded body.
// See [Context.WriteAs].
func (me *Context) WriteJson(statusCode int, value any) error {
	return me.WriteAs(statusCode, CodecJson, value)
}

// WriteXml encodes value as XML, sets the matching Content-Type header,
// and buffers the status code with the encoded body.
// See [Context.WriteAs].
func (me *Context) WriteXml(statusCode int, value any) error {
	return me.WriteAs(statusCode, CodecXml, value)
}

// WriteMessagePack encodes value as MessagePack, sets the matching
// Content-Type header, and buffers the status code with the encoded body.
// See [Context.WriteAs].
func (me *Context) WriteMessagePack(statusCode int, value any) error {
	return me.WriteAs(statusCode, CodecMessagePack, value)
}

// Next invokes the next handler in the chain and returns its error.
// It returns nil when the chain is exhausted.
func (me *Context) Next() error {
	if me.nextHandlerIndex >= len(me.handlers) {
		return nil
	}

	index := me.nextHandlerIndex
	me.nextHandlerIndex += 1
	return me.handlers[index](me)
}

// IsMiddleware reports whether the current handler runs as a middleware.
// It is true inside handlers registered via [App.Use].
func (me *Context) IsMiddleware() bool {
	return me.nextHandlerIndex > 0 && me.nextHandlerIndex <= me.middlewareCount
}

var _ context.Context = (*Context)(nil)

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
// value. When the [App.WithPassLocalsToContext] app option is enabled the value
// is also mirrored into the request context readable via [Context.Value].
func (me *Context) SetLocal(key string, value any) {
	Assert(key != "", "key cannot be empty")
	Assert(value != nil, "value cannot be nil")
	me.locals[key] = value
	if me.app.options.passLocalsToContext {
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
// is a no-op. When the [App.WithPassLocalsToContext] app option is enabled the
// key is shadowed with nil in the request context.
func (me *Context) DeleteLocal(key string) {
	delete(me.locals, key)
	if me.app.options.passLocalsToContext {
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

// Redirect responds with a redirection to location using statusCode.
// It sets the Location header (resolving relative locations against the
// request path and percent-encoding non-ASCII bytes).
// It returns an error when location is not a valid URL.
//
// Permanent redirections tell the client to replace the original URL:
//   - 301 Moved Permanently: GET stays GET, others may become GET.
//   - 308 Permanent Redirect: method and body are unchanged.
//
// Temporary redirections keep the original URL valid:
//   - 302 Found: GET stays GET, others may become GET.
//   - 303 See Other: other methods become GET, body is lost.
//   - 307 Temporary Redirect: method and body are unchanged.
//
// In most cases you will use 302 Found. It's the default for most servers.
//
// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Redirections
func (me *Context) Redirect(statusCode int, location string) error {
	u, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("invalid redirect location url: %w", err)
	}

	// If url was relative, make its path absolute by
	// combining with request path.
	// The client would probably do this for us,
	// but doing it ourselves is more reliable.
	// See RFC 7231, section 7.1.2
	if u.Scheme == "" && u.Host == "" {
		oldpath := me.request.URL.EscapedPath()
		if oldpath == "" { // should not happen, but avoid a crash if it does
			oldpath = "/"
		}

		if location == "" || location[0] != '/' {
			// make relative path absolute
			olddir, _ := path.Split(oldpath)
			location = olddir + location
		}

		var query string
		if i := strings.Index(location, "?"); i != -1 {
			location, query = location[:i], location[i:]
		}

		// clean up
		location = path.Clean(location) + query
	}

	me.SetHeader("Location", hexEscapeNonAscii(location))
	me.SetStatusCode(statusCode)
	return nil
}

// hexEscapeNonAscii percent-encodes bytes >= [utf8.RuneSelf], matching
// [http.Redirect] so non-ASCII Location values stay valid header bytes.
func hexEscapeNonAscii(s string) string {
	newLen := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			newLen += 3
		} else {
			newLen++
		}
	}
	if newLen == len(s) {
		return s
	}
	b := make([]byte, 0, newLen)
	var pos int
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			if pos < i {
				b = append(b, s[pos:i]...)
			}
			b = append(b, '%')
			b = strconv.AppendInt(b, int64(s[i]), 16)
			pos = i + 1
		}
	}
	if pos < len(s) {
		b = append(b, s[pos:]...)
	}
	return string(b)
}
