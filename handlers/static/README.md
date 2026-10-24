# Static file handler

Package `static` serves files from the operating system filesystem or a custom
`fs.FS`. It supports index files, directory listings, cache headers, download
dispositions, content-type detection and HTTP byte ranges.

```go
app.MapGet("/static/*", static.New(
    static.WithRoot("./public"),
    static.WithStripPathPrefix("/static"),
).Handle)
```

The handler maps a request path to a file, serves it with useful headers, and
continues the chain when it has nothing to serve.

## Installation

```go
import "github.com/assaidy/moon/handlers/static"
```

## Quick start

Serve `./public` at `/static`:

```go
app.MapGet("/static/*", static.New(
    static.WithRoot("./public"),
    static.WithStripPathPrefix("/static"),
).Handle)
```

A request for `/static/css/app.css` serves `<root>/css/app.css`.

Serve a custom filesystem:

```go
app.MapGet("/assets/*", static.New(
    static.WithFileSystem(embeddedFiles),
    static.WithStripPathPrefix("/assets"),
).Handle)
```

## How the request is resolved

1. Take `ctx.GetPath()`.
2. Remove `WithStripPathPrefix`, when configured.
3. Join the result with `WithRoot`.
4. Open the exact path from the configured filesystem.
5. If it cannot be opened and `WithCaseInsensitiveFallback` is enabled,
   resolve the path segment by segment against the directory entries,
   ignoring letter case.
6. If it is a directory:
   - try each `WithIndexNames` entry in order;
   - serve the first existing index file;
   - otherwise, list the directory when `WithDirectoryBrowsing(true)`;
   - otherwise continue the not-found path.
7. If it is a file, serve it.
8. If it cannot be opened or is not servable, run `WithNotFoundHandler` or
   continue the chain.

### Path case

Moon keeps request paths as they arrive and matches routes and middleware
case-insensitively by default. Static resolves files with their exact case
first. When the exact path does not exist and `WithCaseInsensitiveFallback` is
enabled (the default), the path is retried ignoring letter case.

Exact requests always win, so a request for `/static/A.txt` serves `A.txt`
even when `a.txt` also exists. A fallback is only used when the requested path
matches no file exactly. In that case an exact entry name is preferred at each
segment, then the remaining case-insensitive candidates in lexical order; if
a candidate directory leads to no match further down, the next candidate is
tried. When the option is disabled, request paths, the mount prefix and index
names must match the filesystem exactly.

`WithCaseInsensitiveFallback` also governs `WithStripPathPrefix` and
`WithIndexNames`.

### Path security

The path is resolved with `path.Join`, which cleans `..` segments. Even so, only
serve a directory that contains public files. Do not point `WithRoot` at a
directory containing secrets, configuration, source code or user-uploaded
executables. Symbolic links are followed by the operating system filesystem, so
also check what your served directory links to.

The handler does **not** provide dotfile filtering. Files such as `.env`,
`.git/config` or backup files are served if they are reachable below the root and
the request path resolves to them.

## Methods

| Method | Behavior |
| --- | --- |
| `GET` | Serves the file, directory listing or range response. |
| `HEAD` | Sends the same headers as `GET`, including `Content-Length`, with no body. |
| Any other | Calls `ctx.Next()` without touching the response. |

`HEAD` ignores `Range` and never receives partial content.

## Configuration

| Option | Default | Description |
| --- | --- | --- |
| `WithRoot(string)` | `"."` | Filesystem path request paths are resolved against. |
| `WithIndexNames([]string)` | `{"index.html"}` | Files tried for directory requests, in order. |
| `WithStripPathPrefix(string)` | `""` | Prefix removed from the request path before resolution. |
| `WithCaseInsensitiveFallback(bool)` | `true` | Retries missing paths, prefixes and index names ignoring letter case. |
| `WithFileSystem(fs.FS)` | `nil` (OS filesystem) | Filesystem to open files from. |
| `WithDownload(bool)` | `false` | Forces `Content-Disposition: attachment`. |
| `WithByteRanges(bool)` | `true` | Serves byte ranges for seekable files. |
| `WithMaxDistinctRanges(int)` | `100` | Maximum distinct ranges per request; negative means unlimited. |
| `WithCoalesceOverlappingRanges(bool)` | `true` | Merges overlapping or touching ranges. |
| `WithDirectoryBrowsing(bool)` | `false` | Lists directories without a servable index. |
| `WithMaxAge(int)` | `-1` | `Cache-Control: max-age` seconds; negative omits the header. |
| `WithNotFoundHandler(moon.Handler)` | `nil` (continue chain) | Handles requests that resolve to nothing. |
| `WithSkip(func(*moon.Context) bool)` | `nil` | Skips the handler for matching requests. |

### `WithRoot`

