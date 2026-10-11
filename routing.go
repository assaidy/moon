package moon

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// TODO: add static files support (maybe as a middleware)

func (me *App) registerRootHandler() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// trim trailing forward slashes except the root "/"
		if r.URL.Path != "/" {
			r.URL.Path = strings.TrimRight(r.URL.Path, "/")
		}
		// normalize the path so matching is case-insensitive
		r.URL.Path = strings.ToLower(r.URL.Path)

		ctx := me.newContext(w, r)
		me.dispatch(ctx)

		var rleSnapshot []RequestLoggingEntry
		if me.options.enableRequestLogging {
			me.rleMutex.RLock()
			rleSnapshot = slices.Clone(me.options.rle)
			me.rleMutex.RUnlock()

			for _, e := range rleSnapshot {
				if e.Before != nil {
					e.Before(ctx)
				}
			}
		}

		err := ctx.Next()
		if err != nil {
			err = me.options.errorHandler(ctx, err)
		}
		ctx.response.flush()

		if me.options.enableRequestLogging {
			me.logRequest(ctx, err, rleSnapshot)
		}

		ctx.requestBodyBuffer.Reset()
		bodyBufferPool.Put(ctx.requestBodyBuffer)
		ctx.response.bodyBuffer.Reset()
		bodyBufferPool.Put(ctx.response.bodyBuffer)
	})

	me.httpServer.Handler = mux
}

func (me *App) dispatch(ctx *Context) {
	ctx.handlers = []Handler{func(ctx *Context) error {
		// Bodies declaring more than the read limit are rejected without reading
		// them. Unknown sizes are enforced while reading instead.
		if ctx.request.ContentLength > int64(me.options.readLimit) {
			return ErrRequestEntityTooLarge
		}
		return ctx.Next()
	}}

	var targetRoute *RouteEntry

	routes := me.routesPerMethod[ctx.GetMethod()]
	for i := range routes {
		if params, ok := routes[i].matchPath(ctx.GetPath()); ok {
			ctx.params = params
			targetRoute = &routes[i]
			break
		}
	}

	for _, middleware := range me.middlewares {
		if targetRoute != nil && middleware.order >= targetRoute.order {
			// only filter by order if we found a matching route.
			continue
		}
		if middleware.matchPath(ctx.GetPath()) {
			ctx.handlers = append(ctx.handlers, middleware.handlers...)
		}
	}
	ctx.middlewareCount = len(ctx.handlers)

	if targetRoute != nil {
		ctx.handlers = append(ctx.handlers, targetRoute.handlers...)
		return
	}

	for method, routes := range me.routesPerMethod {
		if method == ctx.GetMethod() {
			continue
		}
		for _, route := range routes {
			if _, ok := route.matchPath(ctx.GetPath()); ok {
				ctx.handlers = append(ctx.handlers, func(ctx *Context) error { return ErrMethodNotAllowed })
				return
			}
		}
	}

	ctx.handlers = append(ctx.handlers, func(ctx *Context) error { return ErrInvalidEndpoint })
}

// Common HTTP methods.
//
// Unless otherwise noted, these are defined in RFC 7231 section 4.3.
const (
	MethodGet     = "GET"
	MethodHead    = "HEAD"
	MethodPost    = "POST"
	MethodPut     = "PUT"
	MethodPatch   = "PATCH" // RFC 5789
	MethodDelete  = "DELETE"
	MethodConnect = "CONNECT"
	MethodOptions = "OPTIONS"
	MethodTrace   = "TRACE"
	MethodQuery   = "QUERY" // RFC 10008
)

// IsValidHttpMethod reports whether method is a supported HTTP method.
// It is case sensitive and expects the canonical all-caps form
// (e.g. "GET", not "get"). See the Method* constants and [App.Map],
// which normalizes its input with [strings.ToUpper] and [strings.TrimSpace]
// before validating.
func IsValidHttpMethod(method string) bool {
	switch method {
	case MethodGet,
		MethodHead,
		MethodPost,
		MethodPut,
		MethodPatch,
		MethodDelete,
		MethodConnect,
		MethodOptions,
		MethodTrace,
		MethodQuery:
		return true
	}
	return false
}

