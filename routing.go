package moon

import (
	"net/http"
	"regexp"
	"slices"
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
			ctx.pattern = routes[i].pattern
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

// Map registers one or more handlers for method + pattern.
//
// method is trimmed and uppercased before validation, so "get", "GET" and
// " GET " are equivalent. Prefer the [MethodGet], [MethodPost], ...
// constants, or the [App.MapGet], [App.MapPost], ... shortcuts.
//
// pattern is lowercased before validation, so matching ignores letter case:
// "/API/Users", "/Api/Users" and "/api/users" are the same route.
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
// Request flow: middlewares registered with [App.Use] before this call
// whose pattern matches the request path run first, in registration order,
// then these handlers. When the path matches but the method is unregistered,
// the app's [ErrorHandler] is invoked with [ErrMethodNotAllowed] after any
// matching middlewares; when nothing matches it is invoked with
// [ErrInvalidEndpoint] after any matching middlewares, so a custom handler
// can inspect or override them. [Context.GetPattern] holds the route pattern
// when a route matches, otherwise "".
func (me *App) Map(method string, pattern string, handlers ...Handler) {
	method = strings.ToUpper(strings.TrimSpace(method))
	pattern = strings.ToLower(pattern)
	Assert(IsValidHttpMethod(method), "invalid http method")
	Assert(IsValidRoutePattern(pattern), "invalid route pattern")
	Assert(AreRouteParamNamesUnique(pattern), "duplicate param names are not allowed")
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
		order:    int(me.nextRoutingOrder.Add(1)),
	})
}

var routePatternRegex = regexp.MustCompile(
	`^/(?:(?::[A-Za-z0-9_-]+|[A-Za-z0-9_*-]+)(?:/(?::[A-Za-z0-9_-]+|[A-Za-z0-9_*-]+))*)?$`,
)

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

// allMethods is every HTTP method registered by [App.MapAll] and
// [Prefix.MapAll].
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
// Panics if any method is already registered for pattern. Does nothing if
// no handlers are given. See [App.Map] for the pattern grammar.
func (me *App) MapAll(pattern string, handlers ...Handler) {
	if len(handlers) == 0 {
		return
	}
	for _, method := range allMethods {
		me.Map(method, pattern, handlers...)
	}
}

// Use registers middlewares for pattern.
//
// Matching is exact pattern matching: "/" matches "/" only; use "/*" to
// match every path. Unlike [App.Map], patterns have no ":param" segments.
//
// pattern is lowercased before validation, so matching ignores letter case.
//
// pattern grammar:
//
//	pattern = "/" + (segment ("/" + segment)*)?
//	segment = (letter | digit | "_" | "-" | "*")+
//
// Panics on invalid pattern. Does nothing if no middlewares are given.
// Registration order matters: only [App.Map] routes registered after
// this call observe it, in registration order.
//
// Middlewares run first when their pattern matches the request path, even
// when no route matches: the chain is middlewares, then the matched route
// if any, otherwise [ErrMethodNotAllowed] when another method matches the
// path, otherwise [ErrInvalidEndpoint]. [Context.GetPattern] holds the
// route pattern when a route matches, otherwise "".
//
// Each middleware must call [Context.Next] to continue the chain; a
// returned error from the chain is passed to the app's [ErrorHandler].
// A middleware that writes a response without calling [Context.Next]
// short-circuits the chain, so it can serve requests without a route.
func (me *App) Use(pattern string, handlers ...Handler) {
	pattern = strings.ToLower(pattern)
	Assert(IsValidMiddlewarePattern(pattern), "invalid middleware pattern")

	if len(handlers) == 0 {
		return
	}

	me.middlewares = append(me.middlewares, MiddlewareEntry{
		pattern:  pattern,
		handlers: handlers,
		order:    int(me.nextRoutingOrder.Add(1)),
	})
}

