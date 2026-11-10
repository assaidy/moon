# Request ID handler

Package `request_id` assigns a request ID to every request, echoes it in a
response header and stores it in the request context. Request IDs make it
possible to correlate logs, traces and client reports for one request.

```go
app.UseAll(request_id.New())
```

## Installation

```go
import "github.com/assaidy/moon/handlers/request_id"
```

## Quick start

Use the defaults:

```go
app.UseAll(request_id.New())
```

Read the ID in a handler:

```go
app.MapGet("/users", func(ctx *moon.Context) error {
    id, _ := ctx.GetLocal[string](request_id.DefaultLocalKey)
    logger.Info("listing users", "request_id", id)
    return ctx.WriteJson(http.StatusOK, users)
})
```

Change the header name:

```go
app.UseAll(request_id.New(
    request_id.WithHeader("X-Correlation-ID"),
))
```

Change the context local key:

```go
h := request_id.New(
    request_id.WithLocalKey("app.request_id"),
)
app.UseAll(h)

id, _ := ctx.GetLocal[string]("app.request_id")
```

## How it works

For each request that is not skipped:

1. Read the configured request header.
2. Reuse the incoming value when it is a valid ID.
3. Otherwise generate a new ID.
4. Set the same header on the response.
5. Store the ID in a context local.
6. Continue the chain with `ctx.Next()`.

The handler never rejects a request because of a missing or invalid ID. It always
produces a usable ID.

### Incoming IDs

An incoming ID is reused when it is:

- non-empty;
- made only of printable ASCII characters, `0x20` through `0x7E`.

Spaces inside the value are accepted. The HTTP server strips leading and trailing
whitespace from header values before the handler sees them, so edge whitespace is
not preserved.

A valid incoming value is echoed as-is. If the value is invalid, it is discarded
and a new ID is generated.

### Generated IDs

The default generator is `moon.GenerateSecureToken`, which returns 32 random
bytes encoded with unpadded URL-safe base64 (43 characters).

The generator output is trimmed and validated. The handler tries up to three
times. If all attempts are invalid, it falls back to
`moon.GenerateSecureToken`.

## Configuration

All options are passed to `request_id.New`.

| Option | Default | Description |
| --- | --- | --- |
| `WithHeader(string)` | `"X-Request-ID"` | Request header read and response header written. Names are canonicalized. |
| `WithGenerator(func() string)` | `moon.GenerateSecureToken` | Generates IDs; a nil func panics. |
| `WithLocalKey(string)` | `DefaultLocalKey` | Context local key holding the ID, used as-is. |
| `WithSkip(func(*moon.Context) bool)` | `nil` | Requests for which the predicate returns true skip ID assignment. |

### `WithHeader`

```go
request_id.WithHeader("X-Correlation-ID")
```

Sets both the request header read and the response header written. The name is
trimmed and canonicalized, so `x-correlation-id` becomes `X-Correlation-Id`; an
empty name panics.

Default: `"X-Request-ID"`.

### `WithGenerator`

```go
request_id.WithGenerator(func() string {
    return uuid.NewString()
})
```

Sets the ID generator. It panics if the function is nil. The output is trimmed
and must pass the same printable-ASCII validation as incoming IDs. Up to three
attempts are made before the secure-token fallback.

Default: `moon.GenerateSecureToken`.

The generator should be fast and concurrency-safe, because it runs in the request
path.

### `WithLocalKey`

```go
h := request_id.New(
    request_id.WithLocalKey("app.request_id"),
)
```

Sets the context local key under which the ID is stored. The key is used as-is,
including surrounding whitespace; an empty key panics.

Default: `request_id.DefaultLocalKey`.

Read the ID with `ctx.GetLocal[string]("app.request_id")`. For the default key,
use `ctx.GetLocal[string](request_id.DefaultLocalKey)`.

### `WithSkip`

```go
request_id.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetPath() == "/metrics"
})
```

When the predicate returns true:

- no request ID is generated;
- no response header is set;
- nothing is stored in the context;
- the chain continues normally.

Default: `nil`.

## Request logging integration

The handler does not register a request logging entry. Add a custom
`moon.RequestLoggingEntry` yourself when request logging is enabled:

```go
h := request_id.New()

app.AddRequestLoggingEntry(moon.RequestLoggingEntry{
    Key: "request_id",
    Value: func(ctx *moon.Context, err error) any {
        id, _ := ctx.GetLocal[string](request_id.DefaultLocalKey)
        return id
    },
})
app.UseAll(h)
```

## Examples

### Default setup

```go
app.UseAll(request_id.New())
```

### Custom header

```go
app.UseAll(request_id.New(
    request_id.WithHeader("X-Correlation-ID"),
))
```

### UUID generator

```go
app.UseAll(request_id.New(
    request_id.WithGenerator(func() string {
        return uuid.NewString()
    }),
))
```

### Request ID plus response timing

```go
app.UseAll(request_id.New())
app.UseAll(response_time.New())
```

The request ID is available to the timing middleware and every later handler.

### Returning the ID to clients

The handler already echoes it. Clients can read the configured response header
and include it in bug reports.

## Security notes

- An incoming request ID is client-controlled. Do not treat it as an identity,
  authorization token or proof of origin.
- The handler only accepts printable ASCII, but a client can still send long or
  misleading IDs. Apply your own length limits if needed.
- Avoid logging untrusted IDs in contexts where they could be interpreted as
  structured data; log them as a value, not as a format string.
- Request IDs help correlation, not security. Use authentication and
  authorization for access control.

## API reference

```go
const DefaultLocalKey = "moon.handlers.request_id.local_key"

func New(options ...Option) moon.Handler

type Option func(*options)
```

Options:

```go
func WithSkip(f func(ctx *moon.Context) bool) Option
func WithHeader(s string) Option
func WithGenerator(f func() string) Option
func WithLocalKey(s string) Option
```

## Limitations

- The handler does not deduplicate or persist IDs.
- It has no built-in length limit beyond the HTTP server's header limits.
- It does not propagate the ID to outbound HTTP requests; pass it explicitly when
  calling other services.
- If the handler is skipped, nothing is stored, so reading the local returns
  the zero value.
- Read the ID from the context with `ctx.GetLocal[string](request_id.DefaultLocalKey)`,
  or with the key passed to `WithLocalKey`.
