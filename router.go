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
		// cut trailing forward slashes except the root "/"
		if r.URL.Path != "/" {
			r.URL.Path = strings.TrimRight(r.URL.Path, "/")
		}

		var middlewares []Handler

		for _, route := range me.routes {
			if route.isMiddlewarePrefix && strings.HasPrefix(r.URL.Path, route.pattern) {
				middlewares = append(middlewares, route.middlewares...)
				continue
			}

			if params, ok := route.matchPath(r.URL.Path); ok {
				if handlers, ok := route.methodHandlers[r.Method]; ok {
					me.processRequest(w, r, route.pattern, params, append(middlewares, handlers...))
				} else {
					me.processRequest(w, r, "", nil, []Handler{func(ctx *Context) error { return ErrMethodNotAllowed }})
				}
				return
			}
		}

		if len(middlewares) > 0 {
			me.processRequest(w, r, "", nil, middlewares)
			return
		}

		me.processRequest(w, r, "", nil, []Handler{func(ctx *Context) error { return ErrInvalidEndpoint }})
	})

	me.httpServer.Handler = mux
}

// Handle registers one or more handlers for method + pattern.
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
// or if method is already registered
// for pattern. Does nothing if no handlers are given. Handlers passed
// in one call run in order via [Context.Next].
//
// Middlewares registered with [App.Use] before this call whose prefix
// matches the request path run before handlers. A path match with an
// unregistered method invokes the app's [ErrorHandler] with
// [ErrMethodNotAllowed]; no match invokes it with [ErrInvalidEndpoint],
// so a custom handler can inspect or override them. Both carry an empty
// [Context.GetPattern] since no route pattern matched.
func (me *App) Handle(method string, pattern string, handlers ...Handler) {
	Assert(isValidHttpMethod(method), "invalid http method")
	Assert(isValidRoutePattern(pattern), "invalid route pattern")
	Assert(areParamNamesUnique(pattern), "duplicate param names are not allowed")

	if len(handlers) == 0 {
		return
	}

	index := slices.IndexFunc(me.routes, func(r Route) bool { return r.pattern == pattern })
	if index == -1 {
		me.routes = append(me.routes, Route{
			pattern:        pattern,
			methodHandlers: make(map[string][]Handler, 9),
		})
		index = len(me.routes) - 1
	}

	Assert(me.routes[index].methodHandlers[method] == nil, "method already registered for this pattern")
	me.routes[index].methodHandlers[method] = append(me.routes[index].methodHandlers[method], handlers...)
}

func areParamNamesUnique(pattern string) bool {
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

// Get registers handlers for the GET method. See [App.Handle].
func (me *App) Get(pattern string, handlers ...Handler) {
	me.Handle(http.MethodGet, pattern, handlers...)
}

// Head registers handlers for the HEAD method. See [App.Handle].
//
// Handlers must not write bytes to the body of a HEAD response.
func (me *App) Head(pattern string, handlers ...Handler) {
	me.Handle(http.MethodHead, pattern, handlers...)
}

// Post registers handlers for the POST method. See [App.Handle].
func (me *App) Post(pattern string, handlers ...Handler) {
	me.Handle(http.MethodPost, pattern, handlers...)
}

// Put registers handlers for the PUT method. See [App.Handle].
func (me *App) Put(pattern string, handlers ...Handler) {
	me.Handle(http.MethodPut, pattern, handlers...)
}

// Patch registers handlers for the PATCH method. See [App.Handle].
func (me *App) Patch(pattern string, handlers ...Handler) {
	me.Handle(http.MethodPatch, pattern, handlers...)
}

// Delete registers handlers for the DELETE method. See [App.Handle].
func (me *App) Delete(pattern string, handlers ...Handler) {
	me.Handle(http.MethodDelete, pattern, handlers...)
}

// Connect registers handlers for the CONNECT method. See [App.Handle].
func (me *App) Connect(pattern string, handlers ...Handler) {
	me.Handle(http.MethodConnect, pattern, handlers...)
}

// Options registers handlers for the OPTIONS method. See [App.Handle].
func (me *App) Options(pattern string, handlers ...Handler) {
	me.Handle(http.MethodOptions, pattern, handlers...)
}

// Trace registers handlers for the TRACE method. See [App.Handle].
func (me *App) Trace(pattern string, handlers ...Handler) {
	me.Handle(http.MethodTrace, pattern, handlers...)
}

// All registers handlers for every HTTP method on pattern, calling
// [App.Handle] once per method. A request whose method has no dedicated
// registration still reaches these handlers.
//
// Panics if any method is already registered for pattern. Does nothing if
// no handlers are given. See [App.Handle] for the pattern grammar.
func (me *App) All(pattern string, handlers ...Handler) {
	if len(handlers) == 0 {
		return
	}
	for _, method := range []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodOptions,
		http.MethodTrace,
	} {
		me.Handle(method, pattern, handlers...)
	}
}