var middlewarePatternRegex = regexp.MustCompile(
	`^/(?:[A-Za-z0-9_*\-]+(?:/[A-Za-z0-9_*\-]+)*)?$`,
)

// UseAll registers middlewares that run for every path.
// It is shorthand for [App.Use] with the catch-all "/*" pattern.
// See [App.Use] for the middleware chain semantics.
func (me *App) UseAll(handlers ...Handler) {
	me.Use("/*", handlers...)
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
	compiledRegex := regexp.MustCompile(regexString)

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
	compiledRegex := regexp.MustCompile(regexString)

	return compiledRegex.MatchString(path)
}

// Prefix is a shared path prefix for routes and middlewares. Create one
// with [App.Prefix] or [Prefix.Prefix]; it registers nothing on its own.
//
// Every method forwards to the matching [App] method with the child
// pattern joined onto the prefix, so prefixes inherit all of [App.Map]
// and [App.Use] semantics: case-insensitive matching, duplicate
// registration detection, param extraction, ordering, and 404/405
// behavior. A prefix is a naming convenience, not a scope: middlewares
// registered through it apply by path and registration order exactly like
// [App.Use], so register them before the routes they should cover.
type Prefix struct {
	pattern string
	app     *App
}

// join returns the effective pattern for registering child under prefix.
//
// It owns the seam between the two patterns, case by case:
//
//  1. A root prefix adds nothing: join("/", "/users") is "/users", so the
//     child stands alone.
//
//  2. A "/" child is the prefix itself, never the prefix plus a trailing
//     slash, because "/api/" is not a valid pattern: join("/api", "/") is
//     "/api". An empty child lands there too by plain concatenation, and
//     at the root there is no prefix to keep, so join("/", "") stays empty
//     for [App.Map] to reject like Map("") would.
//
//  3. A child on a prefix that already ends in "*" is subsumed: that prefix
//     matches everything that could follow it, so the child adds nothing and
//     join("/users/*", "/posts") is "/users/*". This is the general form of
//     the root catch-all, where join("/*", "/*") is "/*" instead of the
//     invalid "//*". Two consequences follow: such a prefix holds one
//     pattern per method, so registering two different children for the same
//     method panics as a duplicate; and a child's ":params" are dropped,
//     since the catch-all already covers what they would have captured.
//
//  4. Otherwise the two concatenate with exactly one slash between them,
//     adding the one that is missing: join("/api", "users") is "/api/users",
//     never the valid-but-wrong "/apiusers" plain concatenation would give.
//
// A prefix ending in "*" always has a real wildcard there: [IsValidRoutePattern]
// forbids "*" inside ":params", so only a wildcard segment can end the
// pattern, and such a segment always matches whatever comes after it.
func join(prefix string, child string) string {
	if child != "" && !strings.HasPrefix(child, "/") {
		child = "/" + child
	}

	switch {
	case prefix == "/":
		return child
	case child == "/" || strings.HasSuffix(prefix, "*"):
		return prefix
	}
	return prefix + child
}

// Prefix returns a [Prefix] rooted at pattern, so nested calls and route
// registrations are prefixed with it.
//
// pattern is lowercased before validation, so it ignores letter case like
// [App.Map]. Panics on invalid pattern or duplicate param names.
//
// The prefix is only validated here; routes and middlewares registered
// through it are validated by [App.Map] and [App.Use] when they are added.
func (me *App) Prefix(pattern string) Prefix {
	pattern = strings.ToLower(pattern)
	// catch early for better debugging.
	Assert(IsValidRoutePattern(pattern), "invalid route pattern")
	Assert(AreRouteParamNamesUnique(pattern), "duplicate param names are not allowed")

	return Prefix{
		pattern: pattern,
		app:     me,
	}
}

// Prefix returns a [Prefix] rooted at this prefix joined with child, so
// prefixes nest. See [App.Prefix]; it panics on the same conditions.
func (me Prefix) Prefix(child string) Prefix {
	return me.app.Prefix(join(me.pattern, child))
}

