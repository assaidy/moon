# Helmet handler

Package `helmet` sets security-related HTTP response headers. It is a lightweight
middleware that adds headers such as `X-Content-Type-Options`, `X-Frame-Options`,
`Referrer-Policy` and the cross-origin isolation headers to every response.

```go
app.UseAll(helmet.New().Handle)
```

The handler sets headers only. It does not validate requests, block traffic or
configure TLS. It is a defense-in-depth measure, not a replacement for
authentication, authorization, input validation or HTTPS.

## Installation

```go
import "github.com/assaidy/moon/handlers/helmet"
```

## Quick start

Use the defaults:

```go
app.UseAll(helmet.New().Handle)
```

Customize individual headers:

```go
app.UseAll(helmet.New(
    helmet.WithXFrameOptions("DENY"),
    helmet.WithHstsMaxAge(31536000),
    helmet.WithContentSecurityPolicy("default-src 'self'"),
).Handle)
```

Disable a header by passing an empty value:

```go
app.UseAll(helmet.New(
    helmet.WithXFrameOptions(""),
    helmet.WithCrossOriginEmbedderPolicy(""),
).Handle)
```

## Default headers

| Header | Default | Option |
| --- | --- | --- |
| `X-XSS-Protection` | `0` | `WithXssProtection` |
| `X-Content-Type-Options` | `nosniff` | `WithContentTypeNoSniff` |
| `X-Frame-Options` | `SAMEORIGIN` | `WithXFrameOptions` |
| `Strict-Transport-Security` | disabled | `WithHstsMaxAge`, `WithHstsIncludeSubdomains`, `WithHstsPreloadEnabled` |
| `Content-Security-Policy` | disabled | `WithContentSecurityPolicy`, `WithCspReportOnly` |
| `Referrer-Policy` | `no-referrer` | `WithReferrerPolicy` |
| `Permissions-Policy` | disabled | `WithPermissionPolicy` |
| `Cross-Origin-Embedder-Policy` | `require-corp` | `WithCrossOriginEmbedderPolicy` |
| `Cross-Origin-Opener-Policy` | `same-origin` | `WithCrossOriginOpenerPolicy` |
| `Cross-Origin-Resource-Policy` | `same-origin` | `WithCrossOriginResourcePolicy` |
| `Origin-Agent-Cluster` | `?1` | `WithOriginAgentCluster` |
| `X-DNS-Prefetch-Control` | `off` | `WithXDnsPrefetchControl` |
| `X-Download-Options` | `noopen` | `WithXDownloadOptions` |
| `X-Permitted-Cross-Domain-Policies` | `none` | `WithXPermittedCrossDomainPolicies` |

The three headers that commonly break applications — HSTS, CSP and
Permissions-Policy — are disabled by default. The cross-origin isolation headers
have defaults but may need adjustment for applications that embed or are embedded
by other origins.

## How headers are set

For each configured header:

- A non-empty value is written with `ctx.SetHeader`, replacing any previous value.
- An empty value omits the header entirely.
- HSTS is special: it is only set when `hstsMaxAge >= 0`.

The handler sets the headers before calling `ctx.Next()`, so they are present on
the response regardless of how the rest of the chain finishes.

## Configuration

### `WithSkip`

```go
helmet.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetPath() == "/webhook"
})
```

When the predicate returns true, no headers are set and the chain continues. The
predicate runs once per request.

Default: `nil`.

### `WithXssProtection`

Sets `X-XSS-Protection`. The value is trimmed; an empty value disables the header.

Default: `"0"`.

`0` disables the legacy browser XSS auditor. Modern browsers ignore this header,
and the auditor has historically introduced vulnerabilities. Setting `0` is the
recommended modern default. Setting `1; mode=block` is generally no longer
recommended.

### `WithContentTypeNoSniff`

Sets `X-Content-Type-Options`. The value is trimmed; an empty value disables the
header.

Default: `"nosniff"`.

`nosniff` stops browsers from guessing a content type that differs from the
declared `Content-Type`. Keep it unless a client depends on MIME sniffing.

### `WithXFrameOptions`

Sets `X-Frame-Options`. The value is trimmed; an empty value disables the header.