// Use registers middlewares for a raw string prefix.
//
// Matching is [strings.HasPrefix], so "/api" matches "/api", "/api/...", and "/api2/...".
//
// prefix grammar:
//
//	prefix = "/" + (segment ("/" + segment)*)?
//	segment = (letter | digit | "_" | "-")+
//
// Panics on invalid prefix. Does nothing if no middlewares are given.
// Registration order matters: only [App.Handle] routes registered after
// this call observe it, in registration order. If no route matches but a
// prefix does, the collected middlewares still run with an empty
// [Context.GetPattern]: the pattern is only set for routes registered
// by [App.Handle].
//
// Each middleware must call [Context.Next] to continue the chain; a
// returned error from the chain is passed to the app's [ErrorHandler].
func (me *App) Use(prefix string, middlewares ...Handler) {
	// TODO: enable `*` within the prefix, but now after it.
	// this allows maching paths like /x/y/z/... using /x/*/z
	Assert(isValidMiddlewareRoutePrefix(prefix), "invalid middleware route prefix")

	if len(middlewares) == 0 {
		return
	}

	me.routes = append(me.routes, Route{
		isMiddlewarePrefix: true,
		pattern:            prefix,
		middlewares:        middlewares,
	})
}

var middlewareRoutePrefixRegex = regexp.MustCompile(
	`^/(?:[A-Za-z0-9_\-]+(?:/[A-Za-z0-9_\-]+)*)?$`,
)

func isValidMiddlewareRoutePrefix(prefix string) bool {
	return middlewareRoutePrefixRegex.MatchString(prefix)
}

var routePatternRegex = regexp.MustCompile(
	`^/(?:(?::[A-Za-z0-9_-]+|[A-Za-z0-9_*-]+)(?:/(?::[A-Za-z0-9_-]+|[A-Za-z0-9_*-]+))*)?$`,
)

func isValidRoutePattern(pattern string) bool {
	return routePatternRegex.MatchString(pattern)
}

func (me *App) processRequest(
	w http.ResponseWriter,
	r *http.Request,
	pattern string,
	params map[string]string,
	handlers []Handler,
) {
	ctx := newContext(w, r, pattern, params, handlers, me)
	setRequestHandlingStartTimeLocal(ctx)

	// first handler/middleware that will execute all handlers
	err := ctx.Next()
	if err != nil {
		me.errorHandler(ctx, err)
		setRequestHandlingErrorLocal(ctx, err)
	}
	ctx.response.flush()

	if me.enableRequestLogging {
		me.logRequest(ctx)
	}
}

func isValidHttpMethod(method string) bool {
	switch method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodOptions,
		http.MethodTrace:
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
// Responses are buffered until the chain finishes, so headers or status set
// after [Context.Next] returns still apply. Do not mix buffered writes with
// the raw writer from Unwrap in one request; raw use discards the buffer.
type Handler func(ctx *Context) error

// Route is a handler route (pattern + per-method handlers) or a
// middleware prefix entry. See [App.Handle] and [App.Use].
type Route struct {
	// TODO: add support for route metadata to generate apenapi spec
	// TODO: use a route per method: struct Route {pattern, method, handlers}
	// middlewares don't have a pattern or a method.
	// OR use a separate `Prefix` type, but think about its metadata support.
	// TODO: find a way to distinguish between a handler registered as middleware [App.Use] or as a route [App.Handle]
	// this enables a new context mehtod `Context.IsMiddleware()`
	pattern            string
	methodHandlers     map[string][]Handler
	isMiddlewarePrefix bool
	middlewares        []Handler
}

var routeParamRegex = regexp.MustCompile(`:([a-zA-Z0-9_\-]+)`)
var routeWildcardRegex = regexp.MustCompile(`\*`)

func (me Route) matchPath(path string) (map[string]string, bool) {
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
	regexString = routeWildcardRegex.ReplaceAllString(regexString, `.*`)

	// Ensure strict matching from start to end of the string
	regexString = "^" + regexString + "$"

	// Compile final regex
	compiledRegex, err := regexp.Compile(regexString)
	if err != nil {
		return nil, false
	}

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
