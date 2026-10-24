// Package static provides a handler that serves files from the operating
// system filesystem or a custom [fs.FS]. Register [Handler.Handle] in the
// chain:
//
//	app.MapGet("/static/*", static.New(
//		static.WithStripPathPrefix("/static"),
//		static.WithRoot("./public"),
//	).Handle)
//
// The request path is joined with the configured root and opened, and a
// directory is replaced by the first of [WithIndexNames] that exists in it,
// or, when [WithDirectoryBrowsing] is enabled, by a listing of its entries.
// When nothing matches, the request is answered by [WithNotFoundHandler]
// or, by default, continue the chain via [moon.Context.Next]. Files are
// served with their content type, content length and, when known, their
// modification time; GET requests carrying a Range header are answered with
// the requested bytes when the file can be seeked, following the rules of
// [WithByteRanges]. Requests with a method other than GET and HEAD continue
// the chain.
//
// Paths are resolved with their exact letter case first. When
// [WithCaseInsensitiveFallback] is enabled (the default), a path that does
// not exist exactly is resolved again ignoring letter case.
package static

import (
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/assaidy/moon"
)

// Handler serves files from a filesystem. Use [New] to construct it,
// passing [Option]s to configure it, then register [Handler.Handle] in the
// chain.
type Handler struct {
	options options
}

// New returns a handler built from sensible defaults, customized by the
// given options, then register [Handler.Handle] in the chain:
//
//	app.MapGet("/static/*", static.New(
//		static.WithStripPathPrefix("/static"),
//		static.WithRoot("./public"),
//	).Handle)
//
// It panics when a custom filesystem is given via [WithFileSystem] and the
// root is not a valid [fs.ValidPath] path.
func New(opts ...Option) *Handler {
	options := options{
		root:                      ".",
		indexNames:                []string{"index.html"},
		byteRanges:                true,
		coalesceOverlappingRanges: true,
		maxDistinctRanges:         100,
		maxAge:                    -1,
		caseInsensitiveFallback:   true,
	}
	for _, opt := range opts {
		opt(&options)
	}
	moon.Assert(
		options.fs == nil || fs.ValidPath(options.root),
		"root must be a valid io/fs path when a custom filesystem is provided",
	)

	return &Handler{options: options}
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	skip                      func(*moon.Context) bool
	root                      string
	indexNames                []string
	stripPathPrefix           string
	fs                        fs.FS
	download                  bool
	byteRanges                bool
	maxDistinctRanges         int
	coalesceOverlappingRanges bool
	directoryBrowsing         bool
	maxAge                    int
	caseInsensitiveFallback   bool
	notFoundHandler           moon.Handler
}

// Option configures a [Handler]. Pass them to [New].
type Option func(*options)