// Map registers one or more handlers for method + pattern.
//
// method is trimmed and uppercased before validation, so "get", "GET" and
// " GET " are equivalent. Prefer the [MethodGet], [MethodPost], ...
// constants, or the [App.MapGet], [App.MapPost], ... shortcuts.
//
// pattern is lowercased before validation, so matching ignores letter case:
// "/API/Users", "/Api/Users" and "/api/users" are the same route.
//
// pattern grammar (see ParsePattern):
//
//	pattern = "/" [ segment { "/" segment } ]
//	segment = literal | ":name" | ":name?" | "*" | "+"
//
// A literal holds letters, digits, "_" , "-" and ".". A ":name" segment
// matches any single non-empty piece and captures it under name.
// A ":name?" segment may be omitted, capturing "". A "*" segment captures
// the rest of the path, slashes included, and may be empty; a "+"
// segment does the same but requires at least one character. A lone "*"
// is read with GetParam("*"), several as GetParam("*1"), GetParam("*2")
// (same for "+"). Every segment holds exactly one part: literals and
// parameters cannot be mixed inside one segment.
//
// Panics on invalid method/pattern or duplicate param names. Registering
// the same method and pattern twice is allowed; the first registration
// wins and later ones never match. Does nothing if no handlers are given.
// Handlers passed in one call run in order via [Context.Next].
//
// Request flow: middlewares registered with [App.Use] before this call
// whose prefix matches the request path run first, in registration order,
// then these handlers. When the path matches but the method is unregistered,
// the app's [ErrorHandler] is invoked with [ErrMethodNotAllowed] after any
// matching middlewares; when nothing matches it is invoked with
// [ErrInvalidEndpoint] after any matching middlewares, so a custom handler
// can inspect or override them.
func (me *App) Map(method string, pattern string, handlers ...Handler) {
	method = strings.ToUpper(strings.TrimSpace(method))
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	Assert(IsValidHttpMethod(method), "invalid http method")
	segments, names, err := ParsePattern(pattern)
	Assert(err == nil, fmt.Sprintf("invalid pattern: %s", err))
	Assert(areNamesUnique(names), "duplicate param names are not allowed")

	if len(handlers) == 0 {
		return
	}

	me.routesPerMethod[method] = append(me.routesPerMethod[method], RouteEntry{
		method:   method,
		pattern:  pattern,
		segments: segments,
		names:    names,
		handlers: handlers,
		order:    int(me.nextRoutingOrder.Add(1)),
	})
}

// MapGet registers handlers for the GET method. See [App.Map].
func (me *App) MapGet(pattern string, handlers ...Handler) {
	me.Map(MethodGet, pattern, handlers...)
}

// MapHead registers handlers for the HEAD method. See [App.Map].
//
// Handlers must not write bytes to the body of a HEAD response.
func (me *App) MapHead(pattern string, handlers ...Handler) {
	me.Map(MethodHead, pattern, handlers...)
}

// MapPost registers handlers for the POST method. See [App.Map].
func (me *App) MapPost(pattern string, handlers ...Handler) {
	me.Map(MethodPost, pattern, handlers...)
}

// MapPut registers handlers for the PUT method. See [App.Map].
func (me *App) MapPut(pattern string, handlers ...Handler) {
	me.Map(MethodPut, pattern, handlers...)
}

// MapPatch registers handlers for the PATCH method. See [App.Map].
func (me *App) MapPatch(pattern string, handlers ...Handler) {
	me.Map(MethodPatch, pattern, handlers...)
}

// MapDelete registers handlers for the DELETE method. See [App.Map].
func (me *App) MapDelete(pattern string, handlers ...Handler) {
	me.Map(MethodDelete, pattern, handlers...)
}

// MapConnect registers handlers for the CONNECT method. See [App.Map].
func (me *App) MapConnect(pattern string, handlers ...Handler) {
	me.Map(MethodConnect, pattern, handlers...)
}

// MapOptions registers handlers for the OPTIONS method. See [App.Map].
func (me *App) MapOptions(pattern string, handlers ...Handler) {
	me.Map(MethodOptions, pattern, handlers...)
}

// MapTrace registers handlers for the TRACE method. See [App.Map].
func (me *App) MapTrace(pattern string, handlers ...Handler) {
	me.Map(MethodTrace, pattern, handlers...)
}

