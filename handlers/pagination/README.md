# Pagination handler

Package `pagination` parses pagination, sorting and cursor query parameters into
a request-scoped `Info` value and stores it in the request context. It does not
query anything itself: the application decides which pagination policy to use.

```go
app.UseAll(pagination.New())
```

Page numbers, offsets, sorting and cursors are all available at the same time, so
the same endpoint can support page/limit, offset/limit or cursor/limit without a
separate parser for each style.

## Installation

```go
import "github.com/assaidy/moon/handlers/pagination"
```

## Quick start

Register the handler and read the parsed values downstream:

```go
app.UseAll(pagination.New())

app.MapGet("/users", func(ctx *moon.Context) error {
    info, ok := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
    if !ok {
        return moon.ErrInternalServerError
    }

    users, total, err := listUsers(ctx, info.GetLimit(), info.GetOffset(), info.GetSort())
    if err != nil {
        return err
    }

    return ctx.WriteJson(http.StatusOK, moon.Object{
        "data":   users,
        "page":   info.GetPage(),
        "limit":  info.GetLimit(),
        "offset": info.GetOffset(),
        "total":  total,
    })
})
```

With no query string, `GetPage` returns `1`, `GetLimit` returns `10` and
`GetOffset` returns `0`.

## How it works

For each request that is not skipped:

1. Seed `Info` with the configured defaults.
2. Decode the cursor query parameter, if present.
3. Parse the page, limit and offset query parameters.
4. Parse the sort query parameter, when sorting is enabled.
5. Store the `*Info` under the configured context local key.
6. Continue the chain with `ctx.Next()`.

The handler fills every field on every request. Page, offset and cursor do not
exclude each other; the application chooses which values to use. Parsing itself
does not require a database, and no total count or next page is computed.

A malformed value is rejected with `400 Bad Request` (`moon.ErrBadRequest`) and
the error detail names the offending parameter. The rest of the chain does not
run for a rejected request.

## Query parameters

The default keys are:

| Query key | Default | Accepted values |
| --- | --- | --- |
| `page` | `1` | A positive integer. |
| `limit` | `10` | An integer between `minLimit` and `maxLimit` (default `1` to `100`). |
| `offset` | `0` | A non-negative integer. |
| `sort` | none | Comma-separated sort keys, each with an optional `+` or `-` prefix. Disabled unless configured. |
| `cursor` | none | A URL-safe base64-encoded JSON value. |

Every key can be renamed with an option. Any key may also be set to an empty
string to disable that query parameter; the configured default value is then used.
The sort key is empty by default, which disables sorting.

### Page, limit and offset

`page` and `offset` are parsed independently; the handler does not derive one from
the other. If an endpoint uses page numbers, compute the offset yourself:

```go
offset := (info.GetPage() - 1) * info.GetLimit()
```

If it uses offsets, ignore `GetPage`. Mixing both policies on the same endpoint is
possible but usually confusing to clients.

`limit` always has a positive maximum. The default range is `1` to `100`; raise
`maxLimit` deliberately for endpoints that need larger pages. Values outside the
range are rejected by default, so clients learn that their request was not
honored. Enable `WithClampLimit` if you would rather clamp them to the nearest
allowed value. The minimum cannot be zero, because allowing it leads to
unnecessary database work and invites abuse.

### Sorting

Sorting is disabled by default because arbitrary sort keys cannot be passed
safely to a database. Enable it with a query key and an allowlist:

```go
app.UseAll(pagination.New(
    pagination.WithSortQueryKey("sort"),
    pagination.WithAllowedSortKeys([]string{"created_at", "name", "id"}),
))
```

The syntax is:

| Query | Result |
| --- | --- |
| `?sort=name` | `name` ascending. |
| `?sort=+name` | `name` ascending. |
| `?sort=-name` | `name` descending. |
| `?sort=-created_at,name` | `created_at` descending, then `name` ascending. |
| `?sort=name&sort=-id` | `name` ascending, then `id` descending. |

A literal `+` in a query value is decoded as a space, so clients that want an
explicit ascending sign must send `%2B` (`?sort=%2Bname`). Omitting the sign is
the usual way to request ascending order.

Keys are trimmed and compared case sensitively against the allowlist. A key that
is not listed, an empty key, a bare sign, or an empty comma segment is rejected
with `400` and a detail such as `sort key "password" is not allowed`.

