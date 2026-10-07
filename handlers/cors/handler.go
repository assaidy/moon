// Package cors provides a handler that implements the CORS (Cross-Origin
// Resource Sharing) protocol, letting a server declare which foreign origins
// may read its responses and which cross-origin requests the browser is
// allowed to send to it. Register [Handler.Handle] as a middleware:
//
//	app.UseAll(cors.New().Handle)
//
// By default every origin is allowed with "*", the methods HEAD, GET, POST,
// PUT, PATCH, DELETE and QUERY are allowed, no custom headers are exposed
// and no preflight max age is set. Restrict that with the With* methods:
//
//	app.UseAll(cors.New(cors.NewOptions().
//		WithAllowedOrigins([]string{"https://example.com", "https://www.example.com"}).
//		WithAllowedHeaders([]string{"Content-Type", "Authorization"}).
//		WithAllowCredentials(true),
//	).Handle)
//
// Allowed origins may be written exactly ("https://app.example.com"), as a
// subdomain wildcard ("https://*.example.com", which allows every subdomain
// at any depth but not the apex), or as "*" for every origin.
//
// The handler answers the two halves of the protocol:
//
//   - A preflight request (an OPTIONS request carrying Origin and
//     Access-Control-Request-Method) is answered directly with 204 No
//     Content and the Access-Control-Allow-* headers; the chain is not run.
//   - Any other request that carries an Origin header gets
//     Access-Control-Allow-Origin before the chain runs, along with the
//     credentials and exposed-headers headers when configured.
//
// Requests without an Origin header are outside the scope of CORS, and
// requests whose origin is not allowed get no CORS headers at all. Only Vary
// is set for them, unless every origin is allowed, so shared caches key the
// response on the request origin
// (https://fetch.spec.whatwg.org/#cors-protocol-and-http-caches).
//
// References: the protocol is specified at
// https://fetch.spec.whatwg.org/#cors-protocol and documented on MDN at
// https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/CORS.
package cors

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/assaidy/moon"
)

// Handler sets CORS response headers. Use [New] to construct it, passing
// [NewOptions] chained with the With* methods to configure it, then register
// [Handler.Handle] in the chain.
type Handler struct {
	options Options
}

// New returns a handler built from the given options, or defaults when none
// are given. Chain the With* methods on [NewOptions] to configure it, then
// register [Handler.Handle] in the chain:
//
//	app.UseAll(New().Handle)
//	app.UseAll(New(NewOptions().WithAllowedOrigins([]string{"https://example.com"})).Handle)
//
// It panics when credentials are allowed while the allowed origins contain
// the wildcard "*": a wildcard matches every origin, so pairing it with
// Access-Control-Allow-Credentials would let any site make credentialed
// requests to the server, which is a security risk. List the origins
// explicitly with [Options.WithAllowedOrigins] instead.
func New(options ...Options) *Handler {
	opts := NewOptions()
	if len(options) > 0 {
		moon.Assert(len(options) == 1)
		opts = options[0]
	}

	moon.Assert(
		!(opts.allowCredentials && slices.Contains(opts.allowedOrogins, "*")),
		"credentials cannot be allowed when allowed origins contain a wildcard '*'",
	)

	return &Handler{options: opts}
}

// Options holds the configuration of a [Handler]. All fields are private;
// build one with [NewOptions] for the defaults and chain the With* methods
// to configure it, then pass it to [New].
type Options struct {
	skip                 func(ctx *moon.Context) bool
	allowedOrogins       []string
	allowedOriginsFunc   func(origin string) bool
	allowedMethods       []string
	allowedMethodsString string
	allowedHeaders       []string
	allowedHeadersString string
	exposedHeaders       []string
	exposedHeadersString string
	allowCredentials     bool
	allowPrivateNetwork  bool
	maxAge               int
}

// NewOptions returns an Options populated with the default values: every
// origin allowed with "*", the methods HEAD, GET, POST, PUT, PATCH, DELETE
// and QUERY allowed, and no allowed headers, exposed headers, credentials,
// private network access or preflight max age.
// See the With* methods for each default.
func NewOptions() Options {
	methods := []string{
		moon.MethodHead,
		moon.MethodGet,
		moon.MethodPost,
		moon.MethodPut,
		moon.MethodPatch,
		moon.MethodDelete,
		moon.MethodQuery,
	}

	return Options{
		allowedOrogins:       []string{"*"},
		allowedMethods:       methods,
		allowedMethodsString: strings.Join(methods, ", "),
		maxAge:               -1,
	}
}