// MapQuery registers handlers for the QUERY method. See [App.Map].
func (me *App) MapQuery(pattern string, handlers ...Handler) {
	me.Map(MethodQuery, pattern, handlers...)
}

// allMethods is every HTTP method registered by [App.MapAll].
var allMethods = []string{
	MethodGet,
	MethodHead,
	MethodPost,
	MethodPut,
	MethodPatch,
	MethodDelete,
	MethodConnect,
	MethodOptions,
	MethodTrace,
	MethodQuery,
}

// MapAll registers handlers for every HTTP method on pattern, calling
// [App.Map] once per method. A request whose method has no dedicated
// registration still reaches these handlers.
//
// Does nothing if no handlers are given. See [App.Map] for the pattern grammar.
func (me *App) MapAll(pattern string, handlers ...Handler) {
	if len(handlers) == 0 {
		return
	}
	for _, method := range allMethods {
		me.Map(method, pattern, handlers...)
	}
}

// Use registers middlewares for prefix.
//
// Matching is prefix matching on whole segments: "/" matches every path,
// "/api" matches "/api" and "/api/users" but not "/apiv2". Prefixes may
// hold ":param", ":param?", "*" and "+" segments like routes; they are
// expanded for the match but their values are not captured.
//
// prefix is lowercased before validation, so matching ignores letter case.
//
// See [App.Map] for the pattern grammar.
//
// Panics on invalid prefix. Does nothing if no middlewares are given.
// Registration order matters: only [App.Map] routes registered after
// this call observe it, in registration order.
//
// Middlewares run first when their prefix matches the request path, even
// when no route matches: the chain is middlewares, then the matched route
// if any, otherwise [ErrMethodNotAllowed] when another method matches the
// path, otherwise [ErrInvalidEndpoint].
//
// Each middleware must call [Context.Next] to continue the chain; a
// returned error from the chain is passed to the app's [ErrorHandler].
// A middleware that writes a response without calling [Context.Next]
// short-circuits the chain, so it can serve requests without a route.
func (me *App) Use(prefix string, handlers ...Handler) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	segments, _, err := ParsePattern(prefix)
	Assert(err == nil, fmt.Sprintf("invalid prefix: %s", err))

	if len(handlers) == 0 {
		return
	}

	me.middlewares = append(me.middlewares, MiddlewareEntry{
		pattern:  prefix,
		segments: segments,
		handlers: handlers,
		order:    int(me.nextRoutingOrder.Add(1)),
	})
}

// UseAll registers middlewares that run for every path.
// It is shorthand for [App.Use] with the "/" prefix.
// See [App.Use] for the middleware chain semantics.
func (me *App) UseAll(handlers ...Handler) {
	me.Use("/", handlers...)
}

// Handler is a middleware or route handler.
//
// Call [Context.Next] to invoke the next handler in the chain and return
// its error (typically `return ctx.Next()`). Return nil to end the chain
// successfully, or a non-nil error to abort and invoke the app
// [ErrorHandler]. Use the [Context] Write methods to buffer a response.
//
// Status and body are buffered until the chain finishes, so headers or
// status set after [Context.Next] returns still apply. Do not mix buffered
// writes with the raw writer from Unwrap in one request; raw use discards
// the buffer.
type Handler func(ctx *Context) error

type partKind int8

const (
	partLiteral partKind = iota
	partParam
	partOptional
	partWildcard
	partPlus
)

type part struct {
	kind partKind
	text string
}

type segment []part

