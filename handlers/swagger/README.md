# Swagger handler

Package `swagger` serves an OpenAPI document generated from a moon app's route
specs, together with a Swagger UI page that renders it.

```go
app.MapGet("/swagger/*", swagger.New(
    swagger.WithTitle("My API"),
    swagger.WithVersion("1.0.0"),
).Handle)
```

The handler turns the documentation metadata attached with
`moon.WithApiSpec` into an OpenAPI 3.2.1 document and serves a Swagger UI page for
it.

## Installation

```go
import "github.com/assaidy/moon/handlers/swagger"
```

## Quick start

Register the handler on a wildcard route:

```go
app.MapGet("/swagger/*", swagger.New(
    swagger.WithTitle("Example API"),
    swagger.WithVersion("1.0.0"),
).Handle)
```

Open:

- `/swagger` or `/swagger/index.html` for the UI;
- `/swagger/openapi.json` for the generated document.

Document a route with `moon.WithApiSpec`:

```go
app.MapGet("/users/:id", getUser,
    moon.WithId("get-user"),
    moon.WithApiSpec(moon.NewApiSpec(
        moon.WithSummary("Get a user"),
        moon.WithOperationId("getUser"),
        moon.WithTags("users"),
        moon.WithParameter(moon.ApiParameter{
            Name:   "id",
            In:     moon.ApiParamInPath,
            Schema: moon.Object{"type": "string"},
        }),
        moon.WithResponse(moon.ApiResponse{
            Status:      http.StatusOK,
            Description: "The requested user.",
        }),
        moon.WithResponse(moon.ApiResponse{
            Status:      http.StatusNotFound,
            Description: "No user with that ID.",
        }),
    )),
)
```

## Mounting

The default mount path is `/swagger`. Register the handler with a wildcard so the
UI, the index page and the document all reach it:

```go
app.MapGet("/swagger/*", swagger.New().Handle)
```

A custom mount:

```go
app.MapGet("/docs/*", swagger.New(
    swagger.WithMountPath("/docs"),
    swagger.WithTitle("Example API"),
).Handle)
```

Served paths for a mount of `/docs`:

| Path | Content |
| --- | --- |
| `/docs` | Swagger UI page |
| `/docs/index.html` | Swagger UI page |
| `/docs/openapi.json` | OpenAPI document |

Matching ignores letter case by default, so `/DOCS/INDEX.HTML` serves the same
page. Pass `WithCaseSensitiveMountMatching(true)` to require exact casing for the
whole mounted path, including the mount segments and both file names.

The handler can also be used as middleware. Paths outside the mount call
`ctx.Next()`, so the rest of the chain can handle them. When no handler follows,
`Next` returns `moon.ErrNotFound`.

## Generation and caching

The document and the UI are built on first use and cached per handler:

- the first request for the UI builds and caches the HTML;
- the first request for the document builds and caches the JSON.

This means:

1. Register every route, including the Swagger route itself, before the first
   request to the handler.
2. Routes registered after the first request are not reflected in the cached
   document.
3. Use one `Handler` per app. The cache belongs to the handler, not the app.

The document is built from `Context.GetApiSpecs`, which returns a snapshot of all
routes in registration order.

## Documented fields

`moon.NewApiSpec` maps directly to OpenAPI operation fields:

| Option | OpenAPI field |
| --- | --- |
| `WithSummary` | `summary` |
| `WithDescription` | `description` |
| `WithOperationId` | `operationId` |
| `WithTags` | `tags` |
| `WithDeprecated` | `deprecated` |
| `WithParameter` | `parameters` |
| `WithRequestBody` | `requestBody` |
| `WithResponse` | `responses` |
| `WithSecurity` | `security` |
| `WithExtra` | Raw operation fields, merged last |

Routes without a spec still appear in the document. They have an empty operation
with a default response.

### Parameters

Documented parameters are emitted in the order given. Path parameters are always
required. Path parameters in the spec must exist in the route pattern, including
generated wildcard names such as `*`, `*1`, `+` and `+1`.

