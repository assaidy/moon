# Health check handler

Package `health_check` provides a terminal endpoint for liveness, readiness and
startup probes. A probe is a function that decides whether the application is
healthy, and a response function renders that decision.

```go
app.MapGet(health_check.ReadinessEndpoint, health_check.New().Handle)
```

Health endpoints are commonly used by Kubernetes, load balancers, container
orchestrators and monitoring systems.

## Installation

```go
import "github.com/assaidy/moon/handlers/health_check"
```

## Quick start

Register the built-in endpoints:

```go
app.MapGet(health_check.LivenessEndpoint, health_check.New().Handle)
app.MapGet(health_check.ReadinessEndpoint, health_check.New().Handle)
app.MapGet(health_check.StartupEndpoint, health_check.New().Handle)
```

With the default configuration:

- the probe always reports healthy;
- a healthy probe writes `200 OK`;
- an unhealthy probe writes `503 Service Unavailable`;
- the body is empty.

## Endpoint constants

| Constant | Path | Typical purpose |
| --- | --- | --- |
| `LivenessEndpoint` | `/livez` | Is the process running and able to respond? |
| `ReadinessEndpoint` | `/readyz` | Is the application ready to serve traffic? |
| `StartupEndpoint` | `/startupz` | Has initialization finished? |

The constants are convenience values. You can register the handler on any path.

## How it works

`Handle` does three things:

1. Calls the configured probe with the request context.
2. Passes the boolean result to the configured response function.
3. Returns the response function's error.

It never calls `ctx.Next()`. The endpoint is responsible for the whole response.

## Configuration

All options are passed to `health_check.New`.

| Option | Default | Description |
| --- | --- | --- |
| `WithProbe(func(*moon.Context) bool)` | always `true` | Decides whether the endpoint reports healthy; a nil func panics. |
| `WithResponse(func(*moon.Context, bool) error)` | status-only `200` when healthy, `503` otherwise | Renders the probe result; a nil func panics. |

### `WithProbe`

```go
health_check.WithProbe(func(ctx *moon.Context) bool {
    return db.IsReady()
})
```

The probe returns `true` for healthy and `false` for unhealthy. It panics if the
function is nil.

Default: always `true`.

The probe should be cheap enough to run on every probe request. If it performs
network calls, apply a short timeout and avoid blocking indefinitely:

```go
health_check.WithProbe(func(ctx *moon.Context) bool {
    ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()
    return db.PingContext(ctx) == nil
})
```

The probe receives the request context, so client cancellation and request
deadlines can propagate to dependencies.

### `WithResponse`

```go
health_check.WithResponse(func(ctx *moon.Context, ok bool) error {
    if !ok {
        return ctx.WriteJson(http.StatusServiceUnavailable, moon.Object{
            "status": "unhealthy",
        })
    }
    return ctx.WriteJson(http.StatusOK, moon.Object{
        "status": "healthy",
    })
})
```

The response function renders the probe result. It panics if the function is nil.

Default: `200 OK` when healthy, `503 Service Unavailable` otherwise, with no body.

Returning an error hands it to the app's error handler. If you write your own
response, never write body bytes for a `HEAD` request; HEAD responses must not
carry a body.

## Probe semantics

Liveness, readiness and startup answer different questions. Using separate probes
avoids restarting a process that is merely not ready yet.

### Liveness

- Question: is the process alive and not deadlocked?
- Failure usually causes a container restart.
- Keep it independent of downstream services. A database outage should not
  restart every application instance.
- Default probe is often sufficient.

```go
app.MapGet(health_check.LivenessEndpoint, health_check.New().Handle)
```

### Readiness

- Question: can this instance serve requests right now?
- Failure removes the instance from load balancing without restarting it.
- Check critical dependencies and startup state.
- Avoid expensive checks on every probe.

```go
app.MapGet(health_check.ReadinessEndpoint, health_check.New(
    health_check.WithProbe(func(ctx *moon.Context) bool {
        return db.IsReady() && cache.IsReady()
    }),
).Handle)
```

Moon services expose `IsReady`, and `moon.CheckServicesReadiness` can combine
them. The `App` method checks every registered service that has started:

