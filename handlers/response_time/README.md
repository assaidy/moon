# Response time handler

Package `response_time` measures how long the rest of the handler chain takes and
reports the duration in a response header. It is a simple way to expose latency
without changing route handlers.

```go
app.UseAll(response_time.New().Handle)
```

The default header is `X-Response-Time`, and the value is a Go duration string
such as `1.234ms` or `2.5s`.

## Installation

```go
import "github.com/assaidy/moon/handlers/response_time"
```

## Quick start

```go
app.UseAll(response_time.New().Handle)
```

A response then carries:

```text
X-Response-Time: 843.217µs
```

Customize the header:

```go
app.UseAll(response_time.New(
    response_time.WithHeader("X-Took"),
).Handle)
```

Skip selected requests:

```go
app.UseAll(response_time.New(
    response_time.WithSkip(func(ctx *moon.Context) bool {
        return ctx.GetPath() == "/healthz"
    }),
).Handle)
```

## How it works

```go
start := time.Now()
err := ctx.Next()
ctx.SetHeader("X-Response-Time", time.Since(start).String())
return err
```

- If the skip predicate returns true, it calls `ctx.Next()` without measuring and
  without setting the header.
- Otherwise it measures the chain, sets the header, and returns the chain error
  unchanged.
- The header is set even when the chain returns an error, so error responses also
  report timing.
- The value uses `time.Duration.String()`, which can be parsed with
  `time.ParseDuration`.
- The default error handler is registered first in the chain, so it renders the
  error response after this middleware returns; rendering time is not included.
  To include it, disable the built-in handler with `moon.WithErrorHandler(nil)`
  and register `moon.DefaultErrorHandler` after the timing handler.
- Registered globally, it measures the remaining middleware and the matched route
  handler; registered as route middleware, it measures the route handler and any
  later route middleware.

Because response headers are written directly to the underlying response writer,
setting the header after `ctx.Next()` works even though the response body is
buffered.

## Configuration

All options are passed to `response_time.New`.

| Option | Default | Description |
| --- | --- | --- |
| `WithHeader(string)` | `"X-Response-Time"` | Response header carrying the duration. Names are canonicalized. |
| `WithSkip(func(*moon.Context) bool)` | `nil` | Requests for which the predicate returns true run the chain without timing. |

### `WithHeader`

```go
response_time.WithHeader("X-Response-Time")
```

Sets the response header name. Surrounding whitespace is trimmed and the name is
canonicalized, so `x-took` becomes `X-Took`; an empty name panics.

Default: `"X-Response-Time"`.

### `WithSkip`

```go
response_time.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetHeader("X-Internal-Probe") != ""
})
```

When the predicate returns true, the request still runs the chain, but no timing
is recorded and no header is set.

Default: `nil`.

## Examples

### Global timing

```go
app.UseAll(response_time.New().Handle)
```

### Custom header

```go
app.UseAll(response_time.New(
    response_time.WithHeader("Server-Timing"),
).Handle)
```

Note that the `Server-Timing` header has a specific syntax for browser developer
tools. The handler writes a bare duration, not a full `Server-Timing` metric. If
you need the structured format, write the header yourself with
`Server-Timing: app;dur=12.3` using milliseconds.

### Route-specific timing

```go
app.MapGet("/search", search,
    moon.WithMiddlewares(response_time.New().Handle),
)
```

Only `/search` gets the header. Other routes are unaffected.

### Skipping health probes

```go
app.UseAll(response_time.New(
    response_time.WithSkip(func(ctx *moon.Context) bool {
        switch ctx.GetPath() {
        case "/livez", "/readyz", "/startupz":
            return true
        default:
            return false
        }
    }),
).Handle)
```

### Combining with request IDs

```go
app.UseAll(request_id.New().Handle)
app.UseAll(response_time.New().Handle)
```

The request ID is available during the measured chain, and both headers appear on
the response.

### Including error rendering time

The default error handler is registered first in the chain, so it renders error
responses after this middleware returns and that time is not measured. To include
it, disable the built-in handler and register `moon.DefaultErrorHandler` after the
timing handler:

```go
app := moon.New(moon.WithErrorHandler(nil))
app.UseAll(response_time.New().Handle)
app.UseAll(moon.DefaultErrorHandler)
```

`DefaultErrorHandler` is a normal handler, so it runs inside the chain, after
`response_time` calls `Next`. The header then also covers writing the error
response.

## Security notes

- Response timing reveals information about internal processing and dependency
  latency. Consider whether that is acceptable on public endpoints.
- Very precise timing can help attackers infer cache hits, database behavior or
  other internal state. Strip the header at the proxy if needed.
- The header is not a substitute for real metrics or tracing.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithSkip(f func(ctx *moon.Context) bool) Option
func WithHeader(s string) Option
```

## Limitations

- The duration is measured around `ctx.Next()`, not around the full request
  lifecycle or the final flush by the router.
- Error rendering time is not included.
- The header format is `time.Duration.String()`, not a fixed unit or a structured
  `Server-Timing` value.
- The handler does not record percentiles, histograms or metrics. Use a metrics
  library for that.