Path parameters that are present in the route pattern but missing from the
parameter list are inferred automatically as required string parameters.

```go
app.MapGet("/users/:id/posts/:post_id", getPost,
    moon.WithApiSpec(moon.NewApiSpec(
        moon.WithParameter(moon.ApiParameter{
            Name: "id",
            In:   moon.ApiParamInPath,
            Schema: moon.Object{"type": "string"},
        }),
        // post_id is inferred.
    )),
)
```

Parameter locations:

```go
moon.ApiParamInPath
moon.ApiParamInQuery
moon.ApiParamInHeader
moon.ApiParamInCookie
```

A parameter schema defaults to `{"type": "string"}` when nil. `WithParameter`
appends; repeated calls accumulate.

### Request body

```go
moon.WithRequestBody(moon.ApiRequestBody{
    Description: "The user to create.",
    Required:    true,
    Content: map[string]moon.ApiMediaType{
        "application/json": {
            Schema: moon.Object{
                "type": "object",
                "required": []string{"name"},
                "properties": moon.Object{
                    "name": moon.Object{"type": "string"},
                },
            },
        },
    },
})
```

A request body is emitted only when its `Content` map is non-empty.

### Responses

```go
moon.WithResponse(moon.ApiResponse{
    Status:      http.StatusCreated,
    Description: "The user was created.",
    Content: map[string]moon.ApiMediaType{
        "application/json": {
            Schema: moon.Object{"type": "object"},
        },
    },
    Headers: map[string]moon.ApiHeader{
        "Location": {
            Description: "URL of the created user.",
            Schema:      moon.Object{"type": "string"},
        },
    },
})
```

- A `Status` of zero means the default response.
- When description is empty, the HTTP status text is used for numeric statuses.
- When no responses are configured, the document emits a `default` response with
  the description `"Undocumented response."`.
- Response header names must be unique case-insensitively; styles other than
  `"simple"` panic.
- `Example` and `Examples` are mutually exclusive for a media type.

### Security and components

```go
app := moon.New()

app.MapGet("/docs/*", swagger.New(
    swagger.WithComponents(map[string]any{
        "securitySchemes": map[string]any{
            "bearerAuth": map[string]any{
                "type":         "http",
                "scheme":       "bearer",
                "bearerFormat": "JWT",
            },
        },
    }),
).Handle)

app.MapGet("/private", handler,
    moon.WithApiSpec(moon.NewApiSpec(
        moon.WithSecurity(moon.ApiSecurityRequirement{
            "bearerAuth": []string{},
        }),
    )),
)
```

`WithComponents` shallow-clones the map; nested values are used as-is. Components
can hold schemas, security schemes, parameters, responses and other reusable
OpenAPI objects. The handler documents the security requirement; it does not
enforce authentication.

`WithExtra` merges raw operation fields last and can override the generated
fields:

```go
moon.WithExtra("x-internal", true)
```

## Path generation

Moon patterns are converted to OpenAPI path templates:

| Moon pattern | OpenAPI path |
| --- | --- |
| `/users/:id` | `/users/{id}` |
| `/files/*` | `/files/{*}` |
| `/files/*/*` | `/files/{*1}/{*2}` |
| `/files/+` | `/files/{+}` |
| `/files/:id?` | `/files/{id}` and `/files` variants |

Literal parts and escapes are converted to their literal text.

### Optional parameters

A route with optional path parameters is expanded into separate OpenAPI paths, one
per combination of present and omitted optional parameters.

- 0 optional parameters: one path.
- 1 to 3 optional parameters: all `2^n` combinations, up to 8 variants.
- 4 or more optional parameters: only the variant with every optional parameter
  present.

For omitted-parameter variants, the operation ID gets a suffix listing omitted
names in sorted order:

```text
getUser_without_org_and_team
```

If no operation ID is configured, the variant keeps an empty operation ID.

