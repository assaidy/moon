# Basic auth handler

Package `basic_auth` implements HTTP Basic authentication (RFC 7617) on top of
the HTTP authentication framework (RFC 9110). It reads the `Authorization`
request header, validates the credentials with a callback, and stores the
authenticated user-id in a context local.

```go
app.UseAll(basic_auth.New(basic_auth.WithAuthorizer(authorize)).Handle)
```

Basic authentication transmits the password in cleartext; base64 is only an
encoding. Use it over TLS.

## Installation

```go
import "github.com/assaidy/moon/handlers/basic_auth"
```

## Quick start

```go
func authorize(user, password string, ctx *moon.Context) (bool, error) {
    return user == "admin" && password == "secret", nil
}

app.UseAll(basic_auth.New(basic_auth.WithAuthorizer(authorize)).Handle)
```

Read the authenticated user in a handler:

```go
app.MapGet("/me", func(ctx *moon.Context) error {
    user, ok := ctx.GetLocal[string](basic_auth.DefaultLocalKey)
    if !ok {
        return moon.ErrUnauthorized
    }
    return ctx.WriteJson(http.StatusOK, map[string]string{"user": user})
})
```

Use a custom realm:

```go
app.UseAll(basic_auth.New(
    basic_auth.WithAuthorizer(authorize),
    basic_auth.WithRealm("Admin area"),
).Handle)
```

## How it works

For each request that is not skipped:

1. Read the `Authorization` header. More than one field line is rejected with
   `400`, because `Authorization` is a singleton field (RFC 9110 §11.6.2).
2. Missing or empty credentials get `401` with a challenge.
3. Enforce `WithHeaderLimit`: longer values get `431`.
4. Parse `auth-scheme [ 1*SP token68 ]` (RFC 9110 §11.4). An invalid scheme
   token or `token68` gets `400`; a bare scheme with no credentials gets `401`
   as partial credentials.
5. Match the scheme case-insensitively. A different scheme gets `401` with a
   Basic challenge.
6. Base64-decode the token. Both padded and unpadded standard base64 are
   accepted, because `token68` allows either (RFC 9110 §11.2).
7. Split the decoded user-pass at the first colon. The text after it is the
   password, so passwords may contain colons; user-ids cannot.
8. Require valid UTF-8 and reject ASCII control characters (RFC 7617 §2).
9. Call the authorizer.
10. On success, store the user-id under the configured local key and run the
    chain.

### Challenge

Every `401` response carries:

```http
WWW-Authenticate: Basic realm="Restricted", charset="UTF-8"
Cache-Control: no-store
Vary: Authorization
```

- `realm` is required by RFC 7617 §2 and is written as an RFC 9110
  quoted-string: quotes and backslashes are escaped.
- `charset="UTF-8"` is the only value RFC 7617 §2.1 defines. It is advisory, but
  this handler enforces it by rejecting credentials that are not valid UTF-8.
- `Cache-Control: no-store` keeps the `401` challenge out of shared caches.
- `Vary: Authorization` makes any cache that stores the response key on the
  header that selects the identity.

These headers are set on the `401` response; successful responses are left to
the downstream handler.

### Status codes

| Status | When |
| --- | --- |
| `400 Bad Request` | Malformed field syntax: invalid scheme token, invalid `token68`, or more than one `Authorization` field line. |
| `401 Unauthorized` | Missing, partial, undecodable or rejected credentials, including a non-`Basic` scheme, a missing colon, invalid UTF-8, control characters, or an authorizer that returns `false`. |
| `431 Request Header Fields Too Large` | The header value is longer than `WithHeaderLimit`. |
| `500 Internal Server Error` | The authorizer returned an error. |
| Other | Credentials were accepted; the downstream handler decides the response. |

## Configuration

All options are passed to `basic_auth.New`.

| Option | Default | Description |
| --- | --- | --- |
| `WithAuthorizer(func(user, password string, ctx *moon.Context) (bool, error))` | required | Validates credentials; a nil func panics. |
| `WithRealm(string)` | `"Restricted"` | Realm advertised in the challenge. Trimmed; empty or control characters panic. |
| `WithHeaderLimit(int)` | `-1` | Maximum `Authorization` value length in bytes. Positive values cap; negative disables; zero panics. |
| `WithLocalKey(string)` | `DefaultLocalKey` | Context local key holding the user-id, used as-is. Empty panics. |
| `WithSkip(func(*moon.Context) bool)` | `nil` | Requests for which the predicate returns true bypass authentication. |