// WithSkip sets a predicate deciding which requests bypass the handler:
// when f returns true for the request, the chain continues via
// [moon.Context.Next] and no file is served. Pass it to [New].
//
// Default: nil (nothing is skipped)
func WithSkip(f func(*moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// WithRoot sets the filesystem path request paths are resolved against.
// Surrounding whitespace is trimmed and the path is cleaned; it panics on an
// empty value. When [WithFileSystem] is used, the root must be a valid
// [fs.ValidPath] path. Pass it to [New].
//
// Default: "." (the working directory)
func WithRoot(s string) Option {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "root cannot be empty")

	return func(o *options) {
		o.root = path.Clean(s)
	}
}

// WithIndexNames sets the file names served for directory requests, tried
// in order. Surrounding whitespace is trimmed. Each name must be a base file
// name: it cannot be empty, ".", "..", or contain a path separator. The first
// one that exists in the directory is served; when none does, the directory
// is listed if [WithDirectoryBrowsing] is enabled and treated as not found
// otherwise. When [WithCaseInsensitiveFallback] is enabled, a name that does
// not exist exactly is matched against the directory entries ignoring case.
// Pass it to [New].
//
// Default: []string{"index.html"}
func WithIndexNames(names []string) Option {
	trimmed := make([]string, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		moon.Assert(isBaseName(name), fmt.Sprintf("index name %q must be a file name without path separators", name))
		trimmed[i] = name
	}
	return func(o *options) {
		o.indexNames = trimmed
	}
}

// isBaseName reports whether name can denote a single file entry: non-empty,
// not a dot entry, and free of path separators.
func isBaseName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// WithStripPathPrefix sets a path prefix removed from the request path
// before resolving it, for mounting the handler under a route prefix. For
// example, with "/static" a request for "/static/css/app.css" serves
// "css/app.css" below the root. Matching follows
// [WithCaseInsensitiveFallback]. Pass it to [New].
//
// Default: "" (nothing is stripped)
func WithStripPathPrefix(s string) Option {
	return func(o *options) {
		o.stripPathPrefix = path.Clean(s)
	}
}

// WithFileSystem sets the [fs.FS] files are opened from instead of the
// operating system filesystem. Pass it to [New].
//
// Default: nil (the operating system filesystem)
func WithFileSystem(fs fs.FS) Option {
	return func(o *options) {
		o.fs = fs
	}
}

// WithDownload forces files to be downloaded instead of rendered: every
// response carries a Content-Disposition attachment header with the file's
// base name as filename. Pass it to [New].
//
// Default: false
func WithDownload(b bool) Option {
	return func(o *options) {
		o.download = b
	}
}

// WithByteRanges sets whether GET requests carrying a Range header are
// answered with the requested bytes of the file. Ranges are sorted by
// offset and, unless disabled by [WithCoalesceOverlappingRanges], merged
// when they overlap or touch. What remains is served as:
//
//   - one 206 Partial Content response with a Content-Range header and a
//     Content-Length matching the range, when only one range remains;
//   - a 206 Partial Content response with a multipart/byteranges body
//     holding one part per range otherwise: parts carry their own
//     Content-Range and the file's content type, and no top-level
//     Content-Range is set;
//   - a 416 Requested Range Not Satisfiable response when nothing is left
//     to serve (every range is zero-length or lies outside the file, as
//     for any range asked of an empty file) or when more ranges remain
//     than [WithMaxDistinctRanges] allows. The response carries a
//     Content-Range header reporting the file size, e.g. "bytes */100".
//
// A Range header that cannot be parsed, or that uses a unit other than
// bytes, is ignored and the whole file is served. HEAD requests ignore
// Range and never receive partial content. When enabled, seekable files
// announce Accept-Ranges: bytes on their responses, while files that
// cannot seek are always served whole and never announce it. Pass it to
// [New].
//
// Default: true
func WithByteRanges(b bool) Option {
	return func(o *options) {
		o.byteRanges = b
	}
}

// WithMaxDistinctRanges sets the maximum number of distinct ranges a request
// may ask for. A request for more, after merging, is refused with 416
// Requested Range Not Satisfiable instead of being served, which bounds the
// number of parts in a multipart response. A negative value removes the
// limit; zero panics. Pass it to [New].
//
// Default: 100
func WithMaxDistinctRanges(i int) Option {
	// Negatives mean no limit.
	// Zero is not allowed as it's misleading.
	moon.Assert(i != 0, "max distinct ranges cannot be zero; use a negative value for no limit")

	return func(o *options) {
		o.maxDistinctRanges = i
	}
}

// WithCoalesceOverlappingRanges sets whether ranges that overlap or touch
// are merged before they are served, so a request is served in as few parts
// as possible; when disabled, every range is served as its own part. Pass
// it to [New].
//
// Default: true
func WithCoalesceOverlappingRanges(b bool) Option {
	return func(o *options) {
		o.coalesceOverlappingRanges = b
	}
}

// WithDirectoryBrowsing sets whether a directory without a servable index
// file is answered with an HTML listing of its entries instead of being
// treated as not found. The listing links to each entry, directories first
// and names compared case-insensitively, with a link to the parent
// directory unless the directory is the root of the served area. The links
// are absolute, so no redirect is needed for a directory path.
//
// The listing is always rendered inline: [WithDownload] does not apply to
// it and Range headers are ignored. [WithMaxAge] applies as it does to
// files, and HEAD requests receive the same headers with no body. A
// directory that cannot be listed is treated as not found. Pass it to
// [New].
//
// Default: false
func WithDirectoryBrowsing(b bool) Option {
	return func(o *options) {
		o.directoryBrowsing = b
	}
}

// WithMaxAge sets the max-age directive of the Cache-Control response
// header, the number of seconds a client may cache the response, whether it
// is a file or a directory listing. Zero and positive values are written, a
// negative value omits the header. Pass it to [New].
//
// Default: -1 (the header is omitted)
func WithMaxAge(i int) Option {
	return func(o *options) {
		o.maxAge = i
	}
}

// WithCaseInsensitiveFallback controls what happens when the exact request
// path does not resolve to a file. When enabled, the path is resolved again by
// matching each segment against directory entries ignoring letter case,
// preferring an exact name at each step and then entries in lexical order.
// [WithIndexNames] and [WithStripPathPrefix] are retried the same way.
//
// Exact requests always win, so files whose names differ only by case do not
// collide unless the requested path matches neither exactly; in that case the
// first matching entry in lexical order is used. When disabled, paths and
// prefixes must match the filesystem exactly. Pass it to [New].
//
// Default: true
func WithCaseInsensitiveFallback(b bool) Option {
	return func(o *options) {
		o.caseInsensitiveFallback = b
	}
}

// WithNotFoundHandler sets the handler answering requests that resolve to
// nothing to serve, instead of continuing the chain via
// [moon.Context.Next]. It is not consulted for failures while writing the
// body. Pass it to [New].
//
// Default: nil ([moon.Context.Next], which resolves to the route handler,
// [moon.ErrMethodNotAllowed] or [moon.ErrNotFound] as appropriate)
func WithNotFoundHandler(h moon.Handler) Option {
	return func(o *options) {
		o.notFoundHandler = h
	}
}

// Handle serves the request. Requests skipped by [WithSkip] and requests
// with a method other than GET and HEAD continue the chain via
// [moon.Context.Next]. A request that resolves to nothing to serve is handed
// to [WithNotFoundHandler] or continues the chain via [moon.Context.Next].
// Errors are returned to the caller: the error of writing the body, or
// [moon.ErrRequestedRangeNotSatisfiable] when a Range request asks for
// ranges that cannot be satisfied.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	if m := ctx.GetMethod(); m != moon.MethodHead && m != moon.MethodGet {
		return ctx.Next()
	}

	r, err := me.resolve(ctx)
	if err != nil {
		return me.notFound(ctx)
	}
	defer r.file.Close()

	return me.serve(ctx, r)
}