Default: `"SAMEORIGIN"`.

Common values:

- `DENY` — the page cannot be framed at all.
- `SAMEORIGIN` — only pages from the same origin can frame it.

`Content-Security-Policy: frame-ancestors` is the modern replacement, but
`X-Frame-Options` is still useful for older browsers. If you use CSP
`frame-ancestors`, keep the two consistent.

### `WithHstsMaxAge`

Sets the `max-age` directive of `Strict-Transport-Security`. A negative value
disables the header; `0` emits `max-age=0`, which tells browsers to forget any
previously stored HSTS policy.

Default: `-1` (header disabled).

HSTS tells browsers to use HTTPS for future requests. It only takes effect over
HTTPS; browsers ignore it on plain HTTP. Start with a short max age and increase it
after confirming HTTPS works for every subdomain you include.

Do not enable HSTS on a site that is not fully HTTPS-ready.

### `WithHstsIncludeSubdomains`

Adds `includeSubDomains` to HSTS.

Default: `true`.

This applies HSTS to every subdomain. Make sure every subdomain supports HTTPS
before enabling it.

### `WithHstsPreloadEnabled`

Adds `preload` to HSTS.

Default: `false`.

Preload requests inclusion in browser HSTS preload lists. Removal is slow and
requires submitting a removal request. Enable it only when you meet the preload
requirements and are prepared for the long-term commitment.

### `WithContentSecurityPolicy`

Sets the CSP value. The value is trimmed; an empty value disables the header.

Default: `""` (disabled).

Example:

```go
helmet.WithContentSecurityPolicy(
    "default-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'none'",
)
```

CSP is application-specific. A policy that is too strict breaks scripts, styles,
fonts and images; a policy that is too loose provides little protection. Test it
before enforcing.

### `WithCspReportOnly`

When true, the CSP is written as `Content-Security-Policy-Report-Only` instead of
`Content-Security-Policy`. Report-only policies report violations without
blocking them.

Default: `false`.

### `WithReferrerPolicy`

Sets `Referrer-Policy`. The value is trimmed; an empty value disables the header.

Default: `"no-referrer"`.

Common values include `no-referrer`, `same-origin`, `strict-origin`,
`strict-origin-when-cross-origin` and `no-referrer-when-downgrade`. Choose based
on how much referrer information your application and analytics need.

### `WithPermissionPolicy`

Sets `Permissions-Policy`. The value is trimmed; an empty value disables the
header.

Default: `""` (disabled).

Example:

```go
helmet.WithPermissionPolicy("camera=(), microphone=(), geolocation=()")
```

Permissions-Policy controls access to browser features. It is site-specific and
disabled by default.

### `WithCrossOriginEmbedderPolicy`

Sets `Cross-Origin-Embedder-Policy`. The value is trimmed; an empty value disables
the header.

Default: `"require-corp"`.

`require-corp` requires cross-origin resources to opt in with CORP or CORS. This
is required for powerful features such as `SharedArrayBuffer` and
`performance.measureUserAgentSpecificMemory()`, but it can block images, scripts
and iframes that do not send the right headers. Use `credentialless` if supported,
or disable the header if it breaks embedded resources.

### `WithCrossOriginOpenerPolicy`

Sets `Cross-Origin-Opener-Policy`. The value is trimmed; an empty value disables
the header.

Default: `"same-origin"`.

`same-origin` isolates the browsing context from cross-origin windows. This
improves security but can break OAuth popups, payment flows and other
window-based integrations. Test those flows.

### `WithCrossOriginResourcePolicy`

Sets `Cross-Origin-Resource-Policy`. The value is trimmed; an empty value disables
the header.

Default: `"same-origin"`.

Controls which origins may load the resource. `same-origin` blocks cross-origin
embedding; use `same-site` or `cross-origin` when other sites must load your
resources.

### `WithOriginAgentCluster`

Sets `Origin-Agent-Cluster`. The value is trimmed; an empty value disables the
header.

Default: `"?1"`.

`?1` requests origin-keyed agent clusters, which improve isolation between
same-site origins. It can affect synchronous scripting between same-site frames;
test if you depend on such behavior.