When the sort query parameter is absent, the configured default sort is used (see
[`WithDefaultSort`](#withdefaultsort)). When it is present, it replaces the
default sort completely, even if the values are invalid.

Read the parsed criteria with `GetSort` and map keys to trusted column names:

```go
var sortColumns = map[string]string{
    "created_at": "users.created_at",
    "name":       "users.name",
    "id":         "users.id",
}

app.MapGet("/users", func(ctx *moon.Context) error {
    info, ok := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
    if !ok {
        return moon.ErrInternalServerError
    }

    orderBy := make([]string, 0, len(info.GetSort()))
    for _, s := range info.GetSort() {
        column, ok := sortColumns[s.Key]
        if !ok {
            return moon.ErrInternalServerError
        }

        direction := "ASC"
        if s.Order == pagination.OrderDescending {
            direction = "DESC"
        }
        orderBy = append(orderBy, column+" "+direction)
    }

    users, err := listUsers(ctx, info.GetLimit(), info.GetOffset(), orderBy)
    if err != nil {
        return err
    }
    return ctx.WriteJson(http.StatusOK, users)
})
```

The handler validates keys against `WithAllowedSortKeys`, but the example still
maps them to known column names instead of using `s.Key` directly in SQL. Keep
the allowlist and `sortColumns` in sync; a key missing from the map indicates a
configuration mistake.

### Cursor

The cursor query parameter is decoded from URL-safe base64 and validated as
JSON:

```text
?cursor=eyJpZCI6NDJ9
```

A missing cursor means there is no cursor, and `GetCursor` returns `""`. An
invalid cursor is rejected with `400` and the detail
`cursor must be a valid base64-encoded JSON value`.

The cursor format is defined by `EncodeCursor` and `DecodeCursor`, so use those
functions instead of hand-encoding or hand-decoding cursors. The handler uses the
same decoder to validate the cursor query, but only stores the raw query value.
It does not inspect the contents. Cursors are client-controlled and must be
treated as input, not as trusted state.

## Reading values

Read `*Info` from the context local:

```go
info, ok := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
```

When the handler is skipped, nothing is stored and `ok` is false.

| Method | Returns |
| --- | --- |
| `GetPage() int` | Parsed page, or the default. |
| `GetLimit() int` | Parsed limit, or the default. |
| `GetOffset() int` | Parsed offset, or the default. |
| `GetSort() []Sort` | Parsed sort criteria, or the default sort. |
| `GetCursor() string` | Raw cursor query value, or `""`. |

Cursor helpers:

| Function | Description |
| --- | --- |
| `EncodeCursor(any) ([]byte, error)` | Encodes a JSON-serializable value as an opaque cursor. |
| `DecodeCursor([]byte, any) error` | Decodes a cursor into a destination pointer. |

### Returning a next cursor

After fetching one row more than the limit, build the next cursor from the last
returned row:

```go
type userCursor struct {
    ID int `json:"id"`
}

app.MapGet("/users", func(ctx *moon.Context) error {
    info, ok := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
    if !ok {
        return moon.ErrInternalServerError
    }

    var cursor userCursor
    if raw := info.GetCursor(); raw != "" {
        if err := pagination.DecodeCursor([]byte(raw), &cursor); err != nil {
            return err
        }
    }

    limit := info.GetLimit()
    users, err := listUsersAfter(ctx, cursor.ID, limit+1)
    if err != nil {
        return err
    }

    var nextCursor string
    if len(users) > limit {
        users = users[:limit]
        encoded, err := pagination.EncodeCursor(userCursor{ID: users[len(users)-1].ID})
        if err != nil {
            return err
        }
        nextCursor = string(encoded)
    }

    return ctx.WriteJson(http.StatusOK, moon.Object{
        "data":        users,
        "next_cursor": nextCursor,
    })
})
```

`EncodeCursor` accepts any value that can be encoded as JSON and returns the
cursor as URL-safe base64 bytes. Convert the result to `string` when writing it
to a JSON response or query parameter.

`DecodeCursor` takes those cursor bytes and a destination pointer. It returns an
error when the input is not valid URL-safe base64, does not contain valid JSON,
or does not match the destination type. Pass `[]byte(info.GetCursor())` to decode
the request cursor.

A non-empty next cursor signals that another page exists. Use
`nextCursor != ""` if your response needs a boolean field.

When decoding into `map[string]any` or `any`, JSON numbers decode as `float64`.
Decode into a typed struct or pointer instead when you want concrete numeric
types, and validate that required fields are present before using the cursor.

## Configuration

All options are passed to `pagination.New`.

| Option                               | Default           | Description                                                                 |
| ------------------------------------ | ----------------- | --------------------------------------------------------------------------- |
| `WithSkip(func(*moon.Context) bool)` | `nil`             | Requests for which the predicate returns true bypass parsing.               |
| `WithPageQueryKey(string)`           | `"page"`          | Query key carrying the page. Trimmed; empty disables page parsing.          |
| `WithDefaultPage(int)`               | `1`               | Page used when the query key is absent or disabled. Must be positive.       |
| `WithLimitQueryKey(string)`          | `"limit"`         | Query key carrying the limit. Trimmed; empty disables limit parsing.        |
| `WithDefaultLimit(int)`              | `10`              | Limit used when the query key is absent or disabled. Must be positive.      |
| `WithMinLimit(int)`                  | `1`               | Smallest accepted limit. Must be positive.                                  |
| `WithMaxLimit(int)`                  | `100`             | Largest accepted limit. Must be positive.                                   |
| `WithClampLimit(bool)`               | `false`           | Clamps out-of-range limits instead of rejecting them.                       |
| `WithOffsetQueryKey(string)`         | `"offset"`        | Query key carrying the offset. Trimmed; empty disables offset parsing.      |
| `WithDefaultOffset(int)`             | `0`               | Offset used when the query key is absent or disabled. Must be non-negative. |
| `WithSortQueryKey(string)`           | `""`              | Enables sorting and sets its query key. Empty disables sorting.             |
| `WithDefaultSort([]Sort)`            | `nil`             | Sort used when the sort query is absent. Keys must be allowed.              |
| `WithAllowedSortKeys([]string)`      | `nil`             | Keys accepted in the sort query. Empty allows none.                         |
| `WithCursorQueryKey(string)`         | `"cursor"`        | Query key carrying the cursor. Trimmed; empty disables cursor parsing.      |
| `WithLocalKey(string)`               | `DefaultLocalKey` | Context local key holding `*Info`. Used as-is; empty panics.                |

`New` panics when the default limit is outside `[minLimit, maxLimit]` and when a
default sort key is not listed in `WithAllowedSortKeys`.

### `WithSkip`

```go
pagination.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetMethod() != http.MethodGet
})
```

A skipped request runs the chain with no parsing and no `Info` in the context.
The predicate runs once per request, before any parsing.

Default: `nil`.

### Query key options

```go
app.UseAll(pagination.New(
    pagination.WithPageQueryKey("p"),
    pagination.WithLimitQueryKey("size"),
    pagination.WithOffsetQueryKey("start"),
    pagination.WithSortQueryKey("order"),
    pagination.WithCursorQueryKey("token"),
))
```

`WithPageQueryKey`, `WithLimitQueryKey`, `WithOffsetQueryKey`,
`WithSortQueryKey` and `WithCursorQueryKey` trim surrounding whitespace. Any of
them may be empty, which disables that query parameter. For page, limit and
offset the configured default is still used; for sort and cursor there is no
default, so the fields stay at their zero values.

### Disabling a query key

```go
app.UseAll(pagination.New(
    pagination.WithPageQueryKey(""),
    pagination.WithLimitQueryKey(""),
))
```

Here `?page=5&limit=500` is ignored: `GetPage` returns the default page and
`GetLimit` returns the default limit. The other query keys keep working unless
they are disabled too.

### `WithDefaultPage`, `WithDefaultLimit`, `WithDefaultOffset`

```go
app.UseAll(pagination.New(
    pagination.WithDefaultPage(1),
    pagination.WithDefaultLimit(25),
    pagination.WithDefaultOffset(0),
))
```

These set the values used when the corresponding query parameter is absent. The
page and limit must be positive; the offset must be non-negative. A default limit
must also be between `WithMinLimit` and `WithMaxLimit`.

Defaults do not change validation: a query parameter that is present must still
be valid. `WithClampLimit` only changes how integer limits outside the accepted
range are handled.

### `WithMinLimit` and `WithMaxLimit`

```go
app.UseAll(pagination.New(
    pagination.WithMinLimit(1),
    pagination.WithMaxLimit(200),
    pagination.WithDefaultLimit(50),
))
```

These set the accepted limit range, inclusive. Both must be positive and must
contain the default limit. By default, a request outside the range gets `400`
with the range in the detail.

Use `WithClampLimit(true)` to clamp out-of-range limits to the nearest allowed
value instead:

```go
app.UseAll(pagination.New(
    pagination.WithClampLimit(true),
    pagination.WithMinLimit(1),
    pagination.WithMaxLimit(200),
    pagination.WithDefaultLimit(50),
))
```

With clamping enabled, `limit=0` becomes `1` and `limit=999` becomes `200`. Values
that are not integers are still rejected with `400`; clamping applies only to
integer limits outside the range.

Choose a maximum that fits your endpoint; do not use a large arbitrary maximum,
as that turns a pagination endpoint into a way to request an arbitrarily large
result set. The minimum cannot be zero, because a zero limit leads to unnecessary
database calls and can be abused.

### `WithDefaultSort`

```go
app.UseAll(pagination.New(
    pagination.WithSortQueryKey("sort"),
    pagination.WithAllowedSortKeys([]string{"created_at", "name", "id"}),
    pagination.WithDefaultSort([]pagination.Sort{
        {Key: "created_at", Order: pagination.OrderDescending},
        {Key: "id"},
    }),
))
```

The default sort is used when the sort query parameter is absent. Keys and orders
are trimmed and lowercased; an empty order becomes `asc`. Valid orders are `pagination.OrderAscending`
(`"asc"`) and `pagination.OrderDescending` (`"desc"`); anything else panics.

When the same key appears more than once in the default sort, the last order wins
and the key keeps its first position.

Every default key must also appear in `WithAllowedSortKeys`, otherwise `New`
panics. This keeps the default consistent with the keys the endpoint exposes.

Default: `nil`.

### `WithAllowedSortKeys`

```go
pagination.WithAllowedSortKeys([]string{"created_at", "name", "id"})
```

Keys are trimmed and compared case sensitively. An empty key panics. An empty
list allows no sort query, so any sort value is rejected.

Use the allow list as a mapping boundary: do not interpolate client-supplied keys
into SQL directly. Map allowed keys to known column names or query fragments.

Default: `nil` (no key is allowed).

### `WithCursorQueryKey`

```go
pagination.WithCursorQueryKey("token")
```

Sets the query key carrying the cursor. Surrounding whitespace is trimmed; an
empty key disables cursor parsing, so `GetCursor` always returns `""`.

Default: `"cursor"`.

### `WithLocalKey`

```go
h := pagination.New(
    pagination.WithLocalKey("app.pagination"),
)
app.UseAll(h)

info, _ := ctx.GetLocal[*pagination.Info]("app.pagination")
```

Sets the context local key. The key is used as-is, including surrounding
whitespace; an empty key panics.

Default: `pagination.DefaultLocalKey`.

## Examples

### Page and limit

```go
app.UseAll(pagination.New())

app.MapGet("/posts", func(ctx *moon.Context) error {
    info, _ := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
    offset := (info.GetPage() - 1) * info.GetLimit()
    posts, err := listPosts(ctx, offset, info.GetLimit())
    if err != nil {
        return err
    }
    return ctx.WriteJson(http.StatusOK, posts)
})
```

### Offset and limit

```go
app.UseAll(pagination.New())

app.MapGet("/posts", func(ctx *moon.Context) error {
    info, _ := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
    posts, err := listPosts(ctx, info.GetOffset(), info.GetLimit())
    if err != nil {
        return err
    }
    return ctx.WriteJson(http.StatusOK, posts)
})
```


### Sortable list

```go
app.UseAll(pagination.New(
    pagination.WithDefaultLimit(20),
    pagination.WithMaxLimit(100),
    pagination.WithSortQueryKey("sort"),
    pagination.WithAllowedSortKeys([]string{"created_at", "title", "id"}),
    pagination.WithDefaultSort([]pagination.Sort{
        {Key: "created_at", Order: pagination.OrderDescending},
    }),
))
```

Requests such as `?sort=-created_at,title&limit=20` produce two criteria.

### Cursor pagination

```go
type eventCursor struct {
    ID int `json:"id"`
}

app.UseAll(pagination.New())

app.MapGet("/events", func(ctx *moon.Context) error {
    info, _ := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)

    var cursor eventCursor
    if raw := info.GetCursor(); raw != "" {
        if err := pagination.DecodeCursor([]byte(raw), &cursor); err != nil {
            return err
        }
    }

    limit := info.GetLimit()
    events, err := listEventsAfter(ctx, cursor.ID, limit+1)
    if err != nil {
        return err
    }

    var nextCursor string
    if len(events) > limit {
        events = events[:limit]
        encoded, err := pagination.EncodeCursor(eventCursor{
            ID: events[len(events)-1].ID,
        })
        if err != nil {
            return err
        }
        nextCursor = string(encoded)
    }

    return ctx.WriteJson(http.StatusOK, moon.Object{
        "data":        events,
        "next_cursor": nextCursor,
    })
})
```

### Custom query keys

```go
app.UseAll(pagination.New(
    pagination.WithPageQueryKey("p"),
    pagination.WithLimitQueryKey("size"),
    pagination.WithSortQueryKey("order"),
    pagination.WithCursorQueryKey("after"),
    pagination.WithAllowedSortKeys([]string{"id", "name"}),
))
```

### Route-specific pagination

```go
app.MapGet("/search", search,
    moon.WithMiddlewares(pagination.New()),
)
```

Only `/search` parses pagination parameters. Other routes are unaffected.

### Skipping non-list endpoints

```go
app.UseAll(pagination.New(
    pagination.WithSkip(func(ctx *moon.Context) bool {
        return ctx.GetMethod() != http.MethodGet
    }),
))
```

## Security notes

- **Cap the limit.** Keep `WithMaxLimit` small enough that a single request
  cannot load an arbitrarily large result set. By default, out-of-range limits
  are rejected; `WithClampLimit` clamps them to the allowed range instead.
- **Allowlist sort keys.** Map allowed keys to known column names; never
  concatenate a client-supplied key into SQL. Keys are case sensitive and only
  the configured list is accepted.
- **Treat cursors as untrusted input.** A cursor is base64-encoded JSON, not a
  signed token. Clients can decode, change and re-encode it.
- **Do not put secrets in cursors.** The contents are readable by the client.
  If cursor integrity or confidentiality matters, sign or encrypt the payload in
  your application rather than relying on the base64 encoding.
- **Validate cursor contents.** Check expected keys and types before using cursor
  values. Missing keys, `nil` values and wrong types are not rejected by the
  handler.
- **Pagination is not authorization.** A valid page, offset, sort or cursor does
  not grant access. Keep ownership and permission filters in every query.
- **Prefer keyset pagination for large offsets.** Large `offset` values can be
  expensive for the database and reveal how much data exists.
- **Rate-limit expensive queries.** Parsing is cheap, but large sorts and deep
  offsets may not be. Apply normal request limits to list endpoints.

## API reference

```go
const (
    OrderAscending  = "asc"
    OrderDescending = "desc"

    DefaultLocalKey = "moon.handlers.pagination.local_key"
)

func New(options ...Option) moon.Handler

func EncodeCursor(v any) ([]byte, error)
func DecodeCursor(raw []byte, v any) error

type Option func(*options)

type Sort struct {
    Key   string
    Order string
}

type Info struct { /* ... */ }

func (i *Info) GetPage() int
func (i *Info) GetLimit() int
func (i *Info) GetOffset() int
func (i *Info) GetSort() []Sort
func (i *Info) GetCursor() string
```

Options:

```go
func WithSkip(f func(ctx *moon.Context) bool) Option
func WithPageQueryKey(s string) Option
func WithDefaultPage(i int) Option
func WithLimitQueryKey(s string) Option
func WithDefaultLimit(i int) Option
func WithMinLimit(i int) Option
func WithMaxLimit(i int) Option
func WithClampLimit(b bool) Option
func WithOffsetQueryKey(s string) Option
func WithDefaultOffset(i int) Option
func WithSortQueryKey(s string) Option
func WithDefaultSort(s []Sort) Option
func WithAllowedSortKeys(keys []string) Option
func WithCursorQueryKey(s string) Option
func WithLocalKey(s string) Option
```

## Limitations

- The handler only parses query parameters. It does not run queries, count
  results, generate next/previous links or produce an envelope.
- Page and offset are independent; no conversion is performed.
- Page, offset and cursor may be supplied together. Enforcing a single policy is
  left to the application.
- Disabling a query key makes the handler ignore that parameter entirely; the
  default value is used instead.
- Sorting is disabled by default and requires both `WithSortQueryKey` and
  `WithAllowedSortKeys`.
- Sort keys are compared case sensitively and are not normalized beyond trimming.
- Duplicate keys in a request sort are kept as separate criteria. Only
  `WithDefaultSort` collapses duplicates.
- The cursor is padded URL-safe base64 over JSON. It is not versioned, signed,
  encrypted or checked for content.
- Decoding a cursor into `map[string]any` or `any` yields `float64` for numbers.
  Decode into a typed struct or pointer for concrete numeric types.
- Integer parsing uses the platform `int`, so values beyond its range are
  rejected as malformed.
- The handler does not generate `Link` headers, total counts or ETags.