Sets the directory files are resolved against. Surrounding whitespace is trimmed
and the path is cleaned. An empty value panics.

Default: `"."` (the process working directory).

When `WithFileSystem` is used, the root must be a valid `fs.ValidPath` path, so it
must be relative to the filesystem root and must not contain `..` or start with
`/`.

### `WithIndexNames`

Sets the file names tried for a directory request. Surrounding whitespace is
trimmed. Each entry must be a base file name: it cannot be empty, `.`, `..`,
or contain a path separator. Invalid names panic when the option is created.
The first existing file wins. When none exists and directory browsing is
disabled, the request follows the not-found path.

Default: `{"index.html"}`.

```go
static.WithIndexNames([]string{"index.html", "index.htm"})
```

### `WithStripPathPrefix`

Removes a mount prefix from the request path before resolution.

```go
static.WithRoot("./public")
static.WithStripPathPrefix("/static")
```

With the prefix `/static`, a request for `/static/css/app.css` resolves to
`<root>/css/app.css`. The prefix is cleaned with `path.Clean`. Matching follows
`WithCaseInsensitiveFallback`.

Default: `""`.

### `WithCaseInsensitiveFallback`

Retries path resolution ignoring letter case when the exact path does not
exist. The exact path is always tried first, so files whose names differ only
by case do not collide for exact requests. The fallback also applies to
`WithStripPathPrefix` and `WithIndexNames`.

```go
static.WithCaseInsensitiveFallback(false)
```

Default: `true`.

### `WithFileSystem`

Uses a custom `fs.FS` instead of the operating system filesystem:

```go
//go:embed public
var embedded embed.FS

app.MapGet("/static/*", static.New(
    static.WithFileSystem(embedded),
    static.WithRoot("public"),
    static.WithStripPathPrefix("/static"),
).Handle)
```

Default: `nil` (operating system filesystem).

With a custom filesystem, files that do not implement `io.Seeker` cannot be
served as ranges and cannot be content-sniffed; the handler falls back to a whole
response and `application/octet-stream` when no extension-based type is known.

### `WithDownload`

Adds `Content-Disposition: attachment` with the file's base name, forcing a
download instead of inline rendering. Non-ASCII names use RFC 2231 encoding.

Default: `false`.

Directory listings always render inline; download mode does not apply to them.

### `WithByteRanges`

Enables partial responses for `GET` requests carrying a `Range` header.

Default: `true`.

The handler supports:

- a single range, answered with `206 Partial Content` and `Content-Range`;
- multiple ranges, answered with `206 Partial Content` and a
  `multipart/byteranges` body;
- suffix ranges such as `bytes=-500`;
- open-ended ranges such as `bytes=500-`;
- clamping when the requested end exceeds the file size.

Invalid range syntax, or a unit other than `bytes`, is ignored and the whole file
is served with `200 OK`.

A valid `Range` request is answered through
`moon.ErrRequestedRangeNotSatisfiable` (416) when no requested range is usable.
The response carries `Content-Range: bytes */<size>` and no file body. This
happens when every range in the request starts at or past the end of the file,
or is a zero-length suffix such as `bytes=-0`; an empty file makes every range
unusable. When at least one range is usable, the others are ignored and only
the usable ranges are served.

A valid request is also refused with 416 when the number of distinct ranges
exceeds `WithMaxDistinctRanges` after coalescing, even though the remaining
ranges are satisfiable.

Seekable files advertise `Accept-Ranges: bytes`. Non-seekable files are served
whole and do not advertise range support.

### `WithMaxDistinctRanges`

Caps the number of ranges served in one response after merging. A request with
more ranges is refused with 416, bounding the number of multipart parts.

Default: `100`.

A negative value removes the limit. Zero panics because it would make every range
request fail.

### `WithCoalesceOverlappingRanges`

Merges ranges that overlap or touch before serving, so a request produces as few
parts as possible.

Default: `true`.

Disable it to serve every requested range as its own multipart part.

### `WithDirectoryBrowsing`

When a directory has no servable index file, serve an HTML listing of its
entries instead of treating it as not found.

Default: `false`.

The listing:

- sorts directories first, then names case-insensitively with a bytewise
  tie-breaker;
- links to each entry with absolute paths;
- includes a link to the parent directory unless the directory is the root of the
  served area;
- escapes names for HTML and percent-encodes link targets;
- is always rendered inline, regardless of `WithDownload`;
- ignores `Range`;
- applies `WithMaxAge`;
- sends the same headers for `HEAD` with no body.

A directory whose opened file handle does not implement `fs.ReadDirFile` follows
the not-found path. Errors while reading an otherwise listable directory are
returned to the caller and handled by the app's error handler.

### `WithMaxAge`

Sets `Cache-Control: max-age=<seconds>` on file responses and directory listings.

Default: `-1` (header omitted).