// TODO: support mixed segments holding several parts (e.g. "/api-:name",
// "/files/*.json"). Currently every segment holds exactly one part:
// a literal, ":name", ":name?", "*" or "+" must be the whole segment.
func ParsePattern(pattern string) ([]segment, []string, error) {
	if len(pattern) == 0 || pattern[0] != '/' {
		return nil, nil, fmt.Errorf("route pattern must start with '/'")
	}
	if pattern == "/" {
		// root has no segments.
		return nil, nil, nil
	}
	if pattern[len(pattern)-1] == '/' {
		return nil, nil, fmt.Errorf("route pattern must not end with '/'")
	}

	var segments []segment
	var names []string

	// ignore the leading '/'; it doesn't count as as segment
	for text := range strings.SplitSeq(pattern[1:], "/") {
		if len(text) == 0 {
			return nil, nil, fmt.Errorf("route pattern must not contain empty segments ('//')")
		}

		var seg segment

		switch {
		case text == "*":
			seg = append(seg, part{kind: partWildcard, text: text})
		case text == "+":
			seg = append(seg, part{kind: partPlus, text: text})
		case text[0] == ':':
			body := text[1:]
			if len(body) == 0 {
				return nil, nil, fmt.Errorf("missing parameter name after ':' in segment %q", text)
			}
			optional := false
			if body[len(body)-1] == '?' {
				optional = true
				body = body[:len(body)-1]
				if len(body) == 0 {
					return nil, nil, fmt.Errorf("missing parameter name before '?' in segment %q", text)
				}
			}
			for i := 0; i < len(body); i++ {
				if !isRouteNameChar(body[i]) {
					return nil, nil, fmt.Errorf("invalid character %q in parameter name %q", body[i], body)
				}
			}
			if optional {
				seg = append(seg, part{kind: partOptional, text: body})
			} else {
				seg = append(seg, part{kind: partParam, text: body})
			}
			names = append(names, body)
		default:
			for i := 0; i < len(text); i++ {
				if text[i] == '*' || text[i] == '+' {
					return nil, nil, fmt.Errorf("wildcard %q must be a whole segment", text)
				}
				if !isLiteralChar(text[i]) {
					return nil, nil, fmt.Errorf("invalid character %q in segment %q", text[i], text)
				}
			}
			seg = append(seg, part{kind: partLiteral, text: text})
		}

		segments = append(segments, seg)
	}

	return segments, names, nil
}

func isRouteNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '_' || c == '-'
}

func isLiteralChar(c byte) bool {
	return isRouteNameChar(c) || c == '.'
}

func areNamesUnique(names []string) bool {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			return false
		}
		seen[name] = struct{}{}
	}
	return true
}

// RouteEntry is a handler route (pattern + per-method handlers). See
// [App.Map]. Middleware patterns live in [MiddlewareEntry]; see [App.Use].
type RouteEntry struct {
	// TODO: add route metadata to generate an OpenAPI spec from the
	// registered routes: an id plus documentation fields (summary, tags, ...).
	// The id defaults to empty, identifies the matched route through
	// the context, and is assigned in [App.dispatch].
	method   string
	pattern  string
	segments []segment
	names    []string
	handlers []Handler
	order    int
}

