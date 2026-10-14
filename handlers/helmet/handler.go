// Package helmet provides a handler that sets security-related response
// headers. Register [Handler.Handle] as a middleware or a route handler:
//
//	app.UseAll(helmet.New().Handle)
//
// Customize or disable individual headers:
//
//	app.UseAll(helmet.New(helmet.Option_XFrameOptions("DENY"), helmet.Option_HstsMaxAge(31536000)).Handle)
//
// As a middleware it runs before the route is resolved, so the headers are
// set even for unmatched requests ([moon.ErrInvalidEndpoint],
// [moon.ErrMethodNotAllowed]).
package helmet

import (
	"fmt"
	"strings"

	"github.com/assaidy/moon"
)

// Handler sets security-related response headers. Use [New] to construct
// it, passing [Option]s to configure it,
// then register [Handler.Handle] in the chain.
type Handler struct {
	options options
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	skip                      func(*moon.Context) bool
	xssProtection             string
	contentTypeNoSniff        string
	xFrameOptions             string
	hstsMaxAge                int
	hstsIncludeSubdomains     bool
	contentSecurityPolicy     string
	cspReportOnly             bool
	hstsPreloadEnabled        bool
	referrerPolicy            string
	permissionPolicy          string
	crossOriginEmbedderPolicy string
	crossOriginOpenerPolicy   string
	crossOriginResourcePolicy string
	originAgentCluster        string
	xDnsPrefetchControl       string
	xDownloadOptions          string
	xPermittedCrossDomain     string
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register [Handler.Handle] in the chain:
//
//	app.UseAll(helmet.New().Handle)
//	app.UseAll(helmet.New(helmet.Option_XFrameOptions("DENY")).Handle)
//
// HSTS, Content-Security-Policy and Permissions-Policy are disabled by
// default (see [Option_HstsMaxAge],
// [Option_ContentSecurityPolicy] and [Option_PermissionPolicy]).
// Requests for which the [Option_Skip] predicate returns true run the
// chain untouched: no headers are set.
func New(opts ...Option) *Handler {
	options := options{
		xssProtection:             "0",
		contentTypeNoSniff:        "nosniff",
		xFrameOptions:             "SAMEORIGIN",
		referrerPolicy:            "no-referrer",
		crossOriginEmbedderPolicy: "require-corp",
		crossOriginOpenerPolicy:   "same-origin",
		crossOriginResourcePolicy: "same-origin",
		originAgentCluster:        "?1",
		xDnsPrefetchControl:       "off",
		xDownloadOptions:          "noopen",
		xPermittedCrossDomain:     "none",
		hstsIncludeSubdomains:     true,
	}
	for _, opt := range opts {
		opt(&options)
	}
	return &Handler{options: options}
}

// Option configures a [Handler]. Pass them to [New]:
//
//	app.UseAll(helmet.New(helmet.Option_XFrameOptions("DENY")).Handle)
type Option func(*options)

// Option_Skip skips setting headers for requests where f returns true.
// The chain still runs; only the headers are omitted. Pass it to [New].
//
// Default: nil (nothing is skipped)
func Option_Skip(f func(*moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// Option_XssProtection sets the X-XSS-Protection header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "0"
func Option_XssProtection(s string) Option {
	return func(o *options) {
		o.xssProtection = strings.TrimSpace(s)
	}
}

// Option_ContentTypeNoSniff sets the X-Content-Type-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "nosniff"
func Option_ContentTypeNoSniff(s string) Option {
	return func(o *options) {
		o.contentTypeNoSniff = strings.TrimSpace(s)
	}
}

// Option_XFrameOptions sets the X-Frame-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "SAMEORIGIN"
func Option_XFrameOptions(s string) Option {
	return func(o *options) {
		o.xFrameOptions = strings.TrimSpace(s)
	}
}

// Option_HstsMaxAge sets the max-age (in seconds) of the
// Strict-Transport-Security header. It panics on a negative value.
// Zero disables the header. Pass it to [New].
//
// Default: 0 (disabled)
func Option_HstsMaxAge(seconds int) Option {
	return func(o *options) {
		moon.Assert(seconds >= 0, "hsts max age cannot be negative")
		o.hstsMaxAge = seconds
	}
}

// Option_HstsIncludeSubdomains controls whether the includeSubDomains directive
// is added to the Strict-Transport-Security header.
// Pass it to [New].
//
// Default: true
func Option_HstsIncludeSubdomains(b bool) Option {
	return func(o *options) {
		o.hstsIncludeSubdomains = b
	}
}

// Option_HstsPreloadEnabled controls whether the preload directive is added to
// the Strict-Transport-Security header.
// Pass it to [New].
//
// Default: false
func Option_HstsPreloadEnabled(b bool) Option {
	return func(o *options) {
		o.hstsPreloadEnabled = b
	}
}

// Option_ContentSecurityPolicy sets the Content-Security-Policy header value
// (or the Content-Security-Policy-Report-Only header when
// [Option_CspReportOnly] is enabled).
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "" (disabled)
func Option_ContentSecurityPolicy(s string) Option {
	return func(o *options) {
		o.contentSecurityPolicy = strings.TrimSpace(s)
	}
}

// Option_CspReportOnly switches the Content-Security-Policy header to
// Content-Security-Policy-Report-Only.
// Pass it to [New].
//
// Default: false
func Option_CspReportOnly(reportOnly bool) Option {
	return func(o *options) {
		o.cspReportOnly = reportOnly
	}
}

// Option_ReferrerPolicy sets the Referrer-Policy header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "no-referrer"
func Option_ReferrerPolicy(s string) Option {
	return func(o *options) {
		o.referrerPolicy = strings.TrimSpace(s)
	}
}

// Option_PermissionPolicy sets the Permissions-Policy header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "" (disabled)
func Option_PermissionPolicy(s string) Option {
	return func(o *options) {
		o.permissionPolicy = strings.TrimSpace(s)
	}
}

// Option_CrossOriginEmbedderPolicy sets the Cross-Origin-Embedder-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. Pass it to [New].
//
// Default: "require-corp"
func Option_CrossOriginEmbedderPolicy(s string) Option {
	return func(o *options) {
		o.crossOriginEmbedderPolicy = strings.TrimSpace(s)
	}
}

// Option_CrossOriginOpenerPolicy sets the Cross-Origin-Opener-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. Pass it to [New].
//
// Default: "same-origin"
func Option_CrossOriginOpenerPolicy(s string) Option {
	return func(o *options) {
		o.crossOriginOpenerPolicy = strings.TrimSpace(s)
	}
}

// Option_CrossOriginResourcePolicy sets the Cross-Origin-Resource-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. Pass it to [New].
//
// Default: "same-origin"
func Option_CrossOriginResourcePolicy(s string) Option {
	return func(o *options) {
		o.crossOriginResourcePolicy = strings.TrimSpace(s)
	}
}

// Option_OriginAgentCluster sets the Origin-Agent-Cluster header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "?1"
func Option_OriginAgentCluster(s string) Option {
	return func(o *options) {
		o.originAgentCluster = strings.TrimSpace(s)
	}
}

// Option_XDnsPrefetchControl sets the X-DNS-Prefetch-Control header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "off"
func Option_XDnsPrefetchControl(s string) Option {
	return func(o *options) {
		o.xDnsPrefetchControl = strings.TrimSpace(s)
	}
}

// Option_XDownloadOptions sets the X-Download-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "noopen"
func Option_XDownloadOptions(s string) Option {
	return func(o *options) {
		o.xDownloadOptions = strings.TrimSpace(s)
	}
}

// Option_XPermittedCrossDomainPolicies sets the
// X-Permitted-Cross-Domain-Policies header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// Pass it to [New].
//
// Default: "none"
func Option_XPermittedCrossDomainPolicies(s string) Option {
	return func(o *options) {
		o.xPermittedCrossDomain = strings.TrimSpace(s)
	}
}

// Handle sets the configured security headers, then runs the chain.
// Headers with empty values are omitted, and Strict-Transport-Security is
// only set when a positive max-age is configured (see
// [Option_HstsMaxAge]).
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	if me.options.xssProtection != "" {
		ctx.SetHeader("X-XSS-Protection", me.options.xssProtection)
	}

	if me.options.contentTypeNoSniff != "" {
		ctx.SetHeader("X-Content-Type-Options", me.options.contentTypeNoSniff)
	}

	if me.options.xFrameOptions != "" {
		ctx.SetHeader("X-Frame-Options", me.options.xFrameOptions)
	}

	if me.options.hstsMaxAge > 0 {
		header := "Strict-Transport-Security"
		ctx.AddHeader(header, fmt.Sprintf("max-age=%d", me.options.hstsMaxAge))
		if me.options.hstsIncludeSubdomains {
			ctx.AddHeader(header, "includeSubDomains")
		}
		if me.options.hstsPreloadEnabled {
			ctx.AddHeader(header, "preload")
		}
	}

	if me.options.contentSecurityPolicy != "" {
		if me.options.cspReportOnly {
			ctx.SetHeader("Content-Security-Policy-Report-Only", me.options.contentSecurityPolicy)
		} else {
			ctx.SetHeader("Content-Security-Policy", me.options.contentSecurityPolicy)
		}
	}

	if me.options.referrerPolicy != "" {
		ctx.SetHeader("Referrer-Policy", me.options.referrerPolicy)
	}

	if me.options.permissionPolicy != "" {
		ctx.SetHeader("Permissions-Policy", me.options.permissionPolicy)
	}

	if me.options.crossOriginEmbedderPolicy != "" {
		ctx.SetHeader("Cross-Origin-Embedder-Policy", me.options.crossOriginEmbedderPolicy)
	}

	if me.options.crossOriginOpenerPolicy != "" {
		ctx.SetHeader("Cross-Origin-Opener-Policy", me.options.crossOriginOpenerPolicy)
	}

	if me.options.crossOriginResourcePolicy != "" {
		ctx.SetHeader("Cross-Origin-Resource-Policy", me.options.crossOriginResourcePolicy)
	}

	if me.options.originAgentCluster != "" {
		ctx.SetHeader("Origin-Agent-Cluster", me.options.originAgentCluster)
	}

	if me.options.xDnsPrefetchControl != "" {
		ctx.SetHeader("X-DNS-Prefetch-Control", me.options.xDnsPrefetchControl)
	}

	if me.options.xDownloadOptions != "" {
		ctx.SetHeader("X-Download-Options", me.options.xDownloadOptions)
	}

	if me.options.xPermittedCrossDomain != "" {
		ctx.SetHeader("X-Permitted-Cross-Domain-Policies", me.options.xPermittedCrossDomain)
	}

	return ctx.Next()
}