// WithSkip skips CORS handling for requests where f returns true: the chain
// still runs, but no CORS headers are set. It returns the same options for
// chaining.
//
// Default: nil (nothing is skipped)
func (me Options) WithSkip(f func(ctx *moon.Context) bool) Options {
	me.skip = f
	return me
}

// normalizeAllowedOrigin validates an entry of
// [Options.WithAllowedOrigins]: entries carrying a wildcard go through
// [normalizeOriginPattern], plain origins through [normalizeOrigin].
func normalizeAllowedOrigin(raw string) (string, bool) {
	if strings.Contains(strings.TrimSpace(raw), "*") {
		return normalizeOriginPattern(raw)
	}
	return normalizeOrigin(raw)
}

// normalizeOriginPattern validates a configured origin that may carry a
// subdomain wildcard, "https://*.example.com", and returns it in canonical
// form. The wildcard must be the whole first label of the host: exactly one
// "*" followed by a dot. Entries without a wildcard are handled by
// [normalizeOrigin].
//
// The domain after the wildcard must itself be a plain host, so a pattern
// with empty labels such as "https://*..example.com" is rejected, like
// "*.example.com" (no scheme), "https://*example.com" (star glued to the
// host), "https://example.*" (star in place of the TLD) and "https://*"
// (no domain).
func normalizeOriginPattern(raw string) (string, bool) {
	origin := strings.TrimSpace(raw)
	if !strings.Contains(origin, "*") {
		return normalizeOrigin(origin)
	}
	if strings.Count(origin, "*") != 1 {
		return "", false
	}
	scheme, hostPort, ok := strings.Cut(origin, "://")
	if !ok || scheme == "" || !strings.HasPrefix(hostPort, "*.") {
		return "", false
	}

	// the domain carries the wildcard rules, so validate it as a plain
	// origin and graft the wildcard back on
	normalized, ok := normalizeOrigin(scheme + "://" + hostPort[len("*."):])
	if !ok {
		return "", false
	}
	normalizedScheme, normalizedHost, _ := strings.Cut(normalized, "://")
	if strings.HasPrefix(normalizedHost, ".") ||
		strings.HasSuffix(normalizedHost, ".") ||
		strings.Contains(normalizedHost, "..") {
		return "", false
	}
	return normalizedScheme + "://*." + normalizedHost, true
}

// originMatches reports whether the normalized request origin matches an
// entry of the allowed origins: an exact match, or, for a subdomain wildcard
// entry such as "https://*.example.com", the same scheme and suffix with at
// least one non-empty label in between, so the wildcard never matches the
// apex origin nor a look-alike host.
func originMatches(normalized string, allowed []string) bool {
	for _, entry := range allowed {
		if !strings.Contains(entry, "*") {
			if entry == normalized {
				return true
			}
			continue
		}
		if matchSubdomain(normalized, entry) {
			return true
		}
	}
	return false
}

// matchSubdomain reports whether the normalized origin sits under the
// wildcard entry. The entry is split around the "*" into a prefix
// ("https://") and a suffix (".example.com", the dot being the label
// separator), so only the labels in between are free: "app" and "api.v2"
// match, while "" and "." do not.
func matchSubdomain(normalized, entry string) bool {
	prefix, suffix, _ := strings.Cut(entry, "*")
	if len(normalized) < len(prefix)+len(suffix) ||
		!strings.HasPrefix(normalized, prefix) ||
		!strings.HasSuffix(normalized, suffix) {
		return false
	}
	sub := normalized[len(prefix) : len(normalized)-len(suffix)]
	return sub != "" &&
		!strings.HasPrefix(sub, ".") &&
		!strings.HasSuffix(sub, ".") &&
		!strings.Contains(sub, "..")
}

