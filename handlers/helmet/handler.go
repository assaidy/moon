// Package helmet provides a handler that sets security-related response
// headers. Register [Handler.Handle] as a middleware or a route handler:
//
//	app.Use("/*", helmet.New().Handle)
//
// Customize or disable individual headers:
//
//	app.Use("/*", helmet.New().WithXFrameOptions("DENY").WithHstsMaxAge(31536000).Handle)
package helmet

import (
	"fmt"
	"strings"

	"github.com/assaidy/moon"
)

// Handler sets security-related response headers. Use [New] to construct
// it with defaults, chain the With* methods to configure it, then register
// [Handler.Handle] in the chain.
type Handler struct {
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

// New returns a handler with default options. Chain the With* methods to
// configure it, then register [Handler.Handle] in the chain:
//
//	app.Use("/*", New().Handle)
//	app.Use("/*", New().WithXFrameOptions("DENY").Handle)
//
// HSTS, Content-Security-Policy and Permissions-Policy are disabled by
// default (see [Handler.WithHstsMaxAge],
// [Handler.WithContentSecurityPolicy] and [Handler.WithPermissionPolicy]).
// Requests for which the [Handler.WithSkip] predicate returns true run the
// chain untouched: no headers are set.
func New() *Handler {
	return &Handler{
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
// handler for chaining.
//
// Default: nil (nothing is skipped)
func (me *Handler) WithSkip(f func(*moon.Context) bool) *Handler {
	me.skip = f
	return me
}

// WithXssProtection sets the X-XSS-Protection header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "0"
func (me *Handler) WithXssProtection(s string) *Handler {
	me.xssProtection = strings.TrimSpace(s)
	return me
}

// WithContentTypeNoSniff sets the X-Content-Type-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "nosniff"
func (me *Handler) WithContentTypeNoSniff(s string) *Handler {
	me.contentTypeNoSniff = strings.TrimSpace(s)
	return me
}

// WithXFrameOptions sets the X-Frame-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "SAMEORIGIN"
func (me *Handler) WithXFrameOptions(s string) *Handler {
	me.xFrameOptions = strings.TrimSpace(s)
	return me
}

// WithHstsMaxAge sets the max-age (in seconds) of the
// Strict-Transport-Security header. It panics on a negative value.
// Zero disables the header. It returns the same handler for chaining.
//
// Default: 0 (disabled)
func (me *Handler) WithHstsMaxAge(seconds int) *Handler {
	moon.Assert(seconds >= 0, "hsts max age cannot be negative")
	me.hstsMaxAge = seconds
	return me
}

// WithHstsIncludeSubdomains controls whether the includeSubDomains directive
// is added to the Strict-Transport-Security header.
// It returns the same handler for chaining.
//
// Default: true
func (me *Handler) WithHstsIncludeSubdomains(b bool) *Handler {
	me.hstsIncludeSubdomains = b
	return me
}

// WithHstsPreloadEnabled controls whether the preload directive is added to
// the Strict-Transport-Security header.
// It returns the same handler for chaining.
//
// Default: false
func (me *Handler) WithHstsPreloadEnabled(b bool) *Handler {
	me.hstsPreloadEnabled = b
	return me
}

// WithContentSecurityPolicy sets the Content-Security-Policy header value
// (or the Content-Security-Policy-Report-Only header when
// [Handler.WithCspReportOnly] is enabled).
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "" (disabled)
func (me *Handler) WithContentSecurityPolicy(s string) *Handler {
	me.contentSecurityPolicy = strings.TrimSpace(s)
	return me
}

// WithCspReportOnly switches the Content-Security-Policy header to
// Content-Security-Policy-Report-Only.
// It returns the same handler for chaining.
//
// Default: false
func (me *Handler) WithCspReportOnly(reportOnly bool) *Handler {
	me.cspReportOnly = reportOnly
	return me
}

// WithReferrerPolicy sets the Referrer-Policy header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "no-referrer"
func (me *Handler) WithReferrerPolicy(s string) *Handler {
	me.referrerPolicy = strings.TrimSpace(s)
	return me
}

// WithPermissionPolicy sets the Permissions-Policy header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "" (disabled)
func (me *Handler) WithPermissionPolicy(s string) *Handler {
	me.permissionPolicy = strings.TrimSpace(s)
	return me
}

// WithCrossOriginEmbedderPolicy sets the Cross-Origin-Embedder-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. It returns the same handler for chaining.
//
// Default: "require-corp"
func (me *Handler) WithCrossOriginEmbedderPolicy(s string) *Handler {
	me.crossOriginEmbedderPolicy = strings.TrimSpace(s)
	return me
}

// WithCrossOriginOpenerPolicy sets the Cross-Origin-Opener-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. It returns the same handler for chaining.
//
// Default: "same-origin"
func (me *Handler) WithCrossOriginOpenerPolicy(s string) *Handler {
	me.crossOriginOpenerPolicy = strings.TrimSpace(s)
	return me
}

// WithCrossOriginResourcePolicy sets the Cross-Origin-Resource-Policy header
// value. Surrounding whitespace is trimmed. An empty value disables the
// header. It returns the same handler for chaining.
//
// Default: "same-origin"
func (me *Handler) WithCrossOriginResourcePolicy(s string) *Handler {
	me.crossOriginResourcePolicy = strings.TrimSpace(s)
	return me
}

// WithOriginAgentCluster sets the Origin-Agent-Cluster header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "?1"
func (me *Handler) WithOriginAgentCluster(s string) *Handler {
	me.originAgentCluster = strings.TrimSpace(s)
	return me
}

// WithXDnsPrefetchControl sets the X-DNS-Prefetch-Control header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "off"
func (me *Handler) WithXDnsPrefetchControl(s string) *Handler {
	me.xDnsPrefetchControl = strings.TrimSpace(s)
	return me
}

// WithXDownloadOptions sets the X-Download-Options header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "noopen"
func (me *Handler) WithXDownloadOptions(s string) *Handler {
	me.xDownloadOptions = strings.TrimSpace(s)
	return me
}

// WithXPermittedCrossDomainPolicies sets the
// X-Permitted-Cross-Domain-Policies header value.
// Surrounding whitespace is trimmed. An empty value disables the header.
// It returns the same handler for chaining.
//
// Default: "none"
func (me *Handler) WithXPermittedCrossDomainPolicies(s string) *Handler {
	me.xPermittedCrossDomain = strings.TrimSpace(s)
	return me
}

// Handle sets the configured security headers, then runs the chain.
// Headers with empty values are omitted, and Strict-Transport-Security is
// only set when a positive max-age is configured (see
// [Handler.WithHstsMaxAge]).
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.skip != nil && me.skip(ctx) {
		return ctx.Next()
	}

	if me.xssProtection != "" {
		ctx.SetHeader("X-XSS-Protection", me.xssProtection)
	}

	if me.contentTypeNoSniff != "" {
		ctx.SetHeader("X-Content-Type-Options", me.contentTypeNoSniff)
	}

	if me.xFrameOptions != "" {
		ctx.SetHeader("X-Frame-Options", me.xFrameOptions)
	}

	if me.hstsMaxAge > 0 {
		header := "Strict-Transport-Security"
		ctx.AddHeader(header, fmt.Sprintf("max-age=%d", me.hstsMaxAge))
		if me.hstsIncludeSubdomains {
			ctx.AddHeader(header, "includeSubDomains")
		}
		if me.hstsPreloadEnabled {
			ctx.AddHeader(header, "preload")
		}
	}

	if me.contentSecurityPolicy != "" {
		if me.cspReportOnly {
			ctx.SetHeader("Content-Security-Policy-Report-Only", me.contentSecurityPolicy)
		} else {
			ctx.SetHeader("Content-Security-Policy", me.contentSecurityPolicy)
		}
	}

	if me.referrerPolicy != "" {
		ctx.SetHeader("Referrer-Policy", me.referrerPolicy)
	}

	if me.permissionPolicy != "" {
		ctx.SetHeader("Permissions-Policy", me.permissionPolicy)
	}

	if me.crossOriginEmbedderPolicy != "" {
		ctx.SetHeader("Cross-Origin-Embedder-Policy", me.crossOriginEmbedderPolicy)
	}

	if me.crossOriginOpenerPolicy != "" {
		ctx.SetHeader("Cross-Origin-Opener-Policy", me.crossOriginOpenerPolicy)
	}

	if me.crossOriginResourcePolicy != "" {
		ctx.SetHeader("Cross-Origin-Resource-Policy", me.crossOriginResourcePolicy)
	}

	if me.originAgentCluster != "" {
		ctx.SetHeader("Origin-Agent-Cluster", me.originAgentCluster)
	}

	if me.xDnsPrefetchControl != "" {
		ctx.SetHeader("X-DNS-Prefetch-Control", me.xDnsPrefetchControl)
	}

	if me.xDownloadOptions != "" {
		ctx.SetHeader("X-Download-Options", me.xDownloadOptions)
	}

	if me.xPermittedCrossDomain != "" {
		ctx.SetHeader("X-Permitted-Cross-Domain-Policies", me.xPermittedCrossDomain)
	}

	return ctx.Next()
}
