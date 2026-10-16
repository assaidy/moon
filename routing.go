package moon

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

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

	ctx.handlers = append(ctx.handlers, func(ctx *Context) error { return ErrNotFound })
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
// See [ParsePattern] for the pattern grammar.
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
// [ErrNotFound] after any matching middlewares, so a custom handler
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
// See [ParsePattern] for the pattern grammar.
//
// Panics on invalid prefix. Does nothing if no middlewares are given.
// Registration order matters: only [App.Map] routes registered after
// this call observe it, in registration order.
//
// Middlewares run first when their prefix matches the request path, even
// when no route matches: the chain is middlewares, then the matched route
// if any, otherwise [ErrMethodNotAllowed] when another method matches the
// path, otherwise [ErrNotFound].
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
		prefix:   prefix,
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
// its error (typically `return ctx.Next()`). A call with no handlers left
// returns [ErrNotFound]. Return nil to end the chain
// successfully, or a non-nil error to abort and invoke the app
// [ErrorHandler]. Use the [Context] Write methods to buffer a response.
//
// Status and body are buffered until the chain finishes, so headers or
// status set after [Context.Next] returns still apply. Do not mix buffered
// writes with the raw writer from Unwrap in one request; raw use discards
// the buffer.
type Handler func(ctx *Context) error

// PartKind identifies how a [Part] matches. See [ParsePattern].
type PartKind int8

const (
	// PartLiteral matches its static text. [Part.Text] holds the literal.
	PartLiteral PartKind = iota
	// PartParam matches any non-empty piece up to the next '/' and captures
	// it. It takes the whole [Segment]; [Part.Text] holds the name.
	PartParam
	// PartOptional is like [PartParam] for ":name?", but may be omitted and
	// capture "". [Part.Text] holds the name.
	PartOptional
	// PartWildcard matches "*" inside a [Segment]: any span, possibly empty
	// and spanning slashes, up to the next part. [Part.Text] is "*".
	PartWildcard
	// PartPlus matches "+" like [PartWildcard], but the span must not be
	// empty. [Part.Text] is "+".
	PartPlus
)

// Part is one unit inside a [Segment], as returned by [ParsePattern].
//
// A [Segment] holding only [PartParam] or [PartOptional] is a parameter
// segment. Otherwise non-literal parts are separated by [PartLiteral]s.
type Part struct {
	Kind PartKind
	// Text is the literal for [PartLiteral], the name for [PartParam] and
	// [PartOptional], and "*" or "+" for [PartWildcard] and [PartPlus].
	Text string
}

// Segment is one "/"-separated piece of a pattern: a sequence of [Part]s.
// The root "/" has no segments. See [ParsePattern] for the grammar.
type Segment []Part

// ParsePattern cuts pattern into segments of parts, collecting ":name"
// names in order for [App.Map] to check.
//
// Grammar:
//
//	pattern = "/" [ segment { "/" segment } ]
//	segment = ":" name [ "?" ] | { literal | "*" | "+" }
//	name    = literal { literal }
//
// A ":name" segment takes the whole segment: it matches any single
// non-empty piece and captures it under name. ":name?" may be omitted,
// capturing "". A name holds the same chars as a literal (letters,
// digits, "_", "-" and "."). A ":" always starts a parameter, so a
// literal colon must be escaped ("\:").
// A literal holds letters, digits, "_", "-" and ".". A trailing "*"
// captures the rest of the path, slashes included, and may be empty;
// a trailing "+" does the same but requires at least one character.
// Inside a segment they capture up to the next part instead. A lone "*"
// is read with GetParam("*"), several as GetParam("*1"), GetParam("*2")
// (same for "+"). A '\' escapes the next char ("\:", "\*", "\+", "\\").
func ParsePattern(pattern string) ([]Segment, []string, error) {
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

	var segments []Segment
	var names []string

	// ignore the leading '/'; it doesn't count as a segment
	for piece := range strings.SplitSeq(pattern[1:], "/") {
		if len(piece) == 0 {
			return nil, nil, fmt.Errorf("route pattern must not contain empty segments ('//')")
		}
		var seg Segment
		idx := 0
		for idx < len(piece) {
			switch piece[idx] {
			case '*':
				if len(seg) != 0 && seg[len(seg)-1].Kind != PartLiteral {
					return nil, nil, fmt.Errorf("non-literal parts must be separated by a literal in segment %q", piece)
				}
				seg = append(seg, Part{PartWildcard, "*"})
				idx++
			case '+':
				if len(seg) != 0 && seg[len(seg)-1].Kind != PartLiteral {
					return nil, nil, fmt.Errorf("non-literal parts must be separated by a literal in segment %q", piece)
				}
				seg = append(seg, Part{PartPlus, "+"})
				idx++
			case '\\':
				idx++ // consume the '\'
				if idx == len(piece) {
					return nil, nil, fmt.Errorf("dangling escape '\\' at end of segment %q", piece)
				}
				switch piece[idx] {
				case '*':
					seg = append(seg, Part{PartLiteral, "*"})
				case '+':
					seg = append(seg, Part{PartLiteral, "+"})
				case ':':
					seg = append(seg, Part{PartLiteral, ":"})
				case '\\':
					seg = append(seg, Part{PartLiteral, "\\"})
				default:
					return nil, nil, fmt.Errorf("invalid escape '\\%c' in segment %q; only '\\\\', '\\:', '\\*' and '\\+' can be escaped", piece[idx], piece)
				}
				idx++ // consume the escaped char
			case ':':
				if len(seg) != 0 {
					return nil, nil, fmt.Errorf("parameter in segment %q must take the whole segment", piece)
				}
				idx++ // consume the ':'
				nameStart := idx
				for idx < len(piece) && isLiteralChar(piece[idx]) {
					idx++
				}
				if nameStart == idx { // must have a name
					if idx < len(piece) && piece[idx] == '?' {
						return nil, nil, fmt.Errorf("missing parameter name before '?' in segment %q", piece)
					}
					return nil, nil, fmt.Errorf("missing parameter name after ':' in segment %q", piece)
				}
				name := piece[nameStart:idx]
				names = append(names, name)
				if idx < len(piece) && piece[idx] == '?' {
					seg = append(seg, Part{PartOptional, name})
					idx++
				} else {
					seg = append(seg, Part{PartParam, name})
				}
				if idx != len(piece) {
					return nil, nil, fmt.Errorf("parameter in segment %q must take the whole segment", piece)
				}
			default:
				literalStart := idx
				for idx < len(piece) && isLiteralChar(piece[idx]) {
					idx++
				}
				if literalStart == idx {
					// there must be a char here. if not, it means we encountered a non-literal char,
					// and it's not a special char as we already check them above.
					return nil, nil, fmt.Errorf("invalid character %q in segment %q", piece[idx], piece)
				}
				seg = append(seg, Part{PartLiteral, piece[literalStart:idx]})
			}
		}

		segments = append(segments, seg)
	}

	return segments, names, nil
}