### `WithAuthorizer`

```go
basic_auth.WithAuthorizer(func(user, password string, ctx *moon.Context) (bool, error) {
    return user == "admin" && password == "secret", nil
})
```

Sets the function that validates credentials. It receives the decoded user-id
and password, plus the request context. The return values mean:

| Result | Behavior |
| --- | --- |
| `(true, nil)` | Credentials accepted; the user-id is stored and the chain runs. |
| `(false, nil)` | Credentials rejected; the handler responds with `401` and a challenge. |
| `(_, err)` | The decision could not be made; the handler responds with `500`. |

An error is for infrastructure failures such as an unreachable user store. It is
not a way to reject a user: use `(false, nil)` for that. This distinction keeps a
backend outage from being mistaken for bad credentials and from prompting the
client to retry its password.

The authorizer is required; `New` panics when it is missing or nil. It runs for
every request that reaches the credential check, after the header has been
parsed and validated, so it never sees malformed base64, invalid UTF-8 or
control characters.

Use a constant-time comparison for secrets:

```go
func authorize(user, password string, ctx *moon.Context) (bool, error) {
    want, ok := users[user]
    if !ok {
        return false, nil
    }
    return subtle.ConstantTimeCompare([]byte(password), []byte(want)) == 1, nil
}
```

Default: none (required).

### `WithRealm`

```go
basic_auth.WithRealm("Admin area")
```

Sets the realm advertised in the `WWW-Authenticate` challenge. Surrounding
whitespace is trimmed.

The value is written as an RFC 9110 quoted-string, so `"` and `\` are escaped
automatically. Control characters cannot appear in a quoted-string even in
escaped form, so an empty value or one containing them panics.

A realm identifies a protection space; it is not an authorization boundary.
Non-ASCII realms are accepted as `obs-text`, but RFC 7617 §3 notes they cannot be
transported reliably, so prefer ASCII.

Default: `"Restricted"`.

### `WithHeaderLimit`

```go
basic_auth.WithHeaderLimit(4096)
```

Caps the length of the `Authorization` header value in bytes. A longer value is
rejected with `431 Request Header Fields Too Large`. The limit covers the whole
value, including the `Basic ` prefix and base64 padding.

A negative value disables the cap. Zero is not accepted: the cap applies only to
positive values, so zero would silently mean "no limit" instead of being an
explicit choice.

Default: `-1` (no limit).

### `WithLocalKey`

```go
h := basic_auth.New(
    basic_auth.WithAuthorizer(authorize),
    basic_auth.WithLocalKey("app.user"),
)
app.UseAll(h.Handle)

user, _ := ctx.GetLocal[string]("app.user")
```

Sets the context local key under which the authenticated user-id is stored. The
key is used as-is, including surrounding whitespace; an empty key panics.

Only the user-id is stored, never the password.

Default: `basic_auth.DefaultLocalKey`.

### `WithSkip`

```go
basic_auth.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetPath() == "/healthz"
})
```

When the predicate returns true:

- no credentials are required;
- the authorizer does not run;
- nothing is stored in the context;
- the chain continues normally.

For route-scoped protection, `app.Use` with a prefix is usually clearer than a
skip predicate.

Default: `nil`.

## Examples

### Static users

```go
var users = map[string]string{
    "admin": "s3cret",
    "alice": "hunter2",
}

func authorize(user, password string, ctx *moon.Context) (bool, error) {
    want, ok := users[user]
    if !ok {
        return false, nil
    }
    return subtle.ConstantTimeCompare([]byte(password), []byte(want)) == 1, nil
}

