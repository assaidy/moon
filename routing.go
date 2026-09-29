package moon

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// TODO: add static files support (maybe as a middleware)
// TODO: add route grouping

func (me *App) registerRootHandler() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// trim trailing forward slashes except the root "/"
		if r.URL.Path != "/" {
			r.URL.Path = strings.TrimRight(r.URL.Path, "/")
		}

		ctx := me.newContext(w, r)
		me.dispatch(ctx)

		setRequestHandlingStartTimeLocal(ctx)
		if err := ctx.Next(); err != nil {
			me.errorHandler(ctx, err)
			setRequestHandlingErrorLocal(ctx, err)
		}
		ctx.response.flush()

		if me.enableRequestLogging {
			me.logRequest(ctx)
		}

		ctx.requestBodyBuffer.Reset()
		bodyBufferPool.Put(ctx.requestBodyBuffer)
		ctx.response.bodyBuffer.Reset()
		bodyBufferPool.Put(ctx.response.bodyBuffer)
	})

	me.httpServer.Handler = mux
}

func (me *App) dispatch(ctx *Context) {
	for _, route := range me.routesPerMethod[ctx.GetMethod()] {
		if params, ok := route.matchPath(ctx.GetPath()); ok {
			ctx.pattern = route.pattern
			ctx.params = params
			ctx.handlers = []Handler{func(ctx *Context) error {
				// Bodies declaring more than the read limit are rejected without reading
				// them. Unknown sizes are enforced while reading instead.
				if ctx.request.ContentLength > int64(me.readLimit) {
					return ErrRequestEntityTooLarge
				}
				return ctx.Next()
			}}
			for _, middleware := range me.middlewares {
				if middleware.order >= route.order {
					continue
				}
				if middleware.matchPath(ctx.GetPath()) {
					ctx.handlers = append(ctx.handlers, middleware.handlers...)
				}
			}
			ctx.middlewareCount = len(ctx.handlers)
			ctx.handlers = append(ctx.handlers, route.handlers...)
			return
		}
	}

	for method, routes := range me.routesPerMethod {
		if method == ctx.GetMethod() {
			continue
		}
		for _, route := range routes {
			if _, ok := route.matchPath(ctx.GetPath()); ok {
				ctx.handlers = []Handler{func(ctx *Context) error { return ErrMethodNotAllowed }}
				return
			}
		}
	}

	ctx.handlers = []Handler{func(ctx *Context) error { return ErrInvalidEndpoint }}
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

// Map registers one or more handlers for method + pattern.
//
// method is case sensitive and must be all-caps (e.g. "GET", not "get").
// Prefer the [MethodGet], [MethodPost], ... constants, or the [App.MapGet],
// [App.MapPost], ... shortcuts.
//
// pattern grammar:
//
//	route            = "/" + (segment ("/" + segment)*)?
//	segment          = param_segment | wildcard_segment
//	param_segment    = ":" + name
//	wildcard_segment = (name | "*")+
//	name             = (letter | digit | "_" | "-")+
//
// Panics on invalid method/pattern, duplicate param names,
// or if method is already registered for pattern.
// Does nothing if no handlers are given. Handlers passed
// in one call run in order via [Context.Next].
//
// Middlewares registered with [App.Use] before this call whose pattern
// matches the request path run before handlers. A path match with an
// unregistered method invokes the app's [ErrorHandler] with
// [ErrMethodNotAllowed]; no match invokes it with [ErrInvalidEndpoint],
// so a custom handler can inspect or override them. Both carry an empty
// [Context.GetPattern] since no route pattern matched, and no middlewares
// run without a route match.
func (me *App) Map(method string, pattern string, handlers ...Handler) {
	Assert(isValidHttpMethod(method), "invalid http method")
	Assert(isValidRoutePattern(pattern), "invalid route pattern")
	Assert(areRouteParamNamesUnique(pattern), "duplicate param names are not allowed")
	Assert(
		slices.IndexFunc(me.routesPerMethod[method], func(r RouteEntry) bool { return r.method == method && r.pattern == pattern }) == -1,
		"method with this pattern already registered",
	)

	if len(handlers) == 0 {
		return
	}

	me.routesPerMethod[method] = append(me.routesPerMethod[method], RouteEntry{
		method:   method,
		pattern:  pattern,
		handlers: handlers,
		order:    int(me.nextOrder.Add(1)),
	})
}

var routePatternRegex = regexp.MustCompile(
	`^/(?:(?::[A-Za-z0-9_-]+|[A-Za-z0-9_*-]+)(?:/(?::[A-Za-z0-9_-]+|[A-Za-z0-9_*-]+))*)?$`,
)

func isValidRoutePattern(pattern string) bool {
	return routePatternRegex.MatchString(pattern)
}

func areRouteParamNamesUnique(pattern string) bool {
	paramNames := routeParamRegex.FindAllStringSubmatch(pattern, -1)
	seen := make(map[string]struct{}, len(paramNames))
	for _, match := range paramNames {
		if _, ok := seen[match[1]]; ok {
			return false
		}
		seen[match[1]] = struct{}{}
	}
	return true
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

// MapAll registers handlers for every HTTP method on pattern, calling
// [App.Map] once per method. A request whose method has no dedicated
// registration still reaches these handlers.
//
// Panics if any method is already registered for pattern. Does nothing if
// no handlers are given. See [App.Map] for the pattern grammar.
func (me *App) MapAll(pattern string, handlers ...Handler) {
	if len(handlers) == 0 {
		return
	}
	for _, method := range []string{
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
	} {
		me.Map(method, pattern, handlers...)
	}
}

// Use registers middlewares for pattern.
//
// Matching is exact pattern matching: "/" matches "/" only; use "/*" to
// match every path. Unlike [App.Map], patterns have no ":param" segments.
//
// pattern grammar:
//
//	pattern = "/" + (segment ("/" + segment)*)?
//	segment = (letter | digit | "_" | "-" | "*")+
//
// Panics on invalid pattern. Does nothing if no middlewares are given.
// Registration order matters: only [App.Map] routes registered after
// this call observe it, in registration order. Middlewares only run when
// a route matches; they add behaviour to existing endpoints and never
// serve as endpoints on their own.
//
// Each middleware must call [Context.Next] to continue the chain; a
// returned error from the chain is passed to the app's [ErrorHandler].
func (me *App) Use(pattern string, handlers ...Handler) {
	Assert(isValidMiddlewarePattern(pattern), "invalid middleware pattern")

	if len(handlers) == 0 {
		return
	}

	me.middlewares = append(me.middlewares, MiddlewareEntry{
		pattern:  pattern,
		handlers: handlers,
		order:    int(me.nextOrder.Add(1)),
	})
}

var middlewarePatternRegex = regexp.MustCompile(
	`^/(?:[A-Za-z0-9_*\-]+(?:/[A-Za-z0-9_*\-]+)*)?$`,
)

func isValidMiddlewarePattern(pattern string) bool {
	return middlewarePatternRegex.MatchString(pattern)
}

func isValidHttpMethod(method string) bool {
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

// RouteEntry is a handler route (pattern + per-method handlers). See
// [App.Map]. Middleware patterns live in [MiddlewareEntry]; see [App.Use].
type RouteEntry struct {
	// TODO: add support for route metadata to generate apenapi spec.
	method   string
	pattern  string
	handlers []Handler
	order    int
	// TODO: Add route id field. default to empty.
	// it's used to identify a route inside the context `Context.routeId`
	// assign it insde [App.dispatch]
}

var routeParamRegex = regexp.MustCompile(`:([a-zA-Z0-9_\-]+)`)
var patternWildcardRegex = regexp.MustCompile(`\*`)

func (me RouteEntry) matchPath(path string) (map[string]string, bool) {
	// Collect param names in order so we can use unnamed groups.
	// Unnamed groups avoid RE2 restrictions on group names (e.g. hyphens
	// in ":version-id" are valid param names but invalid group names).
	paramMatches := routeParamRegex.FindAllStringSubmatch(me.pattern, -1)
	paramNames := make([]string, 0, len(paramMatches))
	for _, match := range paramMatches {
		paramNames = append(paramNames, match[1])
	}

	// Convert standard named placeholders (e.g., ":id") into unnamed groups.
	// [^/]+ ensures it stops matching at the next forward slash.
	regexString := routeParamRegex.ReplaceAllString(me.pattern, `([^/]+)`)

	// Convert "*" into a non-capturing, greedy wildcard match (.*)
	regexString = patternWildcardRegex.ReplaceAllString(regexString, `.*`)

	// Ensure strict matching from start to end of the string
	regexString = "^" + regexString + "$"

	// Compile final regex
	compiledRegex, err := regexp.Compile(regexString)
	Assert(err == nil, "must be nil as we validated the pattern before registeration")

	// Execute the match against the target string
	matches := compiledRegex.FindStringSubmatch(path)
	if matches == nil {
		return nil, false
	}

	// matches[0] is the full match, matches[1:] map to paramNames in order.
	// Wildcards use non-capturing .* so they don't shift indices.
	if len(matches)-1 != len(paramNames) {
		return nil, false
	}
	params := make(map[string]string, len(paramNames))
	for i, name := range paramNames {
		params[name] = matches[i+1]
	}

	return params, true
}

type MiddlewareEntry struct {
	pattern  string
	handlers []Handler
	order    int
}

func (me MiddlewareEntry) matchPath(path string) bool {
	// Convert "*" into a non-capturing, greedy wildcard match (.*)
	regexString := patternWildcardRegex.ReplaceAllString(me.pattern, `.*`)

	// Ensure strict matching from start to end of the string
	regexString = "^" + regexString + "$"

	// Compile final regex
	compiledRegex, err := regexp.Compile(regexString)
	Assert(err == nil, "must be nil as we validated the pattern before registeration")

	return compiledRegex.MatchString(path)
}
