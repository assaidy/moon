# API key auth handler

Package `key_auth` authenticates requests with an API key. The key is read from
a configurable source, validated by a callback, and a `401` challenge is sent
when it is missing or rejected.

```go
app.UseAll(key_auth.New(key_auth.WithValidator(validate)).Handle)
```

API keys are bearer secrets: anyone who has the key can use it. Send them over
TLS and treat them like passwords.

## Installation

```go
import "github.com/assaidy/moon/handlers/key_auth"
```

## Quick start

```go
func validate(ctx *moon.Context, key string) (bool, error) {
    return subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) == 1, nil
}

app.UseAll(key_auth.New(key_auth.WithValidator(validate)).Handle)
```

By default the key is read from the `Authorization` header:

```http
Authorization: Bearer <key>
```

Read the key from `X-API-Key` instead:

```go
app.UseAll(key_auth.New(
    key_auth.WithKeySource(key_auth.SourceXApiKeyHeader()),
    key_auth.WithValidator(validate),
).Handle)
```

Read it from a cookie:

```go
app.UseAll(key_auth.New(
    key_auth.WithKeySource(key_auth.SourceCookie("session")),
    key_auth.WithValidator(validate),
).Handle)
```

Use a custom source and challenge:

```go
app.UseAll(key_auth.New(
    key_auth.WithKeySource(key_auth.KeySource{
        Extractor: func(ctx *moon.Context) string {
            return ctx.GetHeader("X-Custom-Key")
        },
        Challenge: key_auth.Challenge{
            Scheme: "Bearer",
            Realm:  "Internal API",
            Error:  "invalid_token",
        },
    }),
    key_auth.WithValidator(validate),
).Handle)
```

## How it works

For each request that is not skipped:

1. Extract the key with the configured source's `Extractor`.
2. An empty key gets `401` with the configured challenge; the validator does
   not run.
3. Call the validator.
4. A validator error gets `500`; `(false, nil)` gets `401` with the challenge.
5. On success the chain continues.

The handler does not store the key in the context. Store request state in the
validator yourself.

### `SourceAuthorizationHeader`

Parses `Authorization: <scheme> <key>`:

- the scheme comparison is case-insensitive;
- the scheme passed to `SourceAuthorizationHeader` is used for extraction and
  as the challenge scheme;
- an empty scheme defaults to `"ApiKey"`;
- exactly one token must follow the scheme. A bare scheme or extra fields yield
  an empty key, which is treated as missing.

### `SourceXApiKeyHeader`

Reads the exact value of the `X-API-Key` request header.

### `SourceCookie`

Reads the named cookie with `ctx.GetCookie`. The name is trimmed and must not
be empty.

## Challenge

A `401` response carries the `WWW-Authenticate` value generated from the
source's `Challenge`:

```http
WWW-Authenticate: Bearer realm="Restricted"
```

| Field              | Default        | Behavior                                                                   |
| ------------------ | -------------- | -------------------------------------------------------------------------- |
| `Scheme`           | `"ApiKey"`     | Trimmed, must be a valid HTTP token, written unquoted.                     |
| `Realm`            | `"Restricted"` | Trimmed, written as a quoted-string. `"` and `\` are escaped.              |
| `Error`            | empty          | Omitted when empty. Must use the RFC 6749 error-value character set.       |
| `ErrorDescription` | empty          | Omitted when empty. Must use the RFC 6749 error-description character set. |
| `ErrorUrl`         | empty          | Omitted when empty. Must be an absolute URI.                               |
| `Scope`            | empty          | Omitted when empty. Must be space-delimited RFC 6749 scope tokens.         |

Empty optional parameters are not added. Control characters are rejected
because they cannot be carried safely in a header value. All values are
trimmed before validation.

Example with every field set:

```http
WWW-Authenticate: Bearer realm="example", error="invalid_token", error_description="The access token expired", error_uri="https://example.com/errors/invalid-token", scope="read write"
```

The same challenge is sent whether the key was missing or rejected. RFC 6750
suggests omitting `error` when no credentials were presented, but this handler
keeps the challenge uniform and configurable.

## Configuration

All options are passed to `key_auth.New`.

| Option                                                             | Default                               | Description                                                          |
| ------------------------------------------------------------------ | ------------------------------------- | -------------------------------------------------------------------- |
| `WithValidator(func(ctx *moon.Context, key string) (bool, error))` | required                              | Validates the extracted key; a nil func panics.                      |
| `WithKeySource(KeySource)`                                         | `SourceAuthorizationHeader("Bearer")` | Selects where the key is read from and the challenge.                |
| `WithSkip(func(*moon.Context) bool)`                               | `nil`                                 | Requests for which the predicate returns true bypass authentication. |

### `WithValidator`

```go
key_auth.WithValidator(func(ctx *moon.Context, key string) (bool, error) {
    return key == "secret", nil
})
```

The validator receives the request context and the extracted key. The return
values mean:

| Result         | Behavior                                                         |
| -------------- | ---------------------------------------------------------------- |
| `(true, nil)`  | The key is accepted and the chain runs.                          |
| `(false, nil)` | The key is rejected; the handler responds with `401`.            |
| `(_, err)`     | The decision could not be made; the handler responds with `500`. |

Use an error for infrastructure failures such as an unreachable key store, not
for rejecting a key. Rejection is `(false, nil)`.

The validator is required; `New` panics when it is missing or nil.

Compare secrets in constant time:

```go
func validate(ctx *moon.Context, key string) (bool, error) {
    return subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) == 1, nil
}
```

### `WithKeySource`

```go
key_auth.WithKeySource(key_auth.SourceXApiKeyHeader())
```

Sets the `KeySource`: the extractor and the challenge. The extractor is
required, and the challenge is normalized and validated when the option is
applied.

Predefined sources:

```go
key_auth.SourceAuthorizationHeader("Bearer")
key_auth.SourceXApiKeyHeader()
key_auth.SourceCookie("session")
```

For anything else, build a `KeySource` literal with your own `Extractor`.

### `WithSkip`

```go
key_auth.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetPath() == "/healthz"
})
```

When the predicate returns true:

- no key is required;
- the validator does not run;
- the chain continues normally.

For route-scoped protection, `app.Use` with a prefix is usually clearer than a
skip predicate.

Default: `nil`.

## Examples

### Static key

```go
const apiKey = "s3cret"