func isLiteralChar(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.'
}

// pieceEnd returns the end of the path piece starting at pos:
// the next '/' or the end of the path.
func pieceEnd(path string, pos int) int {
	if i := strings.IndexByte(path[pos:], '/'); i >= 0 {
		return pos + i
	}
	return len(path)
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
	// The id defaults to empty and identifies the matched route.
	method   string
	pattern  string
	segments []Segment
	names    []string
	handlers []Handler
	order    int
}

// matchPath returns the captured parameters and true on an exact match,
// nil and false otherwise. The params map is built only on success.
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

	wildTotal, plusTotal := 0, 0
	for i := range segs {
		for j := range segs[i] {
			switch segs[i][j].Kind {
			case PartWildcard:
				wildTotal++
			case PartPlus:
				plusTotal++
			}
		}
	}

	type capture struct{ key, value string }
	captures := make([]capture, 0, len(me.names)+wildTotal+plusTotal)

	var match func(segIdx, partIdx, pos, wildcIdx, plusIdx int) bool
	match = func(segIdx, partIdx, pos, wildcIdx, plusIdx int) bool {
		seg := segs[segIdx]
		if partIdx == len(seg) {
			// Segment done: the next one starts at a piece start.
			// Consume the '/' if present; it may already be gone
			// (optional skip, empty wildcard), in which case pos
			// already follows one.
			if segIdx+1 == len(segs) {
				return pos == len(path)
			}
			if pos < len(path) {
				if path[pos] == '/' {
					pos++
				} else if path[pos-1] != '/' {
					return false
				}
			}
			return match(segIdx+1, 0, pos, wildcIdx, plusIdx)
		}
		p := seg[partIdx]
		switch p.Kind {
		case PartLiteral:
			// Literal: the path must continue with it, else dead end.
			if !strings.HasPrefix(path[pos:], p.Text) {
				return false
			}
			return match(segIdx, partIdx+1, pos+len(p.Text), wildcIdx, plusIdx)
		case PartParam:
			// :id: any non-empty run up to the next '/' works,
			// saved under its name. A param owns its segment.
			end := pieceEnd(path, pos)
			if end == pos {
				return false
			}
			captures = append(captures, capture{p.Text, path[pos:end]})
			if match(segIdx, partIdx+1, end, wildcIdx, plusIdx) {
				return true
			}
			captures = captures[:len(captures)-1]
			return false
		case PartOptional:
			// :id?: try eating first; if the rest fails, retry
			// as if it ate nothing (saves "").
			end := pieceEnd(path, pos)
			if end > pos {
				captures = append(captures, capture{p.Text, path[pos:end]})
				if match(segIdx, partIdx+1, end, wildcIdx, plusIdx) {
					return true
				}
				captures = captures[:len(captures)-1]
			}
			captures = append(captures, capture{p.Text, ""})
			if match(segIdx, partIdx+1, pos, wildcIdx, plusIdx) {
				return true
			}
			captures = captures[:len(captures)-1]
			return false
		case PartWildcard:
			// *: greedy spans, longest first; may be empty and may
			// span slashes. Lone one saved as "*", several as
			// "*1", "*2".
			//
			// Spans ending in '/' are tried last, so separators stay
			// out of captures unless forced. match is backtracking:
			// the first success wins, so try order is preference.
			// E.g. pattern "/*/c" against "/a/b/c": both "a/b/" and
			// "a/b" let the trailing "c" match (the segment boundary
			// tolerates pos already past '/'), but the non-slash pass
			// wins first, so "*" is "a/b". Only when nothing else
			// matches (e.g. "//" in the path forces it) is a trailing
			// '/' kept.
			key := "*"
			if wildTotal > 1 {
				key = "*" + strconv.Itoa(wildcIdx+1)
			}
			for _, slashEnding := range [2]bool{false, true} {
				for end := len(path); end >= pos; end-- {
					slashed := end > pos && path[end-1] == '/'
					if slashed != slashEnding {
						continue
					}
					captures = append(captures, capture{key, path[pos:end]})
					if match(segIdx, partIdx+1, end, wildcIdx+1, plusIdx) {
						return true
					}
					captures = captures[:len(captures)-1]
				}
			}
			return false
		case PartPlus:
			// +: same as *, but the span must not be empty. Slash-ending
			// spans are tried last for the same reason: keep the
			// separator out of the capture unless forced.
			key := "+"
			if plusTotal > 1 {
				key = "+" + strconv.Itoa(plusIdx+1)
			}
			for _, slashEnding := range [2]bool{false, true} {
				for end := len(path); end > pos; end-- {
					if (path[end-1] == '/') != slashEnding {
						continue
					}
					captures = append(captures, capture{key, path[pos:end]})
					if match(segIdx, partIdx+1, end, wildcIdx, plusIdx+1) {
						return true
					}
					captures = captures[:len(captures)-1]
				}
			}
			return false
		}
		return false
	}

	// Start position at 1 to skip the leading '/'; Root doesn't have a segment.
	if !match(0, 0, 1, 0, 0) {
		return nil, false
	}
	params := make(map[string]string, len(captures))
	for _, c := range captures {
		params[c.key] = c.value
	}
	return params, true
}