```go
app.MapGet(health_check.ReadinessEndpoint, health_check.New(
    health_check.WithProbe(func(ctx *moon.Context) bool {
        return app.CheckServicesReadiness()
    }),
).Handle)
```

To check a specific set of services instead, pass them explicitly:

```go
app.MapGet(health_check.ReadinessEndpoint, health_check.New(
    health_check.WithProbe(func(ctx *moon.Context) bool {
        return moon.CheckServicesReadiness(db, cache, queue)
    }),
).Handle)
```

### Startup

- Question: has initialization completed?
- Failure can delay liveness checks while a slow application starts.
- Often used with a startup grace period in the orchestrator.

```go
app.MapGet(health_check.StartupEndpoint, health_check.New(
    health_check.WithProbe(func(ctx *moon.Context) bool {
        return appState.Loaded()
    }),
).Handle)
```

## Examples

### JSON readiness response

```go
app.MapGet(health_check.ReadinessEndpoint, health_check.New(
    health_check.WithProbe(func(ctx *moon.Context) bool {
        return db.IsReady()
    }),
    health_check.WithResponse(func(ctx *moon.Context, ok bool) error {
        if !ok {
            return ctx.WriteJson(http.StatusServiceUnavailable, moon.Object{
                "status": "unhealthy",
                "dependency": "database",
            })
        }
        return ctx.WriteJson(http.StatusOK, moon.Object{
            "status": "healthy",
        })
    }),
).Handle)
```

### Using dependencies and services

```go
app.AddDependency(cfg)
app.AddService(db)

app.MapGet(health_check.ReadinessEndpoint, health_check.New(
    health_check.WithProbe(func(ctx *moon.Context) bool {
        if !ctx.GetDependency[Config]().HealthChecksEnabled {
            return true
        }
        return ctx.GetService[*Database]().IsReady()
    }),
).Handle)
```

`GetService` panics if no service of that type was registered or if it was
registered but never started. In tests, call `app.StartServices()` before
exercising handlers that use services.

### Kubernetes-style probes

```yaml
livenessProbe:
  httpGet:
    path: /livez
    port: 3000
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 3000
  periodSeconds: 5

startupProbe:
  httpGet:
    path: /startupz
    port: 3000
  failureThreshold: 30
  periodSeconds: 2
```

Register matching routes, with `readinessProbe` and `startupProbe` defined by your
application:

```go
app.MapGet("/livez", health_check.New().Handle)
app.MapGet("/readyz", health_check.New(
    health_check.WithProbe(readinessProbe),
).Handle)
app.MapGet("/startupz", health_check.New(
    health_check.WithProbe(startupProbe),
).Handle)
```

### HEAD support

If a probe uses HEAD, the default response writes only a status code and no body,
which is safe. A custom response must respect HEAD:

```go
health_check.WithResponse(func(ctx *moon.Context, ok bool) error {
    code := http.StatusOK
    if !ok {
        code = http.StatusServiceUnavailable
    }
    if ctx.GetMethod() == moon.MethodHead {
        ctx.SetStatusCode(code)
        return nil
    }
    return ctx.WriteJson(code, moon.Object{"ok": ok})
})
```

## Security notes

- Health endpoints often reveal dependency names and internal state. Expose them
  only on trusted networks or protect them with authentication.
- Do not include secrets, connection strings or stack traces in probe responses.
- Keep probes cheap. An unauthenticated endpoint that performs expensive database
  work can be abused.
- Separate liveness from readiness. Letting liveness depend on every downstream
  service can cause cascading restarts.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithProbe(f func(ctx *moon.Context) bool) Option
func WithResponse(f func(ctx *moon.Context, ok bool) error) Option
```

Constants:

```go
const (
    LivenessEndpoint  = "/livez"
    ReadinessEndpoint = "/readyz"
    StartupEndpoint   = "/startupz"
)
```

## Limitations

- The handler performs one probe per request. It does not cache or rate-limit
  probe results.
- The default response has no body and no content type.
- The handler does not call `Next`, so it cannot be combined with a later route
  handler in the same request.
- Probe errors are reported as a boolean; use the response function and logging
  for details.