func validate(ctx *moon.Context, key string) (bool, error) {
    return subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) == 1, nil
}

app.UseAll(key_auth.New(key_auth.WithValidator(validate)).Handle)
```

### Database lookup with errors

```go
func validate(ctx *moon.Context, key string) (bool, error) {
    hash, err := keys.Hash(ctx, key)
    if err != nil {
        return false, err // answered with 500
    }
    return hash != "", nil // unknown key, answered with 401
}
```

### Protect a route prefix

```go
h := key_auth.New(
    key_auth.WithKeySource(key_auth.SourceXApiKeyHeader()),
    key_auth.WithValidator(validate),
)
app.Use("/admin", h.Handle)
```

Requests to `/admin` and paths below it require a key. Other paths are
untouched. Register the middleware before the routes it should protect.

### Public routes and protected routes

If the middleware is global, use `WithSkip` for public paths:

```go
app.UseAll(key_auth.New(
    key_auth.WithKeySource(key_auth.SourceXApiKeyHeader()),
    key_auth.WithValidator(validate),
    key_auth.WithSkip(func(ctx *moon.Context) bool {
        switch ctx.GetPath() {
        case "/", "/healthz":
            return true
        default:
            return false
        }
    }),
).Handle)
```

## Security notes

- **Use TLS.** An API key is a bearer secret; anyone who sees it can use it.
- **Compare in constant time.** Use `crypto/subtle` or a keyed comparison to
  avoid leaking secrets through timing.
- **Store hashes, not plaintext keys.** The validator receives the plaintext
  key, so compare it against a hashed store.
- **Rate-limit authentication.** The handler does not count failures, lock
  accounts or throttle clients.
- **Do not log keys.** The handler never logs the key or the source header;
  keep it that way in your validator and handlers.
- **Cookies need CSRF protection.** Browser cookies are attached automatically
  to matching requests. Protect state-changing endpoints with CSRF tokens,
  `SameSite` cookies or other origin checks.
- **Skip predicates are security-sensitive.** A predicate that skips
  authentication based on a client-controlled value can create a bypass. Base
  it on trusted state.
- **A single challenge is not an authorization decision.** `realm`, `error` and
  `scope` describe the protection space; authorization still belongs in your
  application.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

func SourceAuthorizationHeader(scheme string) KeySource
func SourceXApiKeyHeader() KeySource
func SourceCookie(name string) KeySource

type Handler struct { /* ... */ }
type Option func(*options)

type KeySource struct {
    Extractor func(ctx *moon.Context) string
    Challenge Challenge
}

type Challenge struct {
    Scheme           string
    Realm            string
    Error            string
    ErrorDescription string
    ErrorUrl         string
    Scope            string
}
```

Options:

```go
func WithSkip(f func(ctx *moon.Context) bool) Option
func WithKeySource(s KeySource) Option
func WithValidator(f func(ctx *moon.Context, key string) (bool, error)) Option
```

## Limitations

- One source per handler. There is no built-in fallback from `Authorization` to
  `X-API-Key` or a cookie; use a custom extractor if you need that.
- The `Authorization` source expects exactly one token after the scheme. Extra
  fields make the key look missing and get `401`, not `400`.
- Malformed keys are not distinguished from missing keys; both get the same
  challenge.
- No built-in brute-force protection, account lockout or audit logging.
- No key cache or session. The validator runs on every request, so keep it
  fast.
- No query-parameter or request-body sources; use a custom extractor if needed.
- The handler adds no `Cache-Control` or `Vary` headers to the `401` response.