// Map registers handlers for method + pattern under this prefix, so the
// effective pattern is the prefix joined with pattern. A pattern of "/"
// targets the prefix itself instead of the prefix plus a trailing slash.
//
// A prefix ending in "*" subsumes every child: all of its registrations
// collapse onto the prefix itself, so a second one for the same method
// panics as a duplicate. See [join] for the full set of rules.
//
// See [App.Map] for the method and pattern grammar, the case-insensitive
// matching, and the panic conditions; they all apply to the joined
// pattern.
func (me Prefix) Map(method string, pattern string, handlers ...Handler) {
	me.app.Map(method, join(me.pattern, pattern), handlers...)
}

// MapGet registers handlers for the GET method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapGet(pattern string, handlers ...Handler) {
	me.Map(MethodGet, pattern, handlers...)
}

// MapHead registers handlers for the HEAD method under this prefix.
// See [Prefix.Map].
//
// Handlers must not write bytes to the body of a HEAD response.
func (me Prefix) MapHead(pattern string, handlers ...Handler) {
	me.Map(MethodHead, pattern, handlers...)
}

// MapPost registers handlers for the POST method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapPost(pattern string, handlers ...Handler) {
	me.Map(MethodPost, pattern, handlers...)
}

// MapPut registers handlers for the PUT method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapPut(pattern string, handlers ...Handler) {
	me.Map(MethodPut, pattern, handlers...)
}

// MapPatch registers handlers for the PATCH method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapPatch(pattern string, handlers ...Handler) {
	me.Map(MethodPatch, pattern, handlers...)
}

// MapDelete registers handlers for the DELETE method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapDelete(pattern string, handlers ...Handler) {
	me.Map(MethodDelete, pattern, handlers...)
}

// MapConnect registers handlers for the CONNECT method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapConnect(pattern string, handlers ...Handler) {
	me.Map(MethodConnect, pattern, handlers...)
}

// MapOptions registers handlers for the OPTIONS method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapOptions(pattern string, handlers ...Handler) {
	me.Map(MethodOptions, pattern, handlers...)
}

// MapTrace registers handlers for the TRACE method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapTrace(pattern string, handlers ...Handler) {
	me.Map(MethodTrace, pattern, handlers...)
}

// MapQuery registers handlers for the QUERY method under this prefix.
// See [Prefix.Map].
func (me Prefix) MapQuery(pattern string, handlers ...Handler) {
	me.Map(MethodQuery, pattern, handlers...)
}

// MapAll registers handlers for every HTTP method under this prefix,
// calling [Prefix.Map] once per method. Does nothing if no handlers are
// given. See [App.MapAll].
func (me Prefix) MapAll(pattern string, handlers ...Handler) {
	if len(handlers) == 0 {
		return
	}
	for _, method := range allMethods {
		me.Map(method, pattern, handlers...)
	}
}

// Use registers middlewares for pattern under this prefix, so the
// effective pattern is the prefix joined with pattern. A pattern of "/"
// targets the prefix itself, and [Prefix.UseAll] is shorthand for the
// catch-all "/*" under it.
//
// See [App.Use] for the pattern grammar, the case-insensitive matching,
// and the middleware chain semantics; they all apply to the joined
// pattern. Register middlewares before the routes they should cover.
//
// Panics on the same conditions as [App.Use] once joined; in particular a
// prefix holding ":params" cannot register middlewares at all, because
// middleware patterns have no params.
func (me Prefix) Use(pattern string, handlers ...Handler) {
	me.app.Use(join(me.pattern, pattern), handlers...)
}

// UseAll registers middlewares that run for every path under this prefix.
// It is shorthand for [Prefix.Use] with the catch-all "/*" pattern.
// See [App.Use] for the middleware chain semantics.
func (me Prefix) UseAll(handlers ...Handler) {
	me.Use("/*", handlers...)
}
