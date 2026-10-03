// Package helmet provides a handler that sets security-related response
// headers. Register [Handler.Handle] as a middleware or a route handler:
//
//	app.Use("/*", helmet.New().Handle)
//
// Customize or disable individual headers:
//
//	app.Use("/*", helmet.New(helmet.NewOptions().WithXFrameOptions("DENY").WithHstsMaxAge(31536000)).Handle)
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
// it, passing [NewOptions] chained with the With* methods to configure it,
// then register [Handler.Handle] in the chain.
type Handler struct {
	options Options
}

// Options holds the configuration of a [Handler]. All fields are private;
// build one with [NewOptions] for the defaults and chain the With* methods
// to configure it, then pass it to [New].
type Options struct {
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

// New returns a handler built from the given options, or defaults when none
// are given. Chain the With* methods on [NewOptions] to configure it, then
// register [Handler.Handle] in the chain:
//
//	app.Use("/*", New().Handle)
//	app.Use("/*", New(NewOptions().WithXFrameOptions("DENY")).Handle)
//
// HSTS, Content-Security-Policy and Permissions-Policy are disabled by
// default (see [Options.WithHstsMaxAge],
// [Options.WithContentSecurityPolicy] and [Options.WithPermissionPolicy]).
// Requests for which the [Options.WithSkip] predicate returns true run the
// chain untouched: no headers are set.
func New(opts ...Options) *Handler {
	options := NewOptions()
	if len(opts) > 0 {
		moon.Assert(len(opts) == 1)
		options = opts[0]
	}
	return &Handler{options: options}
}

// NewOptions returns an Options populated with the default values.
// See the With* methods for each default.
func NewOptions() Options {
	return Options{
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
}

// WithSkip skips setting headers for requests where f returns true.
// The chain still runs; only the headers are omitted. It returns the same
// options for chaining.
//
// Default: nil (nothing is skipped)
func (me Options) WithSkip(f func(*moon.Context) bool) Options {
	me.skip = f
	return me
}

// WithXssProtection sets the X-XSS-Protection header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "0"
func (me Options) WithXssProtection(s string) Options {
	me.xssProtection = strings.TrimSpace(s)
	return me
}

// WithContentTypeNoSniff sets the X-Content-Type-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "nosniff"
func (me Options) WithContentTypeNoSniff(s string) Options {
	me.contentTypeNoSniff = strings.TrimSpace(s)
	return me
}

// WithXFrameOptions sets the X-Frame-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "SAMEORIGIN"
func (me Options) WithXFrameOptions(s string) Options {
	me.xFrameOptions = strings.TrimSpace(s)
	return me
}

// WithHstsMaxAge sets the max-age (in seconds) of the
// Strict-Transport-Security header. It panics on a negative value.
// Zero disables the header. It returns the same options for chaining.
//
// Default: 0 (disabled)
func (me Options) WithHstsMaxAge(seconds int) Options {
	moon.Assert(seconds >= 0, "hsts max age cannot be negative")
	me.hstsMaxAge = seconds
	return me
}

// WithHstsIncludeSubdomains controls whether the includeSubDomains directive
// is added to the Strict-Transport-Security header.
// It returns the same options for chaining.
//
// Default: true
func (me Options) WithHstsIncludeSubdomains(b bool) Options {
	me.hstsIncludeSubdomains = b
	return me
}

// WithHstsPreloadEnabled controls whether the preload directive is added to
// the Strict-Transport-Security header.
// It returns the same options for chaining.
//
// Default: false
func (me Options) WithHstsPreloadEnabled(b bool) Options {
	me.hstsPreloadEnabled = b
	return me
}

// WithContentSecurityPolicy sets the Content-Security-Policy header value
// (or the Content-Security-Policy-Report-Only header when
// [Options.WithCspReportOnly] is enabled).
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "" (disabled)
func (me Options) WithContentSecurityPolicy(s string) Options {
	me.contentSecurityPolicy = strings.TrimSpace(s)
	return me
}

// WithCspReportOnly switches the Content-Security-Policy header to
// Content-Security-Policy-Report-Only.
// It returns the same options for chaining.
//
// Default: false
func (me Options) WithCspReportOnly(reportOnly bool) Options {
	me.cspReportOnly = reportOnly
	return me
}

// WithReferrerPolicy sets the Referrer-Policy header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "no-referrer"
func (me Options) WithReferrerPolicy(s string) Options {
	me.referrerPolicy = strings.TrimSpace(s)
	return me
}

// WithPermissionPolicy sets the Permissions-Policy header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "" (disabled)
func (me Options) WithPermissionPolicy(s string) Options {
	me.permissionPolicy = strings.TrimSpace(s)
	return me
}

// WithCrossOriginEmbedderPolicy sets the Cross-Origin-Embedder-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. It returns the same options for chaining.
//
// Default: "require-corp"
func (me Options) WithCrossOriginEmbedderPolicy(s string) Options {
	me.crossOriginEmbedderPolicy = strings.TrimSpace(s)
	return me
}

// WithCrossOriginOpenerPolicy sets the Cross-Origin-Opener-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. It returns the same options for chaining.
//
// Default: "same-origin"
func (me Options) WithCrossOriginOpenerPolicy(s string) Options {
	me.crossOriginOpenerPolicy = strings.TrimSpace(s)
	return me
}

// WithCrossOriginResourcePolicy sets the Cross-Origin-Resource-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. It returns the same options for chaining.
//
// Default: "same-origin"
func (me Options) WithCrossOriginResourcePolicy(s string) Options {
	me.crossOriginResourcePolicy = strings.TrimSpace(s)
	return me
}

// WithOriginAgentCluster sets the Origin-Agent-Cluster header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "?1"
func (me Options) WithOriginAgentCluster(s string) Options {
	me.originAgentCluster = strings.TrimSpace(s)
	return me
}

// WithXDnsPrefetchControl sets the X-DNS-Prefetch-Control header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "off"
func (me Options) WithXDnsPrefetchControl(s string) Options {
	me.xDnsPrefetchControl = strings.TrimSpace(s)
	return me
}

// WithXDownloadOptions sets the X-Download-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "noopen"
func (me Options) WithXDownloadOptions(s string) Options {
	me.xDownloadOptions = strings.TrimSpace(s)
	return me
}

// WithXPermittedCrossDomainPolicies sets the
// X-Permitted-Cross-Domain-Policies header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same options for chaining.
//
// Default: "none"
func (me Options) WithXPermittedCrossDomainPolicies(s string) Options {
	me.xPermittedCrossDomain = strings.TrimSpace(s)
	return me
}

// Handle sets the configured security headers, then runs the chain.
// Headers with empty values are omitted, and Strict-Transport-Security is
// only set when a positive max-age is configured (see
// [Options.WithHstsMaxAge]).
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