type MiddlewareEntry struct {
	prefix   string
	segments []Segment
	handlers []Handler
	order    int
}

// matchPath reports whether the entry's prefix covers path: every prefix
// part must match, leftover path is ignored, so "/api"
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

	var match func(segIdx, partIdx, pos int) bool
	match = func(segIdx, partIdx, pos int) bool {
		seg := segs[segIdx]
		if partIdx == len(seg) {
			// Segment done: the next one starts at a piece start.
			// Consume the '/' if present; it may already be gone
			// (optional skip, empty wildcard), in which case pos
			// already follows one.
			// Leftover path after the last one is ignored.
			if segIdx+1 == len(segs) {
				// Prefix done: the current piece must be fully
				// eaten; anything after it is ignored.
				return pos == pieceEnd(path, pos)
			}
			if pos < len(path) {
				if path[pos] == '/' {
					pos++
				} else if path[pos-1] != '/' {
					return false
				}
			}
			return match(segIdx+1, 0, pos)
		}
		p := seg[partIdx]
		switch p.Kind {
		case PartLiteral:
			// Literal: the path must continue with it, else dead end.
			if !strings.HasPrefix(path[pos:], p.Text) {
				return false
			}
			return match(segIdx, partIdx+1, pos+len(p.Text))
		case PartParam:
			// :id: any non-empty run up to the next '/' works. Value
			// is not saved; middleware answers yes/no only.
			end := pieceEnd(path, pos)
			if end == pos {
				return false
			}
			return match(segIdx, partIdx+1, end)
		case PartOptional:
			// :id?: try eating first; if the rest fails, retry
			// as if it ate nothing.
			end := pieceEnd(path, pos)
			if end > pos && match(segIdx, partIdx+1, end) {
				return true
			}
			return match(segIdx, partIdx+1, pos)
		case PartWildcard:
			// *: greedy spans, longest first; anything left after
			// the prefix is ignored. No capture is kept, and match
			// is yes/no only, so unlike routes there is no separate
			// slash-ending pass to keep separators out of a capture:
			// any success wins.
			for end := len(path); end >= pos; end-- {
				if match(segIdx, partIdx+1, end) {
					return true
				}
			}
			return false
		case PartPlus:
			// +: same as *, but the span must not be empty. No
			// slash-ending pass for the same reason as *: no capture.
			for end := len(path); end > pos; end-- {
				if match(segIdx, partIdx+1, end) {
					return true
				}
			}
			return false
		}
		return false
	}

	// Start position at 1 to skip the leading '/'; Root doesn't have a segment.
	return match(0, 0, 1)
}