// WithAllowedOrigins sets the origins that may read responses. It returns
// the same options for chaining.
//
// Each entry is trimmed and normalized to its canonical form (lowercase
// scheme and host, no path, no default port), so origins may be written in
// any case with or without a trailing slash. The special literal "null" is
// accepted verbatim: browsers send it for privacy-sensitive contexts such as
// sandboxed iframes and file:// pages (see
// https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Origin).
//
// An entry may be a subdomain wildcard, where "*" is the whole first label
// of the host: "https://*.example.com" allows every subdomain of
// example.com at any depth (https://app.example.com,
// https://api.v2.example.com) but not the apex https://example.com itself.
// Wildcards may be combined with other origins, and unlike the all-origins
// wildcard they may be paired with [Options.WithAllowCredentials], since
// they only ever match your own subdomains.
//
// A wildcard is only accepted as a whole first label:
// "https://*example.com", "https://example.*" and "https://*" are not
// subdomain patterns and are rejected.
//
// The wildcard "*" allows every origin, may not be combined with other
// origins, and cannot be paired with [Options.WithAllowCredentials]: every
// site could then make credentialed requests to the server, which is a
// security risk.
//
// It panics on an entry that is not a valid origin or pattern.
//
// Default: []string{"*"}
func (me Options) WithAllowedOrigins(origins []string) Options {
	for i, o := range origins {
		o = strings.TrimSpace(o)
		moon.Assert(!(o == "*" && len(origins) > 1), "wildcard '*' cannot be combined with other origins")
		if o != "*" {
			normalized, ok := normalizeAllowedOrigin(o)
			moon.Assert(ok, fmt.Sprintf("invalid origin %q at index %d", o, i))
			o = normalized
		}
		origins[i] = o
	}
	me.allowedOrogins = origins
	return me
}

// WithAllowedOriginsFunc sets a function that decides whether the request
// origin is allowed, based on application logic. It receives the raw value
// of the Origin request header and returns true to allow it, and it is only
// consulted when the origin does not match the list given to
// [Options.WithAllowedOrigins]; when it allows the origin, that origin is
// echoed back in Access-Control-Allow-Origin. It returns the same options
// for chaining.
//
// Warning: never let the function return true for every origin. This is
// particularly crucial when [Options.WithAllowCredentials] is set to true:
// doing so bypasses the restriction of a wildcard origin with credentials,
// exposing your application to serious security threats. If you need to
// allow every origin, use the wildcard "*" in [Options.WithAllowedOrigins]
// instead of a function.
//
// Default: nil (only the static list decides)
func (me Options) WithAllowedOriginsFunc(f func(origin string) bool) Options {
	me.allowedOriginsFunc = f
	return me
}

// WithAllowedMethods sets the methods listed in
// Access-Control-Allow-Methods on preflight responses. Surrounding
// whitespace is trimmed and each method is uppercased; it panics on a value
// that is not a valid HTTP method. It returns the same options for
// chaining.
//
// Default: HEAD, GET, POST, PUT, PATCH, DELETE and QUERY
func (me Options) WithAllowedMethods(methods []string) Options {
	for i, m := range methods {
		m = strings.ToUpper(strings.TrimSpace(m))
		moon.Assert(moon.IsValidHttpMethod(m))
		methods[i] = m
	}
	me.allowedMethods = methods
	me.allowedMethodsString = strings.Join(methods, ", ")
	return me
}

// WithAllowedHeaders sets the headers listed in Access-Control-Allow-Headers
// on preflight responses: the browser only sends the listed request headers
// in the actual request. Surrounding whitespace is trimmed; it panics on an
// empty value. It returns the same options for chaining.
//
// Default: none
func (me Options) WithAllowedHeaders(headers []string) Options {
	for i, h := range headers {
		h = strings.TrimSpace(h)
		moon.Assert(h != "", fmt.Sprintf("empty header value at index %d", i))
		headers[i] = h
	}
	me.allowedHeaders = headers
	me.allowedHeadersString = strings.Join(headers, ", ")
	return me
}

// WithExposedHeaders sets the headers listed in
// Access-Control-Expose-Headers: by default scripts on another origin may
// only read a short list of response headers, and this widens it. Surrounding
// whitespace is trimmed; it panics on an empty value. It returns the same
// options for chaining.
//
// Default: none
func (me Options) WithExposedHeaders(headers []string) Options {
	for i, h := range headers {
		h = strings.TrimSpace(h)
		moon.Assert(h != "", fmt.Sprintf("empty header value at index %d", i))
		headers[i] = h
	}
	me.exposedHeaders = headers
	me.exposedHeadersString = strings.Join(headers, ", ")
	return me
}