// resolved is the file a request is served from, or the directory it lists
// when directory browsing is enabled, along with the metadata needed to
// describe it in the response headers.
type resolved struct {
	// file is the opened file, or the opened directory when info reports
	// it as a directory, closed by the caller.
	file fs.File
	// info describes the opened file; its size and modification time fill
	// the Content-Length and Last-Modified headers.
	info fs.FileInfo
	// name is the resolved path used to build header values, such as the
	// content type and the download filename, and to tell the root of the
	// served area apart from the directories below it.
	name string
}

// resolve maps the request path to a file: any configured prefix is
// stripped, the path is joined with the root and opened, and a directory is
// replaced by its first existing index file, or kept open to be listed when
// directory browsing is enabled and no index exists. When the exact path
// cannot be opened and [WithCaseInsensitiveFallback] is enabled, the path is
// resolved again by matching each segment case-insensitively. The returned
// error is never exposed to the client, only used to fall back to the
// not-found path.
func (me *Handler) resolve(ctx *moon.Context) (resolved, error) {
	p := ctx.GetPath()
	if me.options.stripPathPrefix != "" {
		p = me.stripPrefix(p)
	}

	name := path.Join(me.options.root, p)
	file, info, err := me.openAndStat(name)
	if err != nil && me.options.caseInsensitiveFallback {
		resolvedName, resolveErr := me.resolveCaseInsensitive(p)
		if resolveErr == nil {
			name = resolvedName
			file, info, err = me.openAndStat(name)
		}
	}
	if err != nil {
		return resolved{}, err
	}

	if info.IsDir() {
		r, err := me.resolveIndex(name)
		if err == nil {
			file.Close()
			return r, nil
		}
		if !me.options.directoryBrowsing {
			file.Close()
			return resolved{}, err
		}
		// The directory is served as a listing; keep it open so that
		// serveDirectory can read its entries.
		return resolved{file: file, info: info, name: name}, nil
	}

	return resolved{file: file, info: info, name: name}, nil
}