Documented path parameters for omitted optional parameters are filtered out of
that variant. Inferred path parameters follow the same rule.

### Path collisions

Routes that produce the same OpenAPI path are merged into one Path Item when their
methods differ. If two routes produce the same path and method, the first
operation is kept and the later one is ignored.

Routes that describe the same path shape with different parameter names produce
different OpenAPI paths and therefore separate Path Items. This can make the
document ambiguous or invalid. Use consistent parameter names for the same path
shape.

`CONNECT` operations are emitted under the Path Item's
`additionalOperations` object, because OpenAPI does not define a top-level
`connect` field.

## Configuration

| Option | Default | Description |
| --- | --- | --- |
| `WithMountPath(string)` | `/swagger` | Base path for the UI and document. |
| `WithCaseSensitiveMountMatching(bool)` | `false` | Requires exact letter case for the whole mounted path. |
| `WithTitle(string)` | `"API Documentation"` | Document title and UI page title. |
| `WithVersion(string)` | `"1.0.0"` | Version of the described API. |
| `WithDescription(string)` | empty | API description in the `info` object. |
| `WithServerUrl(string)` | empty | Adds a server URL to the document. |
| `WithComponents(map[string]any)` | empty | OpenAPI components object. |
| `WithUiAssets(cssUrl, jsUrl)` | Swagger UI 5.34.0 from unpkg | Overrides the UI stylesheet and script URLs. |

### `WithMountPath`

Sets the base path for the UI and document. The value is trimmed of surrounding
whitespace, preserves its letter case, gets a leading `/` when missing, and is
cleaned with `path.Clean`. An empty value panics.

For a mount of `/`, the UI is served at `/` and the document at `/openapi.json`.

Default: `"/swagger"`.

### `WithCaseSensitiveMountMatching`

Requires exact letter case when matching the request path against the mount
path. It applies to the whole mounted path: the mount segments, `index.html`
and `openapi.json`.

Default: `false` (case-insensitive matching).

### `WithTitle`

Sets both the OpenAPI `info.title` and the HTML page title. Trimmed; empty panics.

Default: `"API Documentation"`.

### `WithVersion`

Sets `info.version`, the version of the described API, not the OpenAPI version.
Trimmed; empty panics.

Default: `"1.0.0"`.

### `WithDescription`

Sets `info.description`. The text is stored as given and may contain Markdown.

Default: empty.

### `WithServerUrl`

Adds a `servers` entry:

```go
swagger.WithServerUrl("https://api.example.com")
```

The URL may be absolute or relative. When unset, no `servers` array is emitted and
Swagger UI uses the origin serving the document. Trimmed; empty panics.

Default: empty.

### `WithComponents`

Sets the OpenAPI `components` object. The map is shallow-cloned; nested values are
shared as-is.

Default: empty.

### `WithUiAssets`

Overrides the stylesheet and script URLs. Use it to self-host Swagger UI instead
of loading assets from unpkg. Both URLs are trimmed and must be non-empty.

Default:

```text
https://unpkg.com/swagger-ui-dist@5.34.0/swagger-ui.css
https://unpkg.com/swagger-ui-dist@5.34.0/swagger-ui-bundle.js
```

## Examples

### Basic API documentation

```go
app.MapGet("/swagger/*", swagger.New(
    swagger.WithTitle("Example API"),
    swagger.WithVersion("2.1.0"),
    swagger.WithDescription("Internal API for the example service."),
).Handle)
```

### Documented route