// WithAllowCredentials switches Access-Control-Allow-Credentials on, letting
// the browser attach cookies and other credentials to cross-origin requests
// and read credentialed responses. It returns the same options for
// chaining.
//
// It cannot be combined with the wildcard origin "*", which [New] asserts:
// a wildcard matches every origin, so it would let any site make
// credentialed requests to the server. Credentials are only safe with
// origins listed explicitly in [Options.WithAllowedOrigins].
//
// Default: false
func (me Options) WithAllowCredentials(b bool) Options {
	me.allowCredentials = b
	return me
}

// WithAllowPrivateNetwork answers preflight requests that ask for private
// network access with Access-Control-Allow-Private-Network: true. It returns
// the same options for chaining.
//
// Default: false
func (me Options) WithAllowPrivateNetwork(b bool) Options {
	me.allowPrivateNetwork = b
	return me
}

// WithMaxAge sets Access-Control-Max-Age on preflight responses, the number
// of seconds the browser may cache the preflight result. Zero and positive
// values are written, a negative value omits the header. It returns the
// same options for chaining.
//
// Default: -1 (the header is omitted)
func (me Options) WithMaxAge(i int) Options {
	me.maxAge = i
	return me
}

// normalizeOrigin validates an origin and returns it in canonical form so
// registration-time entries and request origins compare byte for byte.
//
// An origin is "scheme://host[:port]" (or the literal "null"). Normalization
// lowercases the scheme and the host, drops a trailing slash and drops the
// default port for http and https. It reports false for anything else: a
// wildcard (including subdomain patterns such as "https://*.example.com",
// which are not supported), a bare host, a userinfo, a path, a query or a
// fragment.
func normalizeOrigin(raw string) (string, bool) {
	origin := strings.TrimSpace(raw)
	if origin == "" {
		return "", false
	}
	if origin == "null" {
		return "null", true
	}
	if strings.Contains(origin, "*") {
		return "", false
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return "", false
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", false
	}

	scheme := strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	if strings.Contains(hostname, ":") { // IPv6 literal, url.URL.Hostname drops the brackets
		hostname = "[" + hostname + "]"
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		hostname += ":" + port
	}
	return scheme + "://" + hostname, true
}

const (
	// Request Headers (Sent by the browser)
	headerOrigin                             = "Origin"
	headerAccessControlRequestMethod         = "Access-Control-Request-Method"
	headerAccessControlRequestHeaders        = "Access-Control-Request-Headers"
	headerAccessControlRequestPrivateNetwork = "Access-Control-Request-Private-Network"

	// Response Headers
	headerAccessControlAllowOrigin         = "Access-Control-Allow-Origin"
	headerAccessControlAllowMethods        = "Access-Control-Allow-Methods"
	headerAccessControlAllowHeaders        = "Access-Control-Allow-Headers"
	headerAccessControlAllowCredentials    = "Access-Control-Allow-Credentials"
	headerAccessControlExposeHeaders       = "Access-Control-Expose-Headers"
	headerAccessControlMaxAge              = "Access-Control-Max-Age"
	headerAccessControlAllowPrivateNetwork = "Access-Control-Allow-Private-Network"

	// Vary header is critical to prevent caching issues with dynamic origins
	headerVary = "Vary"
)

// Handle sets the CORS response headers, then runs the chain. A preflight
// request is answered with 204 No Content and the chain is not run; any
// other request is served by the chain after the headers are set. Requests
// skipped by [Options.WithSkip] run the chain untouched.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	if isPreflightRequest(ctx) {
		me.setResponseHeadersForPreflightRequest(ctx)
		ctx.SetStatusCode(http.StatusNoContent)
		return nil
	}

	me.setResponseHeadersForGeneralRequest(ctx)
	return ctx.Next()
}