// openAndStat opens name and stats it, closing the file again when stat
// fails, so callers own the returned file on success.
func (me *Handler) openAndStat(name string) (fs.File, fs.FileInfo, error) {
	file, err := me.open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

// resolveCaseInsensitive finds the actual path for p below the root by
// matching each segment against directory entries ignoring letter case. An
// exact entry name is preferred at each segment; the remaining matches are
// tried in lexical order, backtracking when a candidate leads to no match
// further down. It returns the path with the entries' actual letter case.
func (me *Handler) resolveCaseInsensitive(p string) (string, error) {
	return me.walkCaseInsensitive(me.options.root, pathSegments(p))
}

// walkCaseInsensitive resolves segs below dir, as described by
// [Handler.resolveCaseInsensitive]. Intermediate segments must be
// directories; the final match may be a file or a directory.
func (me *Handler) walkCaseInsensitive(dir string, segs []string) (string, error) {
	if len(segs) == 0 {
		return dir, nil
	}

	entries, err := me.readDir(dir)
	if err != nil {
		return "", err
	}

	for _, name := range matchEntryNames(entries, segs[0]) {
		next := path.Join(dir, name)
		if len(segs) == 1 {
			file, _, err := me.openAndStat(next)
			if err != nil {
				continue
			}
			file.Close()
			return next, nil
		}
		if resolved, err := me.walkCaseInsensitive(next, segs[1:]); err == nil {
			return resolved, nil
		}
	}

	return "", fs.ErrNotExist
}

// pathSegments splits a request path into the segments resolved below the
// root. The leading slash and empty segments are ignored.
func pathSegments(p string) []string {
	p = strings.TrimPrefix(path.Clean(p), "/")
	if p == "" || p == "." {
		return nil
	}
	return strings.Split(p, "/")
}

// matchEntryNames returns the entry names matching name, preferring the exact
// name when present. Other case-insensitive matches keep the directory's
// lexical order, which makes the fallback deterministic.
func matchEntryNames(entries []fs.DirEntry, name string) []string {
	var exact string
	var matches []string
	for _, entry := range entries {
		entryName := entry.Name()
		if entryName == name {
			exact = entryName
			continue
		}
		if strings.EqualFold(entryName, name) {
			matches = append(matches, entryName)
		}
	}
	if exact == "" {
		return matches
	}
	return append([]string{exact}, matches...)
}

// readDir lists dir from the configured filesystem. Both fs.ReadDir and
// os.ReadDir return entries sorted by name.
func (me *Handler) readDir(dir string) ([]fs.DirEntry, error) {
	if me.options.fs != nil {
		return fs.ReadDir(me.options.fs, dir)
	}
	return os.ReadDir(dir)
}

// resolveIndex opens the first of [WithIndexNames] that exists as a file in
// the directory dir, or reports an error when none does. The exact name is
// tried first; when [WithCaseInsensitiveFallback] is enabled, a name that does
// not exist exactly is matched against the directory entries ignoring case.
func (me *Handler) resolveIndex(dir string) (resolved, error) {
	for _, index := range me.options.indexNames {
		if r, ok := me.openIndex(path.Join(dir, index)); ok {
			return r, nil
		}
		if !me.options.caseInsensitiveFallback {
			continue
		}
		entries, err := me.readDir(dir)
		if err != nil {
			continue
		}
		for _, name := range matchEntryNames(entries, index) {
			if r, ok := me.openIndex(path.Join(dir, name)); ok {
				return r, nil
			}
		}
	}
	return resolved{}, fs.ErrNotExist
}

// openIndex opens name when it is a servable file, closing it and reporting
// false when it cannot be opened or is a directory.
func (me *Handler) openIndex(name string) (resolved, bool) {
	file, info, err := me.openAndStat(name)
	if err != nil {
		return resolved{}, false
	}
	if info.IsDir() {
		file.Close()
		return resolved{}, false
	}
	return resolved{file: file, info: info, name: name}, true
}

// serve writes r as the response: the headers describing it and, for
// methods other than HEAD, its body, either whole or the ranges asked for
// by a Range header. A directory is served as an HTML listing instead. It
// returns the error of writing the body, or
// [moon.ErrRequestedRangeNotSatisfiable] when the requested ranges cannot
// be satisfied.
func (me *Handler) serve(ctx *moon.Context, r resolved) error {
	if r.info.IsDir() {
		return me.serveDirectory(ctx, r)
	}

	me.setResponseHeaders(ctx, r)

	if ctx.GetMethod() == moon.MethodHead {
		return nil
	}

	if me.options.byteRanges {
		if seeker, ok := r.file.(io.Seeker); ok {
			result := me.parseRanges(ctx, r)
			if result.isValid {
				if !result.isSatisfiable {
					// A 416 response describes the representation length, and
					// the error handler writes its own body, so the file length
					// set in setResponseHeaders no longer applies.
					ctx.SetHeader("Content-Range", fmt.Sprintf("bytes */%d", r.info.Size()))
					ctx.GetHttpResponseWriter().Header().Del("Content-Length")
					return moon.ErrRequestedRangeNotSatisfiable
				}
				if len(result.ranges) == 1 {
					return me.serveSingleRange(ctx, r, seeker, result.ranges[0])
				}
				return me.serveMultipartRanges(ctx, r, seeker, result.ranges)
			}
		}
	}

	_, err := io.Copy(ctx.GetHttpResponseWriter(), r.file)
	return err
}

// serveDirectory writes the HTML listing of the directory r. The body is
// built in full before anything is written, so its Content-Length is exact
// and a failure to read the entries leaves the response untouched. The
// listing always renders inline: Range headers are ignored and the download
// disposition does not apply.
func (me *Handler) serveDirectory(ctx *moon.Context, r resolved) error {
	dir, ok := r.file.(fs.ReadDirFile)
	if !ok {
		// Nothing can be listed from here, so the request is answered as
		// though browsing were disabled.
		return me.notFound(ctx)
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}

	requestPath := ctx.GetPath()
	parent := ""
	if r.name != me.options.root {
		parent = path.Dir(requestPath)
	}
	body := directoryListing(requestPath, parent, entries)

	me.setCacheControlHeader(ctx)
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	ctx.SetHeader("Content-Length", strconv.Itoa(len(body)))

	if ctx.GetMethod() == moon.MethodHead {
		return nil
	}
	_, err = ctx.GetHttpResponseWriter().Write(body)
	return err
}

// directoryListing renders the HTML listing of a directory: a link to each
// entry, directories first and names compared case-insensitively, with an
// optional link to the parent directory. requestPath is the path the
// listing answers, used for the title and to build the entry links.
func directoryListing(requestPath, parent string, entries []fs.DirEntry) []byte {
	slices.SortFunc(entries, compareDirectoryEntries)

	var b strings.Builder
	b.WriteString("<!doctype html>\n<html>\n<head><meta charset=\"utf-8\"><title>Index of ")
	b.WriteString(html.EscapeString(requestPath))
	b.WriteString("</title></head>\n<body>\n<h1>Index of ")
	b.WriteString(html.EscapeString(requestPath))
	b.WriteString("</h1>\n<ul>\n")
	if parent != "" {
		writeDirectoryEntry(&b, parent, "../")
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		writeDirectoryEntry(&b, path.Join(requestPath, entry.Name()), name)
	}
	b.WriteString("</ul>\n</body>\n</html>\n")
	return []byte(b.String())
}

// writeDirectoryEntry writes one listing entry: href as the link target and
// text as the visible name, both escaped so that a name cannot inject markup
// or change the target.
func writeDirectoryEntry(b *strings.Builder, href, text string) {
	b.WriteString("<li><a href=\"")
	b.WriteString(html.EscapeString(directoryHref(href)))
	b.WriteString("\">")
	b.WriteString(html.EscapeString(text))
	b.WriteString("</a></li>\n")
}

// directoryHref percent-encodes p for use as a link target, leaving the "/"
// separators alone, so a name holding spaces, "#" or other reserved bytes
// still points at the entry being listed.
func directoryHref(p string) string {
	return (&url.URL{Path: p}).EscapedPath()
}

// compareDirectoryEntries orders listing entries: directories first, then by
// name, comparing names case-insensitively and falling back to their exact
// bytes so the order is total.
func compareDirectoryEntries(a, b fs.DirEntry) int {
	if a.IsDir() != b.IsDir() {
		if a.IsDir() {
			return -1
		}
		return 1
	}
	if c := strings.Compare(strings.ToLower(a.Name()), strings.ToLower(b.Name())); c != 0 {
		return c
	}
	return strings.Compare(a.Name(), b.Name())
}

// serveSingleRange writes one range as a 206 response carrying a
// Content-Range header.
func (me *Handler) serveSingleRange(ctx *moon.Context, r resolved, seeker io.Seeker, rng fileRange) error {
	ctx.SetStatusCode(http.StatusPartialContent)
	ctx.SetHeader("Content-Range", rng.contentRange(r.info.Size()))
	ctx.SetHeader("Content-Length", strconv.Itoa(rng.length))

	if _, err := seeker.Seek(int64(rng.offset), io.SeekStart); err != nil {
		return err
	}
	_, err := io.CopyN(ctx.GetHttpResponseWriter(), r.file, int64(rng.length))
	return err
}

// serveMultipartRanges writes several ranges as a 206 multipart/byteranges
// response, one body part per range.
func (me *Handler) serveMultipartRanges(ctx *moon.Context, r resolved, seeker io.Seeker, ranges []fileRange) error {
	size := r.info.Size()
	ctype := contentType(r.name, r.file)
	boundary := multipart.NewWriter(io.Discard).Boundary()
	length, err := multipartRangesLength(boundary, ranges, ctype, size)
	if err != nil {
		return err
	}

	ctx.SetStatusCode(http.StatusPartialContent)
	ctx.SetHeader("Content-Type", "multipart/byteranges; boundary="+boundary)
	ctx.SetHeader("Content-Length", strconv.Itoa(length))

	mw := multipart.NewWriter(ctx.GetHttpResponseWriter())
	if err := mw.SetBoundary(boundary); err != nil {
		return err
	}
	for _, rng := range ranges {
		part, err := mw.CreatePart(rng.mimeHeader(ctype, size))
		if err != nil {
			return err
		}
		if _, err := seeker.Seek(int64(rng.offset), io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(part, r.file, int64(rng.length)); err != nil {
			return err
		}
	}
	return mw.Close()
}

// multipartRangesLength returns the encoded size of the multipart body
// holding ranges, measuring the part headers and boundaries with a counting
// writer instead of buffering any range content.
func multipartRangesLength(boundary string, ranges []fileRange, ctype string, size int64) (int, error) {
	var count countWriter
	mw := multipart.NewWriter(&count)
	if err := mw.SetBoundary(boundary); err != nil {
		return 0, err
	}
	for _, rng := range ranges {
		if _, err := mw.CreatePart(rng.mimeHeader(ctype, size)); err != nil {
			return 0, err
		}
		count += countWriter(rng.length)
	}
	if err := mw.Close(); err != nil {
		return 0, err
	}
	return int(count), nil
}

// countWriter accumulates the number of bytes written to it.
type countWriter int

func (me *countWriter) Write(p []byte) (int, error) {
	*me += countWriter(len(p))
	return len(p), nil
}

// setCacheControlHeader sets the cache lifetime of the response when one is
// configured, shared by file responses and directory listings.
func (me *Handler) setCacheControlHeader(ctx *moon.Context) {
	if me.options.maxAge >= 0 {
		ctx.SetHeader("Cache-Control", "max-age="+strconv.Itoa(me.options.maxAge))
	}
}

// setResponseHeaders sets the headers describing r: the cache lifetime, the
// content type, the content length and, when known, the modification time,
// plus the download disposition when enabled and Accept-Ranges when byte
// ranges are enabled and the file can be served partially. Content-Length
// is always set, so it is correct for HEAD responses too, which carry no
// body.
func (me *Handler) setResponseHeaders(ctx *moon.Context, r resolved) {
	me.setCacheControlHeader(ctx)
	ctx.SetHeader("Content-Type", contentType(r.name, r.file))
	ctx.SetHeader("Content-Length", strconv.FormatInt(r.info.Size(), 10))
	if modTime := r.info.ModTime(); !isZeroTime(modTime) {
		ctx.SetHeader("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	}
	if me.options.download {
		setDownloadHeader(ctx, r.name)
	}
	if me.options.byteRanges {
		if _, ok := r.file.(io.Seeker); ok {
			ctx.SetHeader("Accept-Ranges", "bytes")
		}
	}
}

var unixEpochTime = time.Unix(0, 0)

// isZeroTime reports whether t is obviously unspecified (either zero or Unix()=0).
func isZeroTime(t time.Time) bool {
	return t.IsZero() || t.Equal(unixEpochTime)
}

// contentType returns the Content-Type for a file: the MIME type registered
// for its extension when known, otherwise a sniff of the file contents, and
// "application/octet-stream" as the last resort.
func contentType(name string, file fs.File) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	if t, ok := sniffContentType(file); ok {
		return t
	}
	return "application/octet-stream"
}

// sniffContentType reads a prefix of file to detect its content type and
// rewinds it to its previous position, so serving continues from the start.
// It reports false when file cannot be read and rewound, e.g. it does not
// implement [io.Seeker], so the caller uses a fixed type instead of
// consuming part of the body.
func sniffContentType(file fs.File) (string, bool) {
	// [http.DetectContentType] considers at most the first 512 bytes of data.
	const sniffLen = 512

	seeker, ok := file.(io.Seeker)
	if !ok {
		return "", false
	}
	offset, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", false
	}
	var data [sniffLen]byte
	n, err := io.ReadFull(file, data[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false
	}
	if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
		return "", false
	}
	return http.DetectContentType(data[:n]), true
}

// setDownloadHeader sets Content-Disposition so the client downloads the
// file instead of rendering it. The filename is the base name of name and
// non-ASCII names are encoded with the RFC 2231 syntax.
func setDownloadHeader(ctx *moon.Context, name string) {
	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": path.Base(name),
	})
	if disposition == "" {
		disposition = "attachment"
	}
	ctx.SetHeader("Content-Disposition", disposition)
}

