# Moon

Moon is a small, explicit HTTP framework for Go built on top of `net/http`. It gives you a
typed request context, a pattern router, composable middleware, managed services,
typed dependencies, request logging, API-spec metadata and a set of optional
built-in handlers, while keeping every moving part visible in ordinary Go code.

- Module: `github.com/assaidy/moon`
- Go version: **1.27 or newer**
- No global app registry: create an `App`, configure it, register routes, start it.

---

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Requirements and Installation](#requirements-and-installation)
- [Quick Start](#quick-start)
  - [Minimal server](#minimal-server)
  - [Server with graceful shutdown](#server-with-graceful-shutdown)
- [Core Concepts](#core-concepts)
  - [Application lifecycle](#application-lifecycle)
  - [Handlers and the chain](#handlers-and-the-chain)
  - [Routing](#routing)
    - [Methods](#methods)
    - [Pattern grammar](#pattern-grammar)
    - [Matching rules](#matching-rules)
    - [Path parameters](#path-parameters)
    - [Middleware prefixes and ordering](#middleware-prefixes-and-ordering)
    - [Not found and method not allowed](#not-found-and-method-not-allowed)
  - [Context](#context)
    - [Request access](#request-access)
    - [Reading the request body](#reading-the-request-body)
    - [Writing the response](#writing-the-response)
    - [Buffered writes and the raw path](#buffered-writes-and-the-raw-path)
    - [Locals](#locals)
    - [Application state](#application-state)
    - [Cookies](#cookies)
    - [Context as `context.Context`](#context-as-contextcontext)
  - [Dependencies](#dependencies)
  - [Services](#services)
  - [Errors](#errors)
    - [The `Error` type](#the-error-type)
    - [Default error handler](#default-error-handler)
    - [Custom error handler](#custom-error-handler)
    - [Built-in errors](#built-in-errors)
  - [Codecs](#codecs)
  - [Request logging](#request-logging)
  - [API documentation metadata](#api-documentation-metadata)
  - [Server configuration](#server-configuration)
    - [Timeouts and limits](#timeouts-and-limits)
    - [TLS and HTTP/2](#tls-and-http2)
    - [Graceful shutdown](#graceful-shutdown)
  - [Testing](#testing)
- [Configuration Reference](#configuration-reference)
  - [Application options](#application-options)
  - [Route options](#route-options)
  - [Request logging entries](#request-logging-entries)
- [Utilities](#utilities)
- [Design Notes](#design-notes)
  - [Concurrency](#concurrency)
  - [Response buffering and streaming](#response-buffering-and-streaming)

---

## Overview

Moon wraps `net/http` rather than replacing it. A request enters through a standard
`http.Handler`, is normalized, matched against registered routes, and then runs
through a linear chain of handlers. Each handler receives a `*moon.Context` that
exposes the request, the response, route parameters, request-scoped locals,
application state, dependencies and services.

The framework is deliberately explicit:

- Routes and middleware are registered with method calls.
- Invalid registration panics at startup instead of failing at request time.
- Handler failures are returned as `error` values.
- Configuration happens through functional options with documented defaults.
- Optional behavior lives in separate packages under `handlers/`.

Moon is well suited to APIs, internal services, small web applications and
projects that want a productive framework without hidden runtime machinery.

## Features

- **Pattern router** with literals, named parameters, optional parameters,
  wildcards, plus segments, escaping and case-insensitive matching by default.
- **Method-specific registration** with shortcuts for every supported method and
  `MapAll` for catch-all registrations.
- **Middleware** registered for path prefixes, plus per-route middleware.
- **Typed context** with getters and setters for the request, response, route
  parameters, headers, cookies, forms and shared values.
- **Buffered responses** so middleware can set headers and status before or after
  the rest of the chain runs, with an explicit raw-writer escape hatch.
- **Structured errors** with status codes, machine-readable kinds, optional
  details and a default JSON error handler.
- **Codecs** for JSON, XML and MessagePack, plus a `Codec` interface for custom
  formats.
- **Typed dependencies** registered on the app and retrieved in handlers.
- **Managed services** with ordered or parallel start/stop and automatic cleanup
  during shutdown.
- **Request logging** with pluggable per-request entries.
- **API-spec metadata** attached to routes and consumed by documentation handlers.
- **Optional handlers** for common concerns such as CORS, security headers,
  request IDs, health checks, static files, API documentation, etc.
- **Server configuration** for TLS, HTTP/2, custom protocols, timeouts, header
  limits and body limits.
- **In-process testing** through `App.Test`.

## Requirements and Installation

Moon requires Go 1.27 or newer, as declared in `go.mod`.

```sh
go get github.com/assaidy/moon
```

Import the root package for the framework itself:

```go
import "github.com/assaidy/moon"
```

Built-in handlers are separate packages:

```go
import (
    "github.com/assaidy/moon/handlers/cors"
    "github.com/assaidy/moon/handlers/health_check"
)
```

## Quick Start

### Minimal server

```go
package main

import (
    "log/slog"
    "net/http"

    "github.com/assaidy/moon"
)

func main() {
    app := moon.New(moon.WithListenAddress(":3000"))

    app.MapGet("/", func(ctx *moon.Context) error {
        return ctx.WriteJson(http.StatusOK, moon.Object{"message": "hello"})
    })

    if err := app.Start(); err != nil {
        slog.Error("server failed", "error", err)
    }
}
```

`App.Start` starts registered services, then listens and serves until the server
is shut down or fails. See [Server configuration](#server-configuration) for the
available listener options.

### Server with graceful shutdown

```go
package main

import (
    "log/slog"
    "net/http"
    "time"

    "github.com/assaidy/moon"
)

func main() {
    app := moon.New(
        moon.WithListenAddress(":3000"),
        moon.WithRequestLogging(true),
        moon.WithShutdownTimeout(10*time.Second),
    )

    app.MapGet("/health", func(ctx *moon.Context) error {
        return ctx.WriteStatus(http.StatusOK)
    })

    if err := app.RunUntilSignal(); err != nil {
        slog.Error("server failed", "error", err)
    }
}
```

`App.RunUntilSignal` starts registered services, listens, waits for a signal,
then calls `App.Shutdown`. With no arguments it waits for `syscall.SIGINT` and
`syscall.SIGTERM`. Pass signals explicitly to override those defaults:

```go
if err := app.RunUntilSignal(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP); err != nil {
    slog.Error("server failed", "error", err)
}
```

`App.Shutdown` gracefully shuts down the HTTP server and then stops all started
services. The shutdown timeout applies to the HTTP server shutdown.

## Core Concepts

### Application lifecycle

An application moves through four phases:

1. **Construction** — `moon.New(options...)` builds an `App`, applies defaults and
   registers the root HTTP handler.
2. **Configuration** — add dependencies and services, register middleware with
   `Use`/`UseAll`, and register routes with `Map`, `MapGet`, `MapPost`, and so on.
3. **Serving** — `Start` starts services and listens. It blocks until shutdown.
   `RunUntilSignal` performs the serving phase and the shutdown phase in one
   call.
4. **Shutdown** — `Shutdown` stops accepting requests, waits for in-flight
   requests according to the configured timeout, and stops services.

Route registration is a setup-time operation. Do not register routes or
middleware concurrently with request handling; the routing tables are not
protected by a mutex.

For tests, `App.Test` dispatches a request through the same handler chain without
binding a port. Services are not started automatically by `Test`; see
[Testing](#testing).

### Handlers and the chain

A handler is a function:

```go
type Handler func(ctx *Context) error
```

Every request runs through a flattened chain assembled by the router:

1. An internal body-limit check.
2. Middleware registered with `Use` whose prefix matches the request path, in
   registration order.
3. Middleware attached to the matched route with `WithMiddlewares`, in the order
   given.
4. The route handler.

A handler reaches the next one by calling `ctx.Next()`:

```go
func logging(ctx *moon.Context) error {
    start := time.Now()
    err := ctx.Next()
    ctx.SetHeader("X-Took", time.Since(start).String())
    return err
}
```

Chain rules:

- A handler that returns without calling `Next` ends the chain.
- Returning a non-nil error ends the chain; the error handler registered first
  in the chain (see `WithErrorHandler`) receives it.
- `Next` when no handlers remain returns `moon.ErrNotFound`.
- Because the status and body are buffered, a middleware can set a buffered
  status after `Next` returns and still affect the response. Headers are written
  directly and can also be set after `Next` returns, unless a handler used the
  raw writer.
- `Context.IsMiddleware` reports whether the currently executing handler runs
  before the route handler: handlers registered by `Use`, the error handler and
  the app's internal pre-route handlers. It is false for route middlewares and
  route handlers.

Route handlers and middleware use the same type. The distinction is where they
are registered, not what they can do.

### Routing

#### Methods

Routes are registered per method. Convenience methods exist for every supported
HTTP method:

```go
app.MapGet("/users", listUsers)
app.MapPost("/users", createUser)
app.MapPut("/users/:id", replaceUser)
app.MapPatch("/users/:id", updateUser)
app.MapDelete("/users/:id", deleteUser)
app.MapHead("/users", headUsers)
app.MapOptions("/users", optionsUsers)
app.MapConnect("/tunnel", connectTunnel)
app.MapTrace("/debug", traceRequest)
app.MapQuery("/search", search)
```

`Map(method, pattern, handler, options...)` accepts any supported method and
normalizes it with trimming and uppercasing. `MapAll` registers the same handler
for every method.

Supported methods are `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, `CONNECT`,
`OPTIONS`, `TRACE` and `QUERY`, also available as `moon.MethodGet` and similar
constants. `moon.IsValidHttpMethod` validates the canonical uppercase form.

#### Pattern grammar

A pattern must:

- start with `/`;
- not end with `/`, except the root `/`;
- not contain empty segments such as `//`;
- use only the supported literal characters, parameters, wildcards and escapes.

```text
pattern = "/" [ segment { "/" segment } ]
segment = ":" name [ "?" ] | { literal | "*" | "+" }
name    = literal { literal }
literal = letter | digit | "_" | "-" | "." | "\:" | "\*" | "\+" | "\\"
```

A literal character is a letter, digit, `_`, `-` or `.`. A literal colon must be
escaped as `\:`. The same escape sequences are allowed in parameter names.

Named parameters (`:name` and `:name?`) must occupy a whole path segment; they
cannot share it with wildcards or another parameter. The name itself may contain
one or more literals, so `/users/:w_x.y-z\:xsdf` is valid and captures under
`w_x.y-z:xsdf`.

| Syntax | Meaning | Example |
| --- | --- | --- |
| `literal` | Matches the exact text | `/users` |
| `:name` | Captures one non-empty path piece | `/users/:id` |
| `:name?` | Optionally captures one path piece | `/users/:id?` |
| `*` | Matches any span, including empty and `/` | `/files/*` |
| `+` | Matches any non-empty span, including `/` | `/files/+` |
| `\:`, `\*`, `\+`, `\\` | Escapes a special character | `/literal/\*` |

Wildcards can appear inside a segment, including adjacent wildcards such as
`/*+` and `/**`. A trailing wildcard consumes the rest of the path; one inside
a segment consumes up to the part that follows it. Matching is greedy and
backtracks, so the first wildcard takes as much as possible while still allowing
the rest of the pattern to match.

```go
app.MapGet("/files/*", serveFile)       // /files/a/b/c
app.MapGet("/assets/*.css", serveCSS)   // wildcard inside a segment
app.MapGet("/users/:id?", getUser)      // /users and /users/42
```

Parameter names follow the `literal` grammar above. Duplicate parameter names
in one pattern panic at registration.

#### Matching rules

- Route and middleware matching ignores letter case by default, so
  `/API/Users` and `/api/users` are the same route. Pass
  `WithCaseSensitiveMatching(true)` to require an exact match.
- Request paths and registered patterns are kept as provided, so path
  parameters and wildcard captures preserve the request's letter case.
- Trailing slashes are removed from request paths, except for the root `/`.
- The first route registered for a method whose pattern matches wins. Registering
  the same method and pattern twice is allowed, but the first registration is the
  one that matches.
- Route-specific middleware and the handler run only for the matched route.
- A path that matches a route for another method produces `405 Method Not
  Allowed`; otherwise an unmatched path produces `404 Not Found`.
- Matching middleware runs even when no route matches, so middleware can answer,
  decorate or inspect 404/405 responses.

#### Path parameters

```go
app.MapGet("/users/:id/posts/:post_id", func(ctx *moon.Context) error {
    id := ctx.GetParam("id")
    postID := ctx.GetParam("post_id")
    return ctx.WriteJson(http.StatusOK, moon.Object{
        "user": id,
        "post": postID,
    })
})
```

`GetParam` returns `""` when a key is missing. `GetAllParams` returns the whole
captured map.

Wildcards are captured too:

- A single wildcard is available as `*`.
- Multiple wildcards are available as `*1`, `*2`, and so on.
- A single plus segment is available as `+`.
- Multiple plus segments are available as `+1`, `+2`, and so on.

Optional parameters capture `""` when omitted.

#### Middleware prefixes and ordering

`Use(prefix, handlers...)` registers middleware for a path prefix. Matching is
segment-aware:

```go
app.Use("/", globalMiddleware)       // every path
app.Use("/api", apiMiddleware)       // /api and everything below it
app.Use("/api/users", userMiddleware)
```

`/api` does not match `/apiv2`. Prefixes support the same pattern grammar as
routes, including parameters, optional parameters and wildcards, but values are
not captured for middleware.

Only routes registered **after** a `Use` call observe that middleware. This is
why middleware is normally registered before routes:

```go
app.UseAll(cors.New().Handle)
app.MapGet("/users", listUsers)
```

For requests that match no route, every matching `Use` middleware runs regardless
of its order relative to route registrations, because route ordering is only used
to filter middleware for a matched route.

`UseAll` is shorthand for `Use("/", handlers...)`.

A middleware that writes a response and returns without calling `Next`
short-circuits the chain. This is how handlers such as CORS answer preflight
requests without needing a route.

Per-route middleware is added with `WithMiddlewares`:

```go
app.MapGet("/admin", adminPage,
    moon.WithMiddlewares(authMiddleware),
)
```

The route middleware runs after matching `Use` middleware and before the route
handler. Each middleware must call `Next` to reach the handler.

#### Not found and method not allowed

When no route matches, the chain returns:

- `moon.ErrMethodNotAllowed` when the path matches a route registered for a
  different method;
- `moon.ErrNotFound` otherwise.

Matching middleware runs first, so it can inspect the request or replace the
response. The default error handler writes a JSON error body.

### Context

`Context` is created per request. It carries the request and response, matched
route parameters and id, locals, app state, and access to dependencies and
services.

#### Request access

```go
ctx.GetMethod()          // "GET"
ctx.GetPath()            // "/users/42"
ctx.GetUrl()             // *url.URL
ctx.GetQueryString()     // "page=2"
ctx.GetQuery("page")     // "2"
ctx.GetAllQueries()      // map[string][]string
ctx.GetParam("id")       // "42"
ctx.GetAllParams()       // map[string]string
ctx.GetHeader("Accept")  // request header value
ctx.GetAllHeaders()      // map[string][]string
ctx.GetRemoteAddress()   // "127.0.0.1:54321"
ctx.GetAppLogger()       // *slog.Logger from WithLogger
ctx.GetRouteId()         // route id from WithId, or ""
ctx.GetHttpRequest()     // underlying *http.Request
```

`GetHttpRequest` is an escape hatch for standard-library interoperability.
Prefer the typed getters when they cover the case.

#### Reading the request body

`Read` buffers and caches the entire request body:

```go
body, err := ctx.Read()
```

Repeated calls return the same bytes. The returned slice aliases a pooled buffer
that is recycled after the handler chain finishes, so copy it if you need it
later.

Decode directly with a codec:

```go
var input CreateUser
if err := ctx.ReadJson(&input); err != nil {
    return err
}
```

Available helpers:

- `ReadAs(codec, out)`
- `ReadJson(out)`
- `ReadXml(out)`
- `ReadMessagePack(out)`

The body is limited by `WithMaxBodyBytes` (4 MiB by default). A request that
declares a larger `Content-Length` is rejected before the chain runs. Reads that
exceed the limit produce `moon.ErrRequestEntityTooLarge`; reads that time out
produce `moon.ErrRequestTimeout`.

Because `Read` buffers the whole body in memory, do not use it for unbounded
streams or very large uploads. Stream those directly from `ctx.GetHttpRequest().Body`
instead.

Form values and cookies:

```go
ctx.GetFormValue("name")
ctx.GetAllFormValues()

ctx.GetCookie("session")
ctx.GetManyCookies("session")
ctx.GetAllCookies()
ctx.SetCookie(&http.Cookie{Name: "session", Value: token})
ctx.ClearCookie("session")
```

#### Writing the response

The common response helpers buffer both status and body:

```go
ctx.Write(http.StatusOK, "plain text")
ctx.WriteStatus(http.StatusNoContent)
ctx.WriteJson(http.StatusCreated, user)
ctx.WriteXml(http.StatusOK, document)
ctx.WriteMessagePack(http.StatusOK, payload)
ctx.WriteAs(http.StatusOK, codec, value)
```

`WriteAs` and its helpers set the matching `Content-Type` header. If encoding
fails, nothing is buffered and the error is returned.

Headers and status can also be managed directly:

```go
ctx.SetHeader("Cache-Control", "no-store")
ctx.AddHeader("Vary", "Accept-Encoding")
ctx.SetStatusCode(http.StatusAccepted)
status := ctx.GetStatusCode()
```

`SetHeader` replaces values, while `AddHeader` appends. A later status write
overwrites an earlier one. `SetStatusCode` affects the buffered response only;
the raw path has its own status (see
[Buffered writes and the raw path](#buffered-writes-and-the-raw-path)).

`Redirect` sets `Location` and the status code:

```go
return ctx.Redirect(http.StatusSeeOther, "/login")
```

Relative locations are resolved against the request path and non-ASCII bytes are
percent-encoded.

For low-level access, `GetHttpResponseWriter` returns the response writer
wrapper.

#### Buffered writes and the raw path

Status and body are buffered until the handler chain finishes. Headers are
written directly to the underlying writer. This design allows middleware to do
work after `Next`:

```go
func timing(ctx *moon.Context) error {
    start := time.Now()
    err := ctx.Next()
    ctx.SetHeader("X-Response-Time", time.Since(start).String())
    return err
}
```

The buffer is flushed after the chain, including the error handler, runs. A
buffered response with no status code defaults to `200`.

Some responses cannot be buffered: server-sent events, WebSockets, large
streaming downloads and anything that needs `http.Flusher` or `http.Hijacker`.
Use the raw writer for those. Call `Unwrap` to get the raw writer;
`GetHttpResponseWriter` itself returns the buffered writer.

A complete app exposing both a WebSocket echo endpoint and a server-sent events
endpoint. The WebSocket handler checks for an upgrade request, then hands the raw
writer to the upgrader. The SSE handler sets its headers with the context
helpers, writes each event to the raw writer and flushes it; those headers are
still committed because headers are shared with the raw path, and the request
context's `Done` channel stops the loop when the client disconnects:

```go
package main

import (
    "fmt"
    "net/http"
    "time"

    "github.com/assaidy/moon"
    "github.com/gorilla/websocket"
)

func main() {
    app := moon.New(moon.WithListenAddress(":3000"))
    app.MapGet("/websockets", handleWebSocket)
    app.MapGet("/events", handleEvents)

    if err := app.Start(); err != nil {
        panic(err)
    }
}

// handleWebSocket upgrades the request to a WebSocket connection and echoes
// every message back to the client.
func handleWebSocket(ctx *moon.Context) error {
    if !websocket.IsWebSocketUpgrade(ctx.GetHttpRequest()) {
        return moon.ErrUpgradeRequired
    }

    upgrader := websocket.Upgrader{}
    conn, err := upgrader.Upgrade(ctx.GetHttpResponseWriter().Unwrap(), ctx.GetHttpRequest(), nil)
    if err != nil {
        // The upgrader has already written its reply to the raw writer, so this
        // error will not be written. Return it anyway so it appears in request logging.
        return fmt.Errorf("websocket: unexpected upgrade error: %w", err)
    }
    defer conn.Close()

    logger := ctx.GetAppLogger()

    for {
        messageType, messagePayload, err := conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
                logger.Error("failed to read websocket message", "error", err)
            }
            return nil
        }

        if err := conn.WriteMessage(messageType, messagePayload); err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
                logger.Error("failed to write websocket message", "error", err)
            }
            return nil
        }
    }
}

// handleEvents streams the current server time as server-sent events until the
// client disconnects.
func handleEvents(ctx *moon.Context) error {
    ctx.SetHeader("Content-Type", "text/event-stream")
    ctx.SetHeader("Cache-Control", "no-cache")

    writer := ctx.GetHttpResponseWriter().Unwrap()
    controller := http.NewResponseController(writer)

    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return nil

        case t := <-ticker.C:
            fmt.Fprintf(writer, "data: The server time is %s\n\n", t.Format(time.TimeOnly))
            controller.Flush()
        }
    }
}
```

Both handlers never write a buffered status or body. The raw `Write` and `Flush`
calls commit the headers above with an implicit `200`; call the raw
`WriteHeader` first when another status is needed.

Any raw use (directly or through `http.NewResponseController`) marks the request
as raw. At flush time the buffered status and body are discarded and the raw
response stands, so do not mix buffered writes with raw writes in one request.

Headers are shared: the handler or outer middleware can set them before the
first raw write or flush, which commits them. Headers set after the raw response
is committed have no effect. This header sharing is what lets middleware such
as helmet and CORS add headers to a raw response. Status is tracked separately
for the two paths: `SetStatusCode` affects only the buffered response, so use
the raw `WriteHeader` when streaming. Raw `Write` defaults to `200`; raw
`Flush`, `Hijack` and `Header` calls record no status, so `GetStatusCode`
returns `0` for them unless the raw path also wrote one.

#### Locals

Locals are request-scoped values:

```go
ctx.SetLocal("user", user)

if user, ok := ctx.GetLocal[*User]("user"); ok {
    // ...
}

raw, ok := ctx.GetAnyLocal("user")
ctx.DeleteLocal("user")
```

- `SetLocal` panics on an empty key or nil value.
- `GetLocal[T]` returns `false` when the key is missing or has another type.
- Locals are not shared between requests.
- With `WithPassLocalsToContext(true)`, `SetLocal` mirrors values into the
  underlying request context, and `DeleteLocal` shadows the key with nil. This is
  intended for interoperability with standard-library middleware that reads
  `r.Context().Value`.

Locals are not protected by a mutex. Handlers in a single chain run
sequentially, so do not access them concurrently from goroutines without your
own synchronization.

#### Application state

State is shared by every request handled by the app:

```go
ctx.SetState("started_at", time.Now())

if started, ok := ctx.GetState[time.Time]("started_at"); ok {
    // ...
}

raw, ok := ctx.GetAnyState("started_at")
ctx.DeleteState("started_at")
```

State is stored in a `sync.Map`, so it is safe for concurrent access. `SetState`
panics on an empty key or nil value. Use state for small, process-wide values;
use services or dependencies for resources with their own lifecycle.

#### Cookies

Cookie helpers mirror the standard library's behavior. `ClearCookie` expires
every request cookie with the given name and sets nothing when no such cookie was
sent.

#### Context as `context.Context`

`*moon.Context` implements `context.Context` by delegating to the request
context:

```go
func handler(ctx *moon.Context) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return ctx.WriteStatus(http.StatusOK)
    }
}
```

Use `ctx.Deadline`, `ctx.Done`, `ctx.Err` and `ctx.Value` as usual.

### Dependencies

Dependencies are typed values registered on the app and retrieved in handlers:

```go
app.AddDependency(logger)
app.AddDependency(cfg)

app.MapGet("/users", func(ctx *moon.Context) error {
    logger := ctx.GetDependency[*slog.Logger]()
    logger.Info("listing users")
    return ctx.WriteJson(http.StatusOK, users)
})
```

- The lookup key is the concrete type of the registered value.
- Registering the same type again replaces the previous value.
- `AddDependency` panics on a nil value (including a typed nil).
- `GetDependency[T]` panics when no dependency of type `T` was registered.

Dependencies are stored in a plain map and are not protected by a mutex.
Register them during setup, before serving requests.

### Services

A service is a managed background resource with a lifecycle:

```go
type Service interface {
    Name() string
    Start() error
    Stop() error
    IsReady() bool
}
```

Register one with `AddService`:

```go
app.AddService(billing)
```

`AddService` panics when the service is nil, its name is empty or
whitespace-only, or a service of the same concrete type is already registered.

Services are started by `App.Start`, or explicitly by `App.StartServices` when
testing without listening. They are stopped by `App.Shutdown` or explicitly by
`App.StopServices`.

Retrieve a started service in a handler:

```go
billing := ctx.GetService[*BillingService]()
```

`GetService` panics when no service of that type was registered, or when it was
registered but never successfully started.

Lifecycle details:

- If any service fails to start, all services started so far are stopped and no
  service remains visible to `GetService`; the start error is returned.
- `WithParallelServiceStart(true)` starts services concurrently;
  `WithParallelServiceStop(true)` stops them concurrently.
- Services stop in reverse registration order, so services that depend on
  earlier services stop before their dependencies. With parallel stop, the
  stops are initiated concurrently in that order.
- `CheckServicesReadiness(services...)` reports whether every given service is
  non-nil and currently ready.
- `App.CheckServicesReadiness()` reports whether every registered service has
  started and is ready, and returns true when no services are registered.

For tests:

```go
app := moon.New()
app.AddService(testBilling)

if err := app.StartServices(); err != nil {
    t.Fatalf("start services: %v", err)
}
t.Cleanup(app.StopServices)

resp := app.Test(httptest.NewRequest(http.MethodGet, "/users", nil))
```

### Errors

#### The `Error` type

`moon.Error` is the standard structured error:

```go
type Error struct {
    StatusCode  int    `json:"-"`
    Kind        string `json:"kind"`
    Description string `json:"description"`
    Details     any    `json:"details,omitempty"`
}
```

```go
err := moon.NewError(
    http.StatusConflict,
    "user_exists",
    "A user with this email already exists.",
).WithDetails(moon.Object{"field": "email"})
```

`Error()` returns the kind, or `kind(details)` when details are set. `Is` matches
another `Error` by `Kind`, so `errors.Is` works across details and wrapping.
`WithDetails` returns a copy and leaves the original unchanged.

#### Default error handler

`DefaultErrorHandler` is registered as the first handler in the chain. It calls
`Next` and writes errors as JSON:

- An `Error` other than `ErrInternalServerError` is written with its status code,
  kind, description and details.
- `ErrInternalServerError` always produces the generic internal-error body, so
  internal details never reach the client.
- Any other error becomes a generic `500` response, with the original error kept
  as details in the log.
- Details for internal errors remain in the logs, not in the response.

The error the chain returns is what request logging records as the request
error. Because the error handler is part of the chain, one that returns `nil`
keeps the failure out of the request record.

#### Custom error handler

An error handler is a normal `moon.Handler`. It is registered as the first
handler in the chain, so it must call `Next` and handle the error that comes
back:

```go
app := moon.New(moon.WithErrorHandler(func(ctx *moon.Context) error {
    err := ctx.Next()
    if err == nil {
        return nil
    }

    var e moon.Error
    if errors.As(err, &e) {
        ctx.SetHeader("X-Error-Kind", e.Kind)
        ctx.WriteJson(e.StatusCode, e)
        return err
    }

    ctx.SetHeader("X-Error-Kind", "internal")
    ctx.WriteJson(
        http.StatusInternalServerError,
        moon.NewError(http.StatusInternalServerError, "internal", "Something went wrong."),
    )
    return err
}))
```

Because 404 and 405 are passed through the same handler, a custom error handler
can inspect or override them too.

Passing `nil` disables the built-in handler. Errors are then rendered only by a
handler you register yourself, which lets a timing middleware include error
rendering in its measurement:

```go
app := moon.New(moon.WithErrorHandler(nil))
app.UseAll(response_time.New().Handle)
app.UseAll(moon.DefaultErrorHandler)
```

`DefaultErrorHandler` runs after the timing handler returns, so the reported
duration includes rendering the error response.

#### Built-in errors

Moon defines an `Error` for every common HTTP error status. Each has a stable
`Kind`, a default description and the matching status code.

| Status | Variable | Kind |
| --- | --- | --- |
| 400 | `ErrBadRequest` | `bad_request` |
| 401 | `ErrUnauthorized` | `unauthorized` |
| 402 | `ErrPaymentRequired` | `payment_required` |
| 403 | `ErrForbidden` | `forbidden` |
| 404 | `ErrNotFound` | `not_found` |
| 405 | `ErrMethodNotAllowed` | `method_not_allowed` |
| 406 | `ErrNotAcceptable` | `not_acceptable` |
| 407 | `ErrProxyAuthRequired` | `proxy_auth_required` |
| 408 | `ErrRequestTimeout` | `request_timeout` |
| 409 | `ErrConflict` | `conflict` |
| 410 | `ErrGone` | `gone` |
| 411 | `ErrLengthRequired` | `length_required` |
| 412 | `ErrPreconditionFailed` | `precondition_failed` |
| 413 | `ErrRequestEntityTooLarge` | `request_entity_too_large` |
| 414 | `ErrRequestUriTooLong` | `request_uri_too_long` |
| 415 | `ErrUnsupportedMediaType` | `unsupported_media_type` |
| 416 | `ErrRequestedRangeNotSatisfiable` | `request_range_not_satisfiable` |
| 417 | `ErrExpectationFailed` | `expectation_failed` |
| 418 | `ErrTeapot` | `teapot` |
| 421 | `ErrMisdirectedRequest` | `misdirected_request` |
| 422 | `ErrUnprocessableEntity` | `unprocessable_entity` |
| 423 | `ErrLocked` | `locked` |
| 424 | `ErrFailedDependency` | `failed_dependency` |
| 425 | `ErrTooEarly` | `too_early` |
| 426 | `ErrUpgradeRequired` | `upgrade_required` |
| 428 | `ErrPreconditionRequired` | `precondition_required` |
| 429 | `ErrTooManyRequests` | `too_many_requests` |
| 431 | `ErrRequestHeaderFieldsTooLarge` | `request_header_fields_too_large` |
| 451 | `ErrUnavailableForLegalReasons` | `unavailable_for_legal_reasons` |
| 500 | `ErrInternalServerError` | `internal_server_error` |
| 501 | `ErrNotImplemented` | `not_implemented` |
| 502 | `ErrBadGateway` | `bad_gateway` |
| 503 | `ErrServiceUnavailable` | `service_unavailable` |
| 504 | `ErrGatewayTimeout` | `gateway_timeout` |
| 505 | `ErrHttpVersionNotSupported` | `http_version_not_supported` |
| 506 | `ErrVariantAlsoNegotiates` | `variant_also_negotiates` |
| 507 | `ErrInsufficientStorage` | `insufficient_storage` |
| 508 | `ErrLoopDetected` | `loop_detected` |
| 510 | `ErrNotExtended` | `not_extended` |
| 511 | `ErrNetworkAuthenticationRequired` | `network_authentication_required` |

### Codecs

A codec encodes response values and decodes request bodies:

```go
type Codec interface {
    Encode(v any) ([]byte, error)
    Decode(raw []byte, v any) error
    ContentType() string
}
```

Built-in codecs:

| Codec | Decoder helper | Encoder helper | Content type |
| --- | --- | --- | --- |
| `moon.CodecJson` | `ReadJson` | `WriteJson` | `application/json` |
| `moon.CodecXml` | `ReadXml` | `WriteXml` | `application/xml` |
| `moon.CodecMessagePack` | `ReadMessagePack` | `WriteMessagePack` | `application/vnd.msgpack` |

Use a custom codec with the generic helpers:

```go
type TextCodec struct{}

func (TextCodec) Encode(v any) ([]byte, error) {
    return []byte(v.(string)), nil
}

func (TextCodec) Decode(raw []byte, v any) error {
    *(v.(*string)) = string(raw)
    return nil
}

func (TextCodec) ContentType() string { return "text/plain; charset=utf-8" }

var textCodec TextCodec

ctx.WriteAs(http.StatusOK, textCodec, "hello")
ctx.ReadAs(textCodec, &value)
```

### Request logging

Request logging is enabled with `WithRequestLogging(true)`. Every handled
request, including 404 and 405 responses, is logged at info level with the
message `"request handled"`.

An app starts with these entries, in order:

| Key | Value |
| --- | --- |
| `duration` | Time spent handling the request |
| `remote` | Client address |
| `method` | Request method |
| `path` | Request path |
| `status` | Response status code (defaults to 200) |
| `error` | Error returned by the chain |

Additional built-in entries are opt-in:

| Entry | Key | Value |
| --- | --- | --- |
| `RleTime` | `time` | Request arrival time |
| `RleUrl` | `url` | Request URL, path and query |
| `RlePid` | `pid` | Process ID that handled the request |
| `RleUserAgent` | `user_agent` | `User-Agent` request header |

Replace the list:

```go
app := moon.New(
    moon.WithRequestLogging(true),
    moon.WithRequestLoggingEntries([]moon.RequestLoggingEntry{
        moon.RleMethod,
        moon.RlePath,
        moon.RleStatus,
        moon.RleTime,
        moon.RlePid,
    }),
)
```

Append to it:

```go
app.AddRequestLoggingEntry(moon.RleUserAgent)
```

Custom entries can capture work in `Before` and render a value afterwards:

```go
entry := moon.RequestLoggingEntry{
    Key: "user_id",
    Before: func(ctx *moon.Context) {
        ctx.SetLocal("log_user_id", "")
    },
    Value: func(ctx *moon.Context, err error) any {
        id, _ := ctx.GetLocal[string]("log_user_id")
        return id
    },
}
app.AddRequestLoggingEntry(entry)
```

Details:

- Keys are trimmed, must be non-empty and must be unique within the app's list.
- A nil `Value` panics; `Before` is optional.
- `Before` hooks run before the handler chain, one per entry present when the
  request starts.
- Entries added while a request is in flight apply starting with the next
  request.
- Entry values should be cheap and non-blocking.
- The built-in request ID handler can contribute a `request_id` entry; see
  [handlers/request_id](handlers/request_id/README.md).

### API documentation metadata

Routes can carry documentation metadata without depending on any documentation
format:

```go
app.MapGet("/users/:id", getUser,
    moon.WithId("get-user"),
    moon.WithApiSpec(moon.NewApiSpec(
        moon.WithSummary("Get a user"),
        moon.WithDescription("Returns a single user by ID."),
        moon.WithOperationId("getUser"),
        moon.WithTags("users"),
        moon.WithParameter(moon.ApiParameter{
            Name: "id",
            In:   moon.ApiParamInPath,
            Schema: moon.Object{"type": "string"},
        }),
        moon.WithResponse(moon.ApiResponse{
            Status:      http.StatusOK,
            Description: "The requested user.",
            Content: map[string]moon.ApiMediaType{
                "application/json": {
                    Schema: moon.Object{"type": "object"},
                },
            },
        }),
        moon.WithResponse(moon.ApiResponse{
            Status:      http.StatusNotFound,
            Description: "No user with that ID.",
        }),
    )),
)
```

Available options:

- `WithSummary`
- `WithDescription`
- `WithOperationId`
- `WithTags`
- `WithDeprecated`
- `WithParameter`
- `WithRequestBody`
- `WithResponse`
- `WithSecurity`
- `WithExtra`

Parameter locations are `ApiParamInPath`, `ApiParamInQuery`, `ApiParamInHeader` and
`ApiParamInCookie`. Path parameters are forced to required and must exist in the
route pattern, including generated wildcard names such as `*`, `*1`, `+` and
`+1`.

`Context.GetApiSpecs` returns a snapshot of every registered route in
registration order, including routes without a spec. It is intended for
documentation handlers that build and cache a document after all routes are
registered. Do not call it while routes are being registered. The swagger handler
uses this API; see [handlers/swagger](handlers/swagger/README.md).

### Server configuration

#### Timeouts and limits

Moon exposes the corresponding `http.Server` settings:

- `WithReadTimeout` — maximum time to read the entire request, including body.
- `WithReadHeaderTimeout` — maximum time to read request headers.
- `WithWriteTimeout` — maximum time to write the response.
- `WithIdleTimeout` — maximum keep-alive idle time.
- `WithMaxHeaderBytes` — maximum header bytes parsed.
- `WithMaxHeaderValueCount` — maximum number of header values.
- `WithMaxBodyBytes` — maximum request body size accepted by the framework.
- `WithShutdownTimeout` — maximum time for the HTTP server shutdown.

Timeout options panic on a zero duration. A negative duration disables that
timeout. `WithReadTimeout`, `WithWriteTimeout`, `WithIdleTimeout` and
`WithShutdownTimeout` default to `-1` (disabled). `WithReadHeaderTimeout`
defaults to the configured read timeout. Header-size and count options reject
non-positive values. For `WithMaxBodyBytes`, a negative value disables the body
limit, zero rejects every request body, and a positive value sets the limit.

#### TLS and HTTP/2

```go
app := moon.New(
    moon.WithListenAddress(":8443"),
    moon.WithTls("cert.pem", "key.pem"),
)
```

- `WithTls(certFile, keyFile)` enables TLS and requires both paths.
- `WithTlsConfig(config)` supplies a TLS configuration. The server clones it; a
  nil config leaves the default TLS configuration.
- `WithHttp2Config(config)` configures HTTP/2; a nil config leaves the default
  HTTP/2 configuration.
- `WithProtocols(protocols)` sets the accepted protocols, including
  unencrypted HTTP/2 when enabled there. A nil value leaves the default protocol
  set (HTTP/1 and HTTP/2).
- `WithClientPriority(bool)` controls whether RFC 9218 HTTP/2 priorities are
  respected. Default: `true`.
- `WithGeneralOptionsHandler(bool)` controls whether general `OPTIONS` requests
  are answered by the server with `200 OK` and an empty body. Default: `false`.

When TLS is enabled and no listen address is set, the default address is
`:https`; otherwise it is `:http`.

#### Graceful shutdown

`Shutdown` performs an HTTP server shutdown, waits according to
`WithShutdownTimeout`, then stops all started services.

`RunUntilSignal` is the high-level lifecycle method. It starts the app, waits for
one of the provided signals, then calls `Shutdown`. When no signals are provided,
it uses `syscall.SIGINT` and `syscall.SIGTERM`.

`Start` returns `nil` after a graceful shutdown. `http.ErrServerClosed` is
treated as a normal shutdown result. Other server errors are returned.

### Testing

`App.Test` dispatches a request through the app's handler chain without binding a
port:

```go
func TestGetUser(t *testing.T) {
    app := moon.New()
    app.MapGet("/users/:id", func(ctx *moon.Context) error {
        return ctx.WriteJson(http.StatusOK, moon.Object{"id": ctx.GetParam("id")})
    })

    req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
    resp := app.Test(req)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("status = %d", resp.StatusCode)
    }

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(string(body), `"42"`) {
        t.Fatalf("unexpected body: %s", body)
    }
}
```

`Test` does not start services. If handlers depend on services, start them
explicitly and stop them after the test:

```go
if err := app.StartServices(); err != nil {
    t.Fatal(err)
}
t.Cleanup(app.StopServices)
```

Tests run through the same routing, middleware, error handling, buffering and
request logging paths as live requests.

## Configuration Reference

### Application options

| Option | Default | Description |
| --- | --- | --- |
| `WithListenAddress(address)` | `:http`, or `:https` with TLS | TCP address to listen on. |
| `WithLogger(logger)` | `slog.Default()` | Logger for framework, service and request logs. Panics on nil. |
| `WithErrorHandler(handler)` | `DefaultErrorHandler` | Handles errors from the chain, including 404 and 405. Registered first in the chain; `nil` disables it. |
| `WithCaseSensitiveMatching(bool)` | `false` | Requires exact letter case in route and middleware path matching. Path parameters keep the request's case either way. |
| `WithRequestLogging(bool)` | `false` | Logs every handled request. |
| `WithRequestLoggingEntries(entries)` | `RleDuration`, `RleRemote`, `RleMethod`, `RlePath`, `RleStatus`, `RleError` | Replaces the request logging list. |
| `WithGeneralOptionsHandler(bool)` | `false` | Lets the server answer general `OPTIONS` requests. |
| `WithReadTimeout(duration)` | `-1` (disabled) | Maximum time to read the entire request. Negative disables. Zero panics. |
| `WithReadHeaderTimeout(duration)` | `WithReadTimeout` | Maximum time to read request headers. Negative disables. Zero panics. |
| `WithWriteTimeout(duration)` | `-1` (disabled) | Maximum time to write the response. Negative disables. Zero panics. |
| `WithIdleTimeout(duration)` | `-1` (disabled) | Maximum keep-alive idle time. Negative disables. Zero panics. |
| `WithMaxHeaderBytes(bytes)` | `http.DefaultMaxHeaderBytes` (1 MiB) | Maximum header bytes parsed. |
| `WithMaxHeaderValueCount(count)` | `http.DefaultMaxHeaderValueCount` (500) | Maximum header values parsed. |
| `WithMaxBodyBytes(bytes)` | 4 MiB | Maximum request body size accepted by `Read`. Negative disables. Zero rejects every request body. |
| `WithTls(certFile, keyFile)` | disabled | Enables TLS with a certificate and key. |
| `WithTlsConfig(config)` | nil | Sets a TLS configuration; nil leaves the default. |
| `WithHttp2Config(config)` | nil | Sets an HTTP/2 configuration; nil leaves the default. |
| `WithProtocols(protocols)` | nil (HTTP/1 and HTTP/2) | Sets accepted protocols; nil leaves the default. |
| `WithClientPriority(bool)` | `true` | Respects HTTP/2 client priorities. |
| `WithParallelServiceStart(bool)` | `false` | Starts services concurrently. |
| `WithParallelServiceStop(bool)` | `false` | Stops services concurrently. |
| `WithShutdownTimeout(duration)` | `-1` (disabled) | Maximum time for HTTP server shutdown. Negative disables. Zero panics. |
| `WithPassLocalsToContext(bool)` | `false` | Mirrors locals into the request context. |

### Route options

| Option | Description |
| --- | --- |
| `WithMiddlewares(handlers...)` | Adds route-specific middleware before the route handler. |
| `WithId(id)` | Sets the id returned by `Context.GetRouteId`. Ids are not required to be unique. |
| `WithApiSpec(spec)` | Attaches metadata created by `NewApiSpec`. Invalid metadata panics at registration. |

### Request logging entries

| Entry | Key | Notes |
| --- | --- | --- |
| `RleDuration` | `duration` | Default entry. |
| `RleRemote` | `remote` | Default entry. |
| `RleMethod` | `method` | Default entry. |
| `RlePath` | `path` | Default entry. |
| `RleStatus` | `status` | Default entry; 200 when nothing was written. |
| `RleError` | `error` | Default entry; error returned by the chain. |
| `RleTime` | `time` | Opt-in; requires its `Before` hook. |
| `RleUrl` | `url` | Opt-in; path plus query. |
| `RlePid` | `pid` | Opt-in. |
| `RleUserAgent` | `user_agent` | Opt-in. |

Custom entries use the same `RequestLoggingEntry` shape. A `Before` hook is
optional; `Value` is required.

## Utilities

```go
moon.Object{"key": "value"}        // map[string]any shorthand
moon.Assert(condition, "message")  // panic when false
moon.IsNilValue(value)             // nil check including typed nils
moon.GenerateSecureToken()         // 32 random bytes, URL-safe base64
moon.GenerateSecureToken(64)       // custom byte count
```

`GenerateSecureToken` uses `crypto/rand` and `base64.RawURLEncoding`. The default
length is 32 random bytes, producing 43 characters. At most one custom length may
be supplied; it must be non-negative because it is used as a byte count.

`IsNilValue` reports true for a nil interface and for nil chans, funcs, maps,
pointers, unsafe pointers, interfaces and slices, including typed nils.

## Design Notes

### Concurrency

- One `Context` exists per request and is not shared between requests.
- Locals are per-request and are not synchronized; use them from the sequential
  handler chain.
- App state uses `sync.Map` and is safe for concurrent access.
- Dependencies are a plain map; configure them before serving.
- Routing and middleware registration is not safe concurrently with requests.
- Request logging entries are protected by a mutex when appended, and each request
  uses a snapshot taken when it starts.
- Services own their own concurrency and must implement `Start`, `Stop` and
  `IsReady` safely for the way the app invokes them.

### Response buffering and streaming

Moon buffers response status and body so middleware can post-process responses.
Headers are written directly to the underlying writer. This is convenient for
compression, timing, CORS and error handling, but it has consequences:

- Large response bodies are held in memory until the chain finishes.
- Streaming responses must use the raw writer.
- Mixing buffered and raw writes discards the buffered status and body; a raw
  write or flush commits the current headers, so set them before it.
- Status codes are tracked separately for the buffered and raw paths.
- `Context.Read` similarly buffers request bodies up to the configured limit.

For large downloads, server-sent events, WebSockets and similar workloads, use
`GetHttpResponseWriter().Unwrap()` with `http.NewResponseController`, and stream
request bodies from `GetHttpRequest().Body`. Set headers before the first raw
write or flush and write the status on the raw path.