// isPreflightRequest reports whether the request is the browser's preflight
// probe: an OPTIONS request carrying Origin and
// Access-Control-Request-Method. Access-Control-Request-Headers is not
// required, since the browser only sends it when the actual request carries
// custom headers.
//
// See https://fetch.spec.whatwg.org/#preflight-request
func isPreflightRequest(ctx *moon.Context) bool {
	return ctx.GetMethod() == moon.MethodOptions &&
		ctx.GetHeader(headerOrigin) != "" &&
		ctx.GetHeader(headerAccessControlRequestMethod) != ""
}

// setResponseHeadersForPreflightRequest sets the headers that let the
// browser send the actual request: the allowed origin, the allowed methods
// and headers, the cache lifetime of this decision and, when asked for,
// private network access. Vary covers every request header the answer
// depends on.
//
// The preflight response must carry Access-Control-Allow-Origin like any
// other CORS response, otherwise the browser rejects it with a network
// error: https://fetch.spec.whatwg.org/#cors-preflight-fetch
func (me *Handler) setResponseHeadersForPreflightRequest(ctx *moon.Context) {
	origin := me.getAllowedOrigin(ctx)
	if origin != "" {
		ctx.SetHeader(headerAccessControlAllowOrigin, origin)
		if me.options.allowCredentials {
			ctx.SetHeader(headerAccessControlAllowCredentials, "true")
		}
	}
	if len(me.options.allowedMethods) > 0 {
		ctx.SetHeader(headerAccessControlAllowMethods, me.options.allowedMethodsString)
	}
	if len(me.options.allowedHeaders) > 0 {
		ctx.SetHeader(headerAccessControlAllowHeaders, me.options.allowedHeadersString)
	}
	if me.options.maxAge >= 0 {
		ctx.SetHeader(headerAccessControlMaxAge, fmt.Sprint(me.options.maxAge))
	}
	if me.options.allowPrivateNetwork && ctx.GetHeader(headerAccessControlRequestPrivateNetwork) == "true" {
		ctx.SetHeader(headerAccessControlAllowPrivateNetwork, "true")
	}
	ctx.SetHeader(headerVary, preflightVary)
}

var preflightVary = strings.Join(
	[]string{headerOrigin, headerAccessControlRequestMethod, headerAccessControlRequestHeaders},
	", ",
)

// setResponseHeadersForGeneralRequest answers the actual (non-preflight)
// request: it resolves the allowed origin, then sets the CORS headers the
// browser needs to hand the response to the calling script.
func (me *Handler) setResponseHeadersForGeneralRequest(ctx *moon.Context) {
	origin := me.getAllowedOrigin(ctx)
	if origin == "" {
		// No Origin header means the request is outside the scope of CORS,
		// and a disallowed origin gets no CORS headers at all. Caches must
		// still key the response on the origin unless every origin is
		// allowed: https://fetch.spec.whatwg.org/#cors-protocol-and-http-caches
		if !slices.Contains(me.options.allowedOrogins, "*") {
			ctx.SetHeader(headerVary, headerOrigin)
		}
		return
	}
	ctx.SetHeader(headerAccessControlAllowOrigin, origin)
	if me.options.allowCredentials {
		ctx.SetHeader(headerAccessControlAllowCredentials, "true")
	}
	if len(me.options.exposedHeaders) > 0 {
		ctx.SetHeader(headerAccessControlExposeHeaders, me.options.exposedHeadersString)
	}
	if origin != "*" {
		ctx.SetHeader(headerVary, headerOrigin)
	}
}

// getAllowedOrigin resolves the value for Access-Control-Allow-Origin: the
// wildcard "*" when any origin is allowed, the request origin when it is
// allowed, and "" when the request is outside the scope of CORS or its
// origin is not allowed, in which case no CORS headers are set.
//
// The static list at [Options.WithAllowedOrigins] is consulted first, and
// [Options.WithAllowedOriginsFunc] only runs when the list does not match.
func (me *Handler) getAllowedOrigin(ctx *moon.Context) string {
	origin := ctx.GetHeader(headerOrigin)
	if origin == "" {
		return ""
	}
	if slices.Contains(me.options.allowedOrogins, "*") {
		return "*"
	}
	normalized, ok := normalizeOrigin(origin)
	if !ok {
		return ""
	}
	if originMatches(normalized, me.options.allowedOrogins) {
		return normalized
	}
	if me.options.allowedOriginsFunc != nil && me.options.allowedOriginsFunc(origin) {
		return origin
	}
	return ""
}