// notFound answers a request that resolved to no file: through
// [WithNotFoundHandler] when configured, and by continuing the chain via
// [moon.Context.Next] otherwise. Continuing lets a later handler serve the
// request when this handler runs as middleware, and resolves to
// [moon.ErrNotFound] when it is the last handler, so a custom error handler
// still sees it.
func (me *Handler) notFound(ctx *moon.Context) error {
	if me.options.notFoundHandler != nil {
		return me.options.notFoundHandler(ctx)
	}
	return ctx.Next()
}

// open opens name from the configured filesystem, or from the operating
// system filesystem when none is configured.
func (me *Handler) open(name string) (fs.File, error) {
	if me.options.fs != nil {
		return me.options.fs.Open(name)
	}
	return os.Open(name)
}

// stripPrefix removes the configured path prefix from name. The exact prefix
// is tried first; when [WithCaseInsensitiveFallback] is enabled, it is retried
// ignoring letter case so a case-variant mount path can still resolve its
// files.
func (me *Handler) stripPrefix(name string) string {
	if stripped, ok := cutPathPrefix(name, me.options.stripPathPrefix, true); ok {
		return stripped
	}
	if me.options.caseInsensitiveFallback {
		if stripped, ok := cutPathPrefix(name, me.options.stripPathPrefix, false); ok {
			return stripped
		}
	}
	return name
}