`0` and positive values write the header; negative values omit it. The handler
does not add `ETag`, `If-None-Match`, `If-Modified-Since` or `304 Not Modified`
handling. It only sends `Last-Modified` when the file reports a non-zero
modification time.

### `WithNotFoundHandler`

Handles requests that resolve to no servable file or directory:

```go
static.WithNotFoundHandler(func(ctx *moon.Context) error {
    return ctx.WriteJson(http.StatusNotFound, moon.Object{
        "error": "asset not found",
    })
})
```

Default: `nil`, which calls `ctx.Next()`. When static is the last handler, that
resolves to `moon.ErrNotFound` and the app's error handler runs. When static is
used as middleware, the rest of the chain can still serve the request.

The not-found handler is not consulted for errors that occur while writing the
body.

### `WithSkip`

```go
static.WithSkip(func(ctx *moon.Context) bool {
    return ctx.GetHeader("X-Skip-Static") != ""
})
```

A skipped request calls `ctx.Next()` without serving a file.

Default: `nil`.

## Response headers

For files, the handler sets:

| Header | When |
| --- | --- |
| `Content-Type` | Always. From the extension, then content sniffing, then `application/octet-stream`. |
| `Content-Length` | For successful file and range responses, including `HEAD`. It is removed for a 416 response so the error handler can write its own body. |
| `Last-Modified` | When the file reports a known (non-zero, non-epoch) modification time. |
| `Cache-Control` | When `WithMaxAge >= 0`. |
| `Content-Disposition` | When `WithDownload(true)`. |
| `Accept-Ranges` | When byte ranges are enabled and the file is seekable. |
| `Content-Range` | On single-range responses, and on 416 responses as `bytes */<size>`. |

Content sniffing reads up to 512 bytes and rewinds the file. It only runs when the
extension is not enough and the file is seekable.

## Examples

### Public assets

```go
app.MapGet("/static/*", static.New(
    static.WithRoot("./public"),
    static.WithStripPathPrefix("/static"),
    static.WithMaxAge(3600),
).Handle)
```

### Embedded assets

```go
//go:embed assets
var assets embed.FS

app.MapGet("/assets/*", static.New(
    static.WithFileSystem(assets),
    static.WithRoot("assets"),
    static.WithStripPathPrefix("/assets"),
).Handle)
```

### Downloads

```go
app.MapGet("/download/*", static.New(
    static.WithRoot("./files"),
    static.WithStripPathPrefix("/download"),
    static.WithDownload(true),
).Handle)
```

### Directory browsing

```go
app.MapGet("/browse/*", static.New(
    static.WithRoot("./shared"),
    static.WithStripPathPrefix("/browse"),
    static.WithDirectoryBrowsing(true),
).Handle)
```

### Route-specific middleware

```go
app.MapGet("/static/*", static.New(
    static.WithRoot("./public"),
    static.WithStripPathPrefix("/static"),
).Handle,
    moon.WithMiddlewares(authMiddleware),
)
```

The authentication middleware runs before static. If it short-circuits, no file
is served.

## Security notes

- Do not serve directories containing secrets or source code.
- Dotfiles are not filtered. Remove or block `.env`, `.git`, backups and similar
  files from the served tree.
- Symbolic links are followed by the operating system filesystem.
- Directory browsing reveals file names and structure. Disable it in production
  unless it is intentional.
- Range support can be used to probe file sizes and content. Serve public files
  only.
- Static file serving is not authorization. Put authentication middleware in front
  of private files.
- Be careful with user-controlled roots, prefixes and filesystem paths.

## API reference

```go
func New(options ...Option) *Handler
func (h *Handler) Handle(ctx *moon.Context) error

type Handler struct { /* ... */ }
type Option func(*options)
```

Options:

```go
func WithSkip(f func(*moon.Context) bool) Option
func WithRoot(s string) Option
func WithIndexNames(names []string) Option
func WithStripPathPrefix(s string) Option
func WithCaseInsensitiveFallback(b bool) Option
func WithFileSystem(fs fs.FS) Option
func WithDownload(b bool) Option
func WithByteRanges(b bool) Option
func WithMaxDistinctRanges(i int) Option
func WithCoalesceOverlappingRanges(b bool) Option
func WithDirectoryBrowsing(b bool) Option
func WithMaxAge(i int) Option
func WithNotFoundHandler(h moon.Handler) Option
```

## Limitations

- No `ETag`, `If-None-Match`, `If-Modified-Since` or `304 Not Modified` handling.
- No compression or precompressed `.gz`/`.br` variants.
- No upload handling.
- No built-in dotfile filtering.
- No automatic SPA fallback; use `WithNotFoundHandler`.
- Content sniffing and range serving require `io.Seeker`; non-seekable `fs.FS`
  files are served whole.
- Directory listings have a fixed HTML layout and no theming options.