// matchPath returns the captured parameters and true on an exact match,
// nil and false otherwise. The params map is built only on success,
// so failed candidates cost no allocation for it.
func (me RouteEntry) matchPath(path string) (map[string]string, bool) {
	// The path must already be normalized (lowercase, no trailing
	// slash, leading '/'); see [App.registerRootHandler].
	if len(path) == 0 || path[0] != '/' {
		return nil, false
	}

	segs := me.segments
	if len(segs) == 0 {
		if path == "/" {
			return map[string]string{}, true
		}
		return nil, false
	}

	pieces := strings.Split(path[1:], "/")

	wildTotal, plusTotal := 0, 0
	for i := range segs {
		switch segs[i][0].kind {
		case partWildcard:
			wildTotal++
		case partPlus:
			plusTotal++
		}
	}

	type capture struct{ key, value string }
	captures := make([]capture, 0, len(me.names)+wildTotal+plusTotal)

	var match func(segIdx, pathIdx, wildIdx, plusIdx int) bool
	match = func(segIdx, pathIdx, wildIdx, plusIdx int) bool {
		// End: success only if both sides ended together.
		if segIdx == len(segs) {
			return pathIdx == len(pieces)
		}
		p := segs[segIdx][0]
		switch p.kind {
		case partLiteral:
			// Literal: pieces must equal, else dead end.
			return pathIdx < len(pieces) && pieces[pathIdx] == p.text && match(segIdx+1, pathIdx+1, wildIdx, plusIdx)
		case partParam:
			// :id: any non-empty piece works, saved under its name.
			if pathIdx >= len(pieces) || len(pieces[pathIdx]) == 0 {
				return false
			}
			captures = append(captures, capture{p.text, pieces[pathIdx]})
			if match(segIdx+1, pathIdx+1, wildIdx, plusIdx) {
				return true
			}
			captures = captures[:len(captures)-1]
			return false
		case partOptional:
			// :id?: try eating the piece first; if the rest fails,
			// retry as if it ate nothing (saves ""). Path ended
			// means only the empty reading.
			if pathIdx < len(pieces) && len(pieces[pathIdx]) > 0 {
				captures = append(captures, capture{p.text, pieces[pathIdx]})
				if match(segIdx+1, pathIdx+1, wildIdx, plusIdx) {
					return true
				}
				captures = captures[:len(captures)-1]
			}
			captures = append(captures, capture{p.text, ""})
			if match(segIdx+1, pathIdx, wildIdx, plusIdx) {
				return true
			}
			captures = captures[:len(captures)-1]
			return false
		case partWildcard:
			// *: tries every split, longest rest first (/v1/*/shop
			// vs /v1/a/b/shop tries a/b before a). Lone one saved
			// as "*", several as "*1", "*2".
			key := "*"
			if wildTotal > 1 {
				key = "*" + strconv.Itoa(wildIdx+1)
			}
			for k := len(pieces); k >= pathIdx; k-- {
				captures = append(captures, capture{key, strings.Join(pieces[pathIdx:k], "/")})
				if match(segIdx+1, k, wildIdx+1, plusIdx) {
					return true
				}
				captures = captures[:len(captures)-1]
			}
			return false
		case partPlus:
			// +: same as *, but skips the empty rest.
			key := "+"
			if plusTotal > 1 {
				key = "+" + strconv.Itoa(plusIdx+1)
			}
			for k := len(pieces); k > pathIdx; k-- {
				value := strings.Join(pieces[pathIdx:k], "/")
				if len(value) == 0 {
					continue
				}
				captures = append(captures, capture{key, value})
				if match(segIdx+1, k, wildIdx, plusIdx+1) {
					return true
				}
				captures = captures[:len(captures)-1]
			}
			return false
		}
		return false
	}

	if !match(0, 0, 0, 0) {
		return nil, false
	}
	params := make(map[string]string, len(captures))
	for _, c := range captures {
		params[c.key] = c.value
	}
	return params, true
}

type MiddlewareEntry struct {
	pattern  string
	segments []segment
	handlers []Handler
	order    int
}

// matchPath reports whether the entry's prefix covers path: every prefix
// piece must match, leftover path pieces are ignored, so "/api"
// covers "/api/users" but not "/apiv2". Values are not captured.
func (me MiddlewareEntry) matchPath(path string) bool {
	// The path must already be normalized (lowercase, no trailing
	// slash, leading '/'); see [App.registerRootHandler].
	if len(path) == 0 || path[0] != '/' {
		return false
	}

	segs := me.segments
	if len(segs) == 0 {
		return true
	}

	pieces := strings.Split(path[1:], "/")

	var match func(segIdx, pathIdx int) bool
	match = func(segIdx, pathIdx int) bool {
		// End: prefix matched; leftover path is ignored.
		if segIdx == len(segs) {
			return true
		}
		p := segs[segIdx][0]
		switch p.kind {
		case partLiteral:
			// Literal: pieces must equal, else dead end.
			return pathIdx < len(pieces) && pieces[pathIdx] == p.text && match(segIdx+1, pathIdx+1)
		case partParam:
			// :id: any non-empty piece works. Value is not saved;
			// middleware answers yes/no only.
			return pathIdx < len(pieces) && len(pieces[pathIdx]) > 0 && match(segIdx+1, pathIdx+1)
		case partOptional:
			// :id?: try eating the piece first; if the rest fails,
			// retry as if it ate nothing. Path ended means only
			// the empty reading.
			if pathIdx < len(pieces) && len(pieces[pathIdx]) > 0 && match(segIdx+1, pathIdx+1) {
				return true
			}
			return match(segIdx+1, pathIdx)
		case partWildcard:
			// *: tries every split, longest rest first. Anything
			// left after the prefix is ignored.
			for k := len(pieces); k >= pathIdx; k-- {
				if match(segIdx+1, k) {
					return true
				}
			}
			return false
		case partPlus:
			// +: same as *, but skips the empty rest.
			for k := len(pieces); k > pathIdx; k-- {
				if len(strings.Join(pieces[pathIdx:k], "/")) == 0 {
					continue
				}
				if match(segIdx+1, k) {
					return true
				}
			}
			return false
		}
		return false
	}

	return match(0, 0)
}