```go
type CreateUserRequest struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

app.MapPost("/users", createUser,
    moon.WithApiSpec(moon.NewApiSpec(
        moon.WithSummary("Create a user"),
        moon.WithDescription("Creates a user and returns the stored record."),
        moon.WithOperationId("createUser"),
        moon.WithTags("users"),
        moon.WithRequestBody(moon.ApiRequestBody{
            Required: true,
            Content: map[string]moon.ApiMediaType{
                "application/json": {
                    Schema: moon.Object{
                        "type":     "object",
                        "required": []string{"name", "email"},
                        "properties": moon.Object{
                            "name":  moon.Object{"type": "string"},
                            "email": moon.Object{"type": "string", "format": "email"},
                        },
                    },
                },
            },
        }),
        moon.WithResponse(moon.ApiResponse{
            Status:      http.StatusCreated,
            Description: "The user was created.",
        }),
        moon.WithResponse(moon.ApiResponse{
            Status:      http.StatusConflict,
            Description: "A user with that email already exists.",
        }),
    )),
)
```

### Self-hosted Swagger UI

```go
app.MapGet("/docs/*", swagger.New(
    swagger.WithMountPath("/docs"),
    swagger.WithUiAssets(
        "/assets/swagger-ui.css",
        "/assets/swagger-ui-bundle.js",
    ),
).Handle)

app.MapGet("/assets/*", static.New(
    static.WithRoot("./swagger-ui"),
    static.WithStripPathPrefix("/assets"),
).Handle)
```

The asset routes must be registered and reachable by the browser.

### Multiple optional path parameters

```go
app.MapGet("/reports/:year?/:month?", getReport,
    moon.WithApiSpec(moon.NewApiSpec(
        moon.WithOperationId("getReport"),
        moon.WithParameter(moon.ApiParameter{
            Name: "year",
            In:   moon.ApiParamInPath,
        }),
        moon.WithParameter(moon.ApiParameter{
            Name: "month",
            In:   moon.ApiParamInPath,
        }),
    )),
)
```

This generates four paths:

- `/reports`
- `/reports/{year}`
- `/reports/{month}`
- `/reports/{year}/{month}`

with operation IDs derived from the omitted combinations.

### Custom components

```go
app.MapGet("/docs/*", swagger.New(
    swagger.WithComponents(map[string]any{
        "schemas": map[string]any{
            "User": map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "id":   map[string]any{"type": "string"},
                    "name": map[string]any{"type": "string"},
                },
            },
        },
    }),
).Handle)
```

Reference the component from a schema:

```go
Schema: moon.Object{"$ref": "#/components/schemas/User"}
```

## Security notes

- The document describes every registered route, including internal routes. Do not
  expose the Swagger handler on a public network unless the route list is safe to
  publish.
- The default UI loads JavaScript and CSS from unpkg. If your Content Security
  Policy blocks third-party scripts, self-host the assets with `WithUiAssets`.
- The Swagger UI page is not authenticated. Protect the route with middleware if
  the API documentation is private.
- Security requirements in the document are descriptive only. Enforce
  authentication and authorization in handlers.
- Avoid putting secrets, internal hostnames or sensitive examples in descriptions,
  schemas or examples.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithMountPath(mountPath string) Option
func WithCaseSensitiveMountMatching(b bool) Option
func WithTitle(title string) Option
func WithVersion(version string) Option
func WithDescription(description string) Option
func WithServerUrl(serverUrl string) Option
func WithComponents(components map[string]any) Option
func WithUiAssets(cssUrl, jsUrl string) Option
```

Constants:

```go
const (
    OpenApiVersion   = "3.2.1"
    DefaultMountPath = "/swagger"
    SpecFileName     = "openapi.json"
)
```

## Limitations

- The handler generates OpenAPI 3.2.1 only.
- The document model covers `info`, `servers`, `paths` and `components`; other
  top-level OpenAPI fields are not configurable.
- Schemas are not generated from Go types. Provide them through `moon.ApiMediaType`
  and `moon.ApiParameter`.
- The handler does not validate requests or responses against the document.
- The document and UI are cached after the first request; late route registration is
  not reflected.
- Optional path parameters expand to at most 8 variants. Routes with 4 or more
  optional parameters are documented only with every optional parameter present.
- Same path shapes with different parameter names produce separate Path Items.
- The first route wins when two routes produce the same path and method.
- Security requirements are documentation only and are not enforced.
