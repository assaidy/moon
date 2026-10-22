# Skip handler

Package `skip` wraps a handler and conditionally bypasses it. When a predicate
returns true for a request, the wrapped handler does not run and the chain
continues. When the predicate returns false, the wrapped handler runs normally.

```go
h := skip.New(handler, predicate)
app.UseAll(h.Handle)
```

This is useful for applying a handler only to some requests without writing a
custom conditional wrapper each time.

## Installation

```go
import "github.com/assaidy/moon/handlers/skip"
```

## Quick start

```go
h := skip.New(authMiddleware, func(ctx *moon.Context) bool {
    return ctx.GetPath() == "/public"
})
app.UseAll(h.Handle)
```

For `/public`, `authMiddleware` is bypassed and the chain continues. For every
other path, `authMiddleware` runs as usual.

## How it works

```go
func (me *Handler) Handle(ctx *moon.Context) error {
    if me.predicate(ctx) {
        return ctx.Next()
    }
    return me.handler(ctx)
}
```

- The predicate runs once per request, before the wrapped handler.
- `true` means **skip the wrapped handler** and continue the chain.
- `false` means run the wrapped handler.
- The wrapped handler's error is returned unchanged.
- The predicate's result is not cached; each request evaluates it again.

`skip` does not short-circuit with a response of its own. It delegates either to
the wrapped handler or to the rest of the chain.

## Configuration

`skip` has no functional options. Both arguments are required:

```go
func New(handler moon.Handler, predicate func(*moon.Context) bool) *Handler
```

- `handler` must be non-nil; otherwise `New` panics.
- `predicate` must be non-nil; otherwise `New` panics.

The predicate should be cheap, deterministic enough for the request, and free of
side effects that would surprise the wrapped handler.

## Examples

### Public routes without authentication

```go
func isPublic(ctx *moon.Context) bool {
    switch ctx.GetPath() {
    case "/", "/login", "/register", "/healthz":
        return true
    default:
        return false
    }
}

app.UseAll(skip.New(authMiddleware, isPublic).Handle)
```

### Skip rate limiting for internal requests

```go
func isInternal(ctx *moon.Context) bool {
    return ctx.GetHeader("X-Internal-Token") == internalToken
}

app.UseAll(skip.New(rateLimitMiddleware, isInternal).Handle)
```

Be careful with trust decisions based on headers; make sure the network path
guarantees the header cannot be forged by clients.

### Skip caching for mutations

```go
func isMutation(ctx *moon.Context) bool {
    switch ctx.GetMethod() {
    case moon.MethodPost, moon.MethodPut, moon.MethodPatch, moon.MethodDelete:
        return true
    default:
        return false
    }
}

app.UseAll(skip.New(cacheMiddleware, isMutation).Handle)
```

### Skip logging for probes

```go
app.UseAll(skip.New(requestLogger, func(ctx *moon.Context) bool {
    switch ctx.GetPath() {
    case "/livez", "/readyz", "/startupz":
        return true
    default:
        return false
    }
}).Handle)
```

### Invert a built-in skip option

Some built-in handlers have their own `WithSkip`. Use `skip` when you want the
same predicate to control a handler that does not offer one:

```go
app.UseAll(skip.New(helmet.New().Handle, func(ctx *moon.Context) bool {
    return ctx.GetPath() == "/embed"
}).Handle)
```

## Security notes

- A predicate that skips authentication based on a client-controlled value can
  create an authentication bypass. Base security decisions on trusted state.
- Test the skip predicate thoroughly. A public-route list that accidentally
  includes a sensitive path can expose it.
- Do not use `skip` to hide authorization logic; keep the wrapped handler's
  behavior easy to audit.
- Prefer exact path or method checks over broad substring checks.

## API reference

```go
func New(handler moon.Handler, predicate func(*moon.Context) bool) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
```

## Limitations

- One wrapped handler per `skip` instance.
- No option to invert the predicate; negate it in your function.
- No caching of predicate results.
- Skipping as a terminal route handler calls `Next` and may produce `ErrNotFound`
  when no later handler exists.