// cutPathPrefix removes prefix from name and reports whether it was present.
// A leading slash is kept on the result. Comparisons follow caseSensitive.
func cutPathPrefix(name, prefix string, caseSensitive bool) (string, bool) {
	var pathEqual = func(a, b string, caseSensitive bool) bool {
		if caseSensitive {
			return a == b
		}
		return strings.EqualFold(a, b)
	}

	if pathEqual(name, prefix, caseSensitive) {
		return "/", true
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if len(name) < len(prefix) || !pathEqual(name[:len(prefix)], prefix, caseSensitive) {
		return "", false
	}
	return "/" + name[len(prefix):], true
}

// parseRangesResult describes the outcome of parsing the request's Range
// header. The ranges hold the satisfiable ranges in ascending order, and are
// set only when the header is both valid and satisfiable.
type parseRangesResult struct {
	ranges        []fileRange
	isValid       bool
	isSatisfiable bool
}

// fileRange is a run of length bytes starting at offset, both relative to
// the beginning of the representation.
type fileRange struct {
	offset int
	length int
}

// contentRange describes me as a Content-Range value within a representation
// of size bytes, e.g. "bytes 0-99/100".
func (me fileRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", me.offset, me.offset+me.length-1, size)
}

// mimeHeader returns the headers describing me as one part of a
// multipart/byteranges body.
func (me fileRange) mimeHeader(ctype string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Range": {me.contentRange(size)},
		"Content-Type":  {ctype},
	}
}