app.UseAll(basic_auth.New(basic_auth.WithAuthorizer(authorize)).Handle)
```

Store hashes, not plaintext passwords, in real applications.

### Database lookup with errors

```go
func authorize(user, password string, ctx *moon.Context) (bool, error) {
    hash, err := users.PasswordHash(ctx, user)
    if err != nil {
        return false, err // answered with 500
    }
    if hash == "" {
        return false, nil // unknown user, answered with 401
    }
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}
```

### Protect a route prefix

```go
h := basic_auth.New(
    basic_auth.WithAuthorizer(authorize),
    basic_auth.WithRealm("Admin area"),
)
app.Use("/admin", h.Handle)
```

Requests to `/admin` and paths below it require credentials. Other paths are
untouched. Register the middleware before the routes it should protect:
`app.Use` applies to routes registered after the call.

### Read the user downstream

```go
app.UseAll(basic_auth.New(
    basic_auth.WithAuthorizer(authorize),
    basic_auth.WithLocalKey("app.user"),
).Handle)

app.MapGet("/me", func(ctx *moon.Context) error {
    user, ok := ctx.GetLocal[string]("app.user")
    if !ok {
        return moon.ErrUnauthorized
    }
    return ctx.WriteJson(http.StatusOK, map[string]string{"user": user})
})
```

### Public routes and protected routes

Prefer scoping the middleware:

```go
app.Use("/account", basic_auth.New(basic_auth.WithAuthorizer(authorize)).Handle)
```

If the middleware is global, use `WithSkip` for public paths:

```go
app.UseAll(basic_auth.New(
    basic_auth.WithAuthorizer(authorize),
    basic_auth.WithSkip(func(ctx *moon.Context) bool {
        switch ctx.GetPath() {
        case "/", "/login", "/healthz":
            return true
        default:
            return false
        }
    }),
).Handle)
```

## Security notes

- **Use TLS.** Basic credentials are only base64-encoded, not encrypted. Without
  TLS, anyone on the path can read the password.
- **Store password hashes.** The authorizer receives the plaintext password, so
  compare it against bcrypt, scrypt or Argon2 output, never a plaintext store.
- **Compare in constant time.** Use `crypto/subtle` or the password hashing
  library's comparison to avoid leaking secrets through timing.
- **Rate-limit authentication.** The handler does not count failures or lock
  accounts. Add throttling or lockout where credentials are accepted.
- **Do not log credentials.** The handler never logs the `Authorization` header
  or the password; keep it that way in your authorizer and handlers.
- **A realm is not a security boundary.** It identifies a protection space and
  is displayed to users. Authorization still belongs in your application.
- **Browsers cache Basic credentials.** There is no logout endpoint; credentials
  are reused for the realm until the browser discards them. Prefer short-lived
  accounts or an additional session layer when logout matters.
- **Watch out for CSRF.** Browsers attach cached Basic credentials to matching
  requests automatically. Protect state-changing endpoints with CSRF tokens,
  custom headers or other origin checks.
- **User enumeration.** A visible difference between "unknown user" and "wrong
  password" leaks account existence. Keep the authorizer's timing and responses
  uniform if that matters.
- **UTF-8 only, no normalization.** Non-UTF-8 credentials are rejected. The
  handler does not apply NFC, so normalize usernames when creating accounts if
  you need canonical comparisons.
- **Skip predicates are security-sensitive.** A predicate that skips
  authentication based on a client-controlled value can create a bypass. Base it
  on trusted state.

## API reference

```go
const DefaultLocalKey = "moon.handlers.basic_auth.local_key"

func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithSkip(f func(ctx *moon.Context) bool) Option
func WithAuthorizer(f func(user, password string, ctx *moon.Context) (bool, error)) Option
func WithHeaderLimit(i int) Option
func WithRealm(s string) Option
func WithLocalKey(s string) Option
```

## Limitations

- Only the Basic scheme. Digest, Bearer and other schemes are not handled; a
  request carrying one of them gets a Basic challenge.
- Origin-server authentication only. `Proxy-Authorization`, `Proxy-Authenticate`
  and `407` are not implemented.
- UTF-8 only. There is no fallback to ISO-8859-1 or another legacy charset, as
  RFC 7617 Appendix B.2 allows.
- No Unicode normalization. The handler validates UTF-8 but does not apply NFC or
  the PRECIS profiles from RFC 7613.
- No credential cache or session. The authorizer runs on every request, so keep
  it fast.
- No brute-force protection, account lockout or audit logging.
- `Authorization` is treated as a singleton field; multiple field lines are
  rejected with `400` rather than merged.
- The header limit applies to a single header value. The HTTP server's own header
  limits still apply to the rest of the request.