### `WithXDnsPrefetchControl`

Sets `X-DNS-Prefetch-Control`. The value is trimmed; an empty value disables the
header.

Default: `"off"`.

`off` disables speculative DNS prefetching, which can reduce privacy leaks but may
slightly increase latency for external resources.

### `WithXDownloadOptions`

Sets `X-Download-Options`. The value is trimmed; an empty value disables the
header.

Default: `"noopen"`.

This is a legacy Internet Explorer header that prevents downloaded files from
being opened directly in the context of the site.

### `WithXPermittedCrossDomainPolicies`

Sets `X-Permitted-Cross-Domain-Policies`. The value is trimmed; an empty value
disables the header.

Default: `"none"`.

This legacy header controls whether Adobe Flash and PDF readers may load
cross-domain policy files. `none` disables them.

## Examples

### Strict application with HTTPS

```go
app.UseAll(helmet.New(
    helmet.WithXFrameOptions("DENY"),
    helmet.WithHstsMaxAge(63072000),
    helmet.WithHstsIncludeSubdomains(true),
    helmet.WithContentSecurityPolicy(
        "default-src 'self'; base-uri 'none'; frame-ancestors 'none'",
    ),
    helmet.WithReferrerPolicy("strict-origin-when-cross-origin"),
    helmet.WithPermissionPolicy("camera=(), microphone=(), geolocation=()"),
).Handle)
```

### API that embeds external resources

```go
app.UseAll(helmet.New(
    helmet.WithXFrameOptions("SAMEORIGIN"),
    helmet.WithCrossOriginEmbedderPolicy(""),
    helmet.WithCrossOriginResourcePolicy("cross-origin"),
).Handle)
```

### Gradual CSP rollout

```go
app.UseAll(helmet.New(
    helmet.WithContentSecurityPolicy("default-src 'self'; report-uri /csp-report"),
    helmet.WithCspReportOnly(true),
).Handle)
```

Report-only mode lets you observe violations before enforcing the policy.

### Skipping a webhook endpoint

```go
app.UseAll(helmet.New(
    helmet.WithSkip(func(ctx *moon.Context) bool {
        return ctx.GetPath() == "/webhooks/payments"
    }),
).Handle)
```

## Security notes

- Helmet does not replace HTTPS, authentication or authorization.
- HSTS should only be enabled when every affected host and subdomain supports
  HTTPS.
- CSP needs application-specific tuning; an untested policy can break the site.
- Cross-origin isolation headers can break legitimate embeds and popups. Test
  OAuth, payments, maps and third-party widgets.
- `X-XSS-Protection: 0` is intentional; the legacy auditor can create
  vulnerabilities.
- Security headers are only one layer. Validate input, escape output, use secure
  cookies and keep dependencies patched.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithSkip(f func(*moon.Context) bool) Option
func WithXssProtection(s string) Option
func WithContentTypeNoSniff(s string) Option
func WithXFrameOptions(s string) Option
func WithHstsMaxAge(seconds int) Option
func WithHstsIncludeSubdomains(b bool) Option
func WithHstsPreloadEnabled(b bool) Option
func WithContentSecurityPolicy(s string) Option
func WithCspReportOnly(reportOnly bool) Option
func WithReferrerPolicy(s string) Option
func WithPermissionPolicy(s string) Option
func WithCrossOriginEmbedderPolicy(s string) Option
func WithCrossOriginOpenerPolicy(s string) Option
func WithCrossOriginResourcePolicy(s string) Option
func WithOriginAgentCluster(s string) Option
func WithXDnsPrefetchControl(s string) Option
func WithXDownloadOptions(s string) Option
func WithXPermittedCrossDomainPolicies(s string) Option
```

## Limitations

- The handler does not add `Content-Security-Policy` nonces or hashes; build the
  policy and nonce handling in your application.
- The handler does not know whether the connection uses HTTPS; HSTS is written
  whenever a non-negative max age is configured.
- Values are used as provided after trimming; the handler does not validate
  directives or header syntax.
- Headers are only set on responses that pass through the handler. Responses
  written before the middleware runs are unaffected.
