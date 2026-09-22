package moon

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// TODO: add static files support (maybe as a middleware)
// TODO: add route grouping

func (me *App) registerRootHandler() {
	mux := http.NewServeMux()

	// NOTE: we cannot log/error-handle invalid requests here.
	// this is expected as the error handler and the logger are for handled requests.
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
					w.WriteHeader(ErrMethodNotAllowed.StatusCode)
					json.NewEncoder(w).Encode(ErrMethodNotAllowed)
				}
				return
			}
		}

		if len(middlewares) > 0 {
			me.processRequest(w, r, r.URL.Path, nil, middlewares)
			return
		}

		w.WriteHeader(ErrInvalidEndpoint.StatusCode)
		json.NewEncoder(w).Encode(ErrInvalidEndpoint)
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
// unregistered method responds 405; no match responds 404.
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
// prefix does, the collected middlewares still run.
//
// Each middleware must call [Context.Next] to continue the chain; a
// returned error from the chain is passed to the app's [ErrorHandler].
func (me *App) Use(prefix string, middlewares ...Handler) {
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
	ctx := newContext(
		w,
		r,
		pattern,
		params,
		handlers,
		&me.state,
		me.dependencies,
		me.startedServices,
		me.passLocalsToContext,
	)
	start := time.Now()

	// first handler/middleware that will execute all handlers
	err := ctx.Next()
	if err != nil {
		me.errorHandler(ctx, err)
	}

	if me.enableRequestLogging {
		me.logger.Info(
			"request handled",
			"took", time.Since(start),
			"client", ctx.GetRemoteAddress(),
			"method", ctx.GetMethod(),
			"path", ctx.GetPath(),
			"status", ctx.GetStatusCode(),
			"error", err,
		)
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
// [ErrorHandler]. Use the [Context] Write methods to send a response.
type Handler func(ctx *Context) error

// TODO: add support for route metadata to generate apenapi spec

// Route is a handler route (pattern + per-method handlers) or a
// middleware prefix entry. See [App.Handle] and [App.Use].
type Route struct {
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