// parseRanges parses the request's Range header against r. The result is
// invalid, leaving the whole file to be served, for a missing header, a
// malformed one and a unit other than bytes. Otherwise the result is valid;
// it is satisfiable when there is something to serve, and not satisfiable
// when nothing can be, either because every range lies outside the file (an
// empty file has nothing to serve) or because the count stays over
// [WithMaxDistinctRanges] after coalescing.
func (me *Handler) parseRanges(ctx *moon.Context, r resolved) parseRangesResult {
	header := ctx.GetHeader("Range")
	if header == "" {
		return parseRangesResult{}
	}

	unit, set, found := strings.Cut(header, "=")
	if !found || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return parseRangesResult{}
	}
	size := int(r.info.Size())

	var ranges []fileRange
	specs := 0
	for spec := range strings.SplitSeq(set, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			// HTTP list syntax tolerates empty elements.
			continue
		}
		specs++

		start, end, ok := strings.Cut(spec, "-")
		if !ok {
			return parseRangesResult{}
		}
		start, end = strings.TrimSpace(start), strings.TrimSpace(end)

		if start == "" {
			// Suffix-range: the last suffix bytes of the file. Clamping first
			// leaves nothing to serve for -0 and for an empty file.
			suffix, ok := parseByteCount(end)
			if !ok {
				return parseRangesResult{}
			}
			if suffix > size {
				suffix = size
			}
			if suffix == 0 {
				continue
			}
			ranges = append(ranges, fileRange{offset: size - suffix, length: suffix})
			continue
		}

		first, ok := parseByteCount(start)
		if !ok {
			return parseRangesResult{}
		}
		if first >= size {
			// The range starts past the end: valid but unsatisfiable.
			continue
		}
		length := size - first
		if end != "" {
			last, ok := parseByteCount(end)
			if !ok || last < first {
				return parseRangesResult{}
			}
			if last >= size {
				last = size - 1
			}
			length = last - first + 1
		}
		ranges = append(ranges, fileRange{offset: first, length: length})
	}

	if specs == 0 {
		return parseRangesResult{}
	}
	if len(ranges) == 0 {
		return parseRangesResult{isValid: true}
	}

	slices.SortFunc(ranges, func(a, b fileRange) int { return a.offset - b.offset })
	if me.options.coalesceOverlappingRanges {
		ranges = coalesceRanges(ranges)
	}
	if limit := me.options.maxDistinctRanges; limit > 0 && len(ranges) > limit {
		return parseRangesResult{isValid: true}
	}
	return parseRangesResult{ranges: ranges, isValid: true, isSatisfiable: true}
}

// parseByteCount parses a range position or suffix length: a non-empty run
// of decimal digits that fits an int. Values that overflow the parsing are
// rejected.
func parseByteCount(s string) (int, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// coalesceRanges merges ranges that overlap or touch, expecting them sorted
// by offset, turning a request into the fewest possible parts.
func coalesceRanges(ranges []fileRange) []fileRange {
	merged := make([]fileRange, 0, len(ranges))
	for _, rng := range ranges {
		if n := len(merged); n > 0 && rng.offset <= merged[n-1].offset+merged[n-1].length {
			last := &merged[n-1]
			if end := rng.offset + rng.length; end > last.offset+last.length {
				last.length = end - last.offset
			}
			continue
		}
		merged = append(merged, rng)
	}
	return merged
}
