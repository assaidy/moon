# CORS handler

Package `cors` implements the [Cross-Origin Resource Sharing](https://fetch.spec.whatwg.org/#cors-protocol)
protocol for moon. It decides which foreign origins may read responses, answers
preflight requests, and adds the response headers browsers need before a
cross-origin script can use a response.

```go
app.UseAll(cors.New().Handle)
```

CORS is enforced by browsers. It is not an authentication or authorization
mechanism: a client outside a browser can send whatever headers it wants. Use it
to control browser access, not to protect secrets.

## Installation

```go
import "github.com/assaidy/moon/handlers/cors"
```

## Quick start

Allow every origin with the defaults:

```go
app.UseAll(cors.New().Handle)
```

Restrict origins and allow credentials:

```go
app.UseAll(cors.New(
    cors.WithAllowedOrigins([]string{
        "https://app.example.com",
        "https://www.example.com",
    }),
    cors.WithAllowedHeaders([]string{"Content-Type", "Authorization"}),
    cors.WithAllowCredentials(true),
).Handle)
```

Allow every subdomain of a domain:

```go
app.UseAll(cors.New(
    cors.WithAllowedOrigins([]string{"https://*.example.com"}),
).Handle)
```

The wildcard matches `https://app.example.com` and
`https://api.v2.example.com`, but not the apex `https://example.com`.

## How it works

The handler distinguishes three kinds of requests.

### Preflight requests

A request is a preflight when it is an `OPTIONS` request carrying both:

- an `Origin` header;
- an `Access-Control-Request-Method` header.

The browser sends this probe before a cross-origin request that is not a simple
request. The handler answers it directly:

- `204 No Content`;
- `Access-Control-Allow-Origin`, when the origin is allowed;
- `Access-Control-Allow-Methods`;
- `Access-Control-Allow-Headers`, when configured;
- `Access-Control-Allow-Credentials`, when configured and the origin is allowed;
- `Access-Control-Max-Age`, when configured;
- `Access-Control-Allow-Private-Network`, when requested and enabled;
- a `Vary` header covering the request headers the answer depends on.

The handler does **not** call `ctx.Next()` for a preflight request. The rest of
the chain is skipped, so a preflight does not need a registered route and does not
run authentication or application middleware.

When the origin is not allowed, the preflight still carries the configured
methods, allowed headers, max age and `Vary` headers, but it does not carry
`Access-Control-Allow-Origin` or `Access-Control-Allow-Credentials`. The browser
rejects the actual cross-origin request because the allow-origin header is
missing.

### Normal cross-origin requests

Any non-preflight request with an `Origin` header is a cross-origin request. For
an allowed origin the handler sets:

- `Access-Control-Allow-Origin`;
- `Access-Control-Allow-Credentials`, when configured;
- `Access-Control-Expose-Headers`, when configured;
- `Vary: Origin`, except when the configured answer is the all-origins wildcard.

Then it continues the chain with `ctx.Next()`.

### Requests without an allowed origin

A request without an `Origin` header is outside the scope of CORS. A request with
a disallowed origin gets no CORS headers at all. When every origin is not allowed,
the handler still sets `Vary: Origin` so shared caches keep separate responses per
origin.

The application still handles the request; CORS headers only determine whether a
browser exposes the response to the calling script.

## Configuration

All options are passed to `cors.New`. Options are applied in order.

| Option | Default | Description |
| --- | --- | --- |
| `WithAllowedOrigins([]string)` | `{"*"}` | Origins allowed to read responses. Supports exact origins, subdomain wildcards and `"*"`. |
| `WithAllowedOriginsFunc(func(string) bool)` | `nil` | Dynamic origin decision, consulted when the static list does not match. |
| `WithAllowedMethods([]string)` | `HEAD, GET, POST, PUT, PATCH, DELETE, QUERY` | Methods written to `Access-Control-Allow-Methods` on preflight responses. |
| `WithAllowedHeaders([]string)` | none | Request headers written to `Access-Control-Allow-Headers` on preflight responses. Names are canonicalized. |
| `WithExposedHeaders([]string)` | none | Response headers exposed to browser scripts through `Access-Control-Expose-Headers`. Names are canonicalized. |
| `WithAllowCredentials(bool)` | `false` | Allows cookies and other credentials on cross-origin requests. |
| `WithAllowPrivateNetwork(bool)` | `false` | Answers private-network preflight requests. |
| `WithMaxAge(int)` | `-1` | Seconds a browser may cache a preflight result. Negative omits the header. |
| `WithSkip(func(*moon.Context) bool)` | `nil` | Requests for which the predicate returns true bypass CORS handling. |

### `WithAllowedOrigins`

Each entry is trimmed and normalized:

- scheme and host are lowercased;
- a trailing slash is removed;
- default ports are removed (`:80` for HTTP, `:443` for HTTPS);
- the origin must be `scheme://host[:port]`.

The literal `"null"` is accepted. Browsers use it for privacy-sensitive contexts
such as sandboxed iframes and `file://` pages.

The all-origins wildcard `"*"`:

- allows every origin;
- must be the only entry;
- cannot be combined with `WithAllowCredentials(true)`.

A subdomain wildcard must use `*` as the whole first label:
`https://*.example.com`. It matches subdomains at any depth but not the apex
domain. These are rejected:

- `https://*example.com`
- `https://example.*`
- `https://*`
- `*.example.com` (missing scheme)

Subdomain wildcards may be combined with other origins and with credentials,
because they only match your own subdomains.

`WithAllowedOrigins` panics on an invalid entry or on `"*"` combined with other
origins.

### `WithAllowedOriginsFunc`

The function receives the raw `Origin` request header and returns whether to
allow it. It is only consulted when the origin does not match the static list.
When it returns true, the raw request origin is echoed in
`Access-Control-Allow-Origin`.

```go
app.UseAll(cors.New(
    cors.WithAllowedOrigins([]string{"https://app.example.com"}),
    cors.WithAllowedOriginsFunc(func(origin string) bool {
        return strings.HasSuffix(origin, ".internal.example.com")
    }),
).Handle)
```

Never return true for every origin. Doing so defeats the credentials restriction
that prevents `"*"` from being combined with `WithAllowCredentials`. If every
origin should be allowed, use `"*"` in `WithAllowedOrigins` instead.

The function receives the raw `Origin` header, so validate it carefully if it is
used for anything beyond suffix checks. When it returns true, the handler echoes
that same raw origin unchanged; origins matched from the static list are echoed
in normalized form instead.

### `WithAllowedMethods`

Methods are trimmed and uppercased; invalid HTTP methods panic. They are listed on
preflight responses only. They do not restrict normal requests, and they do not
register routes. A browser preflight can succeed for a method that has no route,
and the actual request then receives `405 Method Not Allowed`.

The default list is `HEAD`, `GET`, `POST`, `PUT`, `PATCH`, `DELETE` and `QUERY`.
It does not include `OPTIONS`, `CONNECT` or `TRACE`.

### `WithAllowedHeaders`

These are the request headers the browser is allowed to send on the actual
cross-origin request. They are written on preflight responses only.

Headers that browsers consider safelisted do not need to be listed. Add
`Content-Type`, `Authorization` and any custom headers your API uses. The
handler does not inspect `Access-Control-Request-Headers`; it always advertises
the configured list.

Surrounding whitespace is trimmed and each name is canonicalized, so
`content-type` is written as `Content-Type`.

### `WithExposedHeaders`

By default browser scripts can read only a small set of response headers. Add
headers that scripts should read. This is a normal-response header, not a
preflight header. Like `WithAllowedHeaders`, it trims whitespace and
canonicalizes each name.

### `WithAllowCredentials`

Enables cookies, HTTP authentication and client certificates on cross-origin
requests. The browser only sends credentials when both the request and the server
allow it.

`"*"` cannot be combined with credentials: that would let every site make
credentialed requests. Use explicit origins or a subdomain wildcard instead.

When credentials are enabled, the response cannot use `Access-Control-Allow-Origin:
*`; the handler always echoes a specific origin.

### `WithAllowPrivateNetwork`

When enabled and a preflight request carries
`Access-Control-Request-Private-Network: true`, the handler responds with
`Access-Control-Allow-Private-Network: true`. This supports browsers implementing
the Private Network Access specification.

### `WithMaxAge`

The number of seconds a browser may cache the preflight result.

- `0` and positive values write `Access-Control-Max-Age`.
- Negative values omit the header.
- Default: `-1`.

Longer values reduce preflight traffic but delay propagation of changed CORS
policy.

### `WithSkip`

```go
app.UseAll(cors.New(
    cors.WithSkip(func(ctx *moon.Context) bool {
        return ctx.GetPath() == "/internal"
    }),
).Handle)
```

A skipped request runs the chain with no CORS headers and no preflight handling.
The predicate runs once per request, before any CORS work.

## Vary and caching

`Vary` tells shared caches which request headers influenced the response.

- Preflight responses set `Vary: Origin, Access-Control-Request-Method,
  Access-Control-Request-Headers`.
- Normal responses with a specific allowed origin set `Vary: Origin`.
- Responses with no `Origin`, or a disallowed origin, set `Vary: Origin` unless
  `"*"` is in the allowed origins list.
- When every origin is allowed with `"*"`, no `Vary: Origin` is needed.

If you add CORS handling behind a shared cache, preserve these headers. A cache
that ignores `Vary` can serve one origin's response to another origin.

## Examples

### Public read-only API

```go
app.UseAll(cors.New(
    cors.WithAllowedMethods([]string{moon.MethodGet, moon.MethodHead}),
).Handle)
```

This answers preflights for `GET` and `HEAD` and allows every origin. It does not
prevent clients from sending other methods; it only tells browsers what is
allowed.

### Explicit allowlist with credentials

```go
app.UseAll(cors.New(
    cors.WithAllowedOrigins([]string{
        "https://app.example.com",
        "https://admin.example.com",
    }),
    cors.WithAllowedMethods([]string{
        moon.MethodGet,
        moon.MethodPost,
        moon.MethodPut,
        moon.MethodPatch,
        moon.MethodDelete,
    }),
    cors.WithAllowedHeaders([]string{
        "Content-Type",
        "Authorization",
        "X-CSRF-Token",
    }),
    cors.WithExposedHeaders([]string{"X-Request-ID"}),
    cors.WithAllowCredentials(true),
    cors.WithMaxAge(600),
).Handle)
```

### API-only CORS on a prefix

```go
app.Use("/api", cors.New(
    cors.WithAllowedOrigins([]string{"https://app.example.com"}),
).Handle)
```

### Combining with request IDs

```go
app.UseAll(cors.New(
    cors.WithExposedHeaders([]string{"X-Request-ID"}),
).Handle)

app.UseAll(request_id.New().Handle)
```

Order matters for preflight requests: CORS answers them without calling `Next`, so
the request ID handler does not run for preflights. If you need request IDs on
preflights too, register the request ID middleware first.

## Security notes

- CORS is not authentication. Every HTTP client can forge `Origin`.
- Never pair `WithAllowCredentials(true)` with an origin predicate that accepts
  every origin.
- Prefer explicit origins over subdomain wildcards, and subdomain wildcards over
  `"*"`.
- Do not expose sensitive response headers through `WithExposedHeaders` unless
  the calling script is trusted.
- A successful preflight does not authorize the actual request. Enforce
  authentication and authorization in your handlers.
- Requests without `Origin` are outside CORS and will still reach your routes;
  do not rely on CORS to block non-browser clients.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithAllowedOrigins(origins []string) Option
func WithAllowedOriginsFunc(f func(origin string) bool) Option
func WithAllowedMethods(methods []string) Option
func WithAllowedHeaders(headers []string) Option
func WithExposedHeaders(headers []string) Option
func WithAllowCredentials(b bool) Option
func WithAllowPrivateNetwork(b bool) Option
func WithMaxAge(i int) Option
func WithSkip(f func(ctx *moon.Context) bool) Option
```

## Limitations

- `Access-Control-Request-Headers` is not used to trim the advertised allowed
  headers; the configured list is returned as-is.
- The handler does not add `Access-Control-Allow-Methods` or
  `Access-Control-Allow-Headers` to normal responses; those are preflight-only.
- There is no built-in origin cache for `WithAllowedOriginsFunc`; keep dynamic
  checks cheap.
- The handler does not implement `Access-Control-Allow-Private-Network`
  validation beyond the request header and the configured switch.
