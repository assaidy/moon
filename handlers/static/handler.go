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
// directory is replaced by the first of [WithIndexNames] that exists in it.
// When nothing matches, the request is answered by [WithNotFoundHandler]
// or, by default, [moon.ErrNotFound]. Files are served with their content
// type, content length and, when known, their modification time. Requests
// with a method other than GET and HEAD continue the chain.
package static

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
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
		root:       ".",
		indexNames: []string{"index.html"},
		maxAge:     -1,
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
	skip              func(*moon.Context) bool
	root              string
	indexNames        []string
	stripPathPrefix   string
	fs                fs.FS
	download          bool
	byteRange         bool
	directoryBrowsing bool
	maxAge            int
	notFoundHandler   moon.Handler
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
// in order. The first one that exists in the directory is served. Pass it
// to [New].
//
// Default: []string{"index.html"}
func WithIndexNames(names []string) Option {
	return func(o *options) {
		o.indexNames = names
	}
}

// WithStripPathPrefix sets a path prefix removed from the request path
// before resolving it, for mounting the handler under a route prefix. For
// example, with "/static" a request for "/static/css/app.css" serves
// "css/app.css" below the root. Pass it to [New].
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

// TODO: WithByteRange
func WithByteRange(b bool) Option {
	panic("unimplemented")
}

// TODO: WithDirectoryBrowsing
func WithDirectoryBrowsing(b bool) Option {
	panic("unimplemented")
}

// WithMaxAge sets the max-age directive of the Cache-Control response
// header, the number of seconds a client may cache the served file. Zero
// and positive values are written, a negative value omits the header. Pass
// it to [New].
//
// Default: -1 (the header is omitted)
func WithMaxAge(i int) Option {
	return func(o *options) {
		o.maxAge = i
	}
}

// WithNotFoundHandler sets the handler answering requests that resolve to
// no file, instead of [moon.ErrNotFound]. It is not consulted for failures
// while writing the body. Pass it to [New].
//
// Default: nil ([moon.ErrNotFound])
func WithNotFoundHandler(h moon.Handler) Option {
	return func(o *options) {
		o.notFoundHandler = h
	}
}

// Handle serves the request. Requests skipped by [WithSkip] and requests
// with a method other than GET and HEAD continue the chain via
// [moon.Context.Next]. A request that resolves to no file is answered by
// [WithNotFoundHandler] or [moon.ErrNotFound]; the error of writing the
// body is returned to the caller.
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

// resolved is the file a request is served from, along with the metadata
// needed to describe it in the response headers.
type resolved struct {
	// file is the opened file, closed by the caller.
	file fs.File
	// info describes the opened file; its size and modification time fill
	// the Content-Length and Last-Modified headers.
	info fs.FileInfo
	// name is the resolved path used to build header values, such as the
	// content type and the download filename.
	name string
}

// resolve maps the request path to a file: any configured prefix is
// stripped, the path is joined with the root and opened, and a directory is
// replaced by its first existing index file. The returned error is never
// exposed to the client, only used to fall back to the not-found path.
func (me *Handler) resolve(ctx *moon.Context) (resolved, error) {
	p := ctx.GetPath()
	if me.options.stripPathPrefix != "" {
		p = stripPathPrefix(p, me.options.stripPathPrefix)
	}

	name := path.Join(me.options.root, p)
	file, err := me.open(name)
	if err != nil {
		return resolved{}, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return resolved{}, err
	}

	if info.IsDir() {
		r, err := me.resolveIndex(name)
		file.Close()
		return r, err
	}

	return resolved{file: file, info: info, name: name}, nil
}

// resolveIndex opens the first of [WithIndexNames] that exists as a file in
// the directory dir, or reports an error when none does.
func (me *Handler) resolveIndex(dir string) (resolved, error) {
	for _, index := range me.options.indexNames {
		name := path.Join(dir, index)
		file, err := me.open(name)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			file.Close()
			continue
		}
		return resolved{file: file, info: info, name: name}, nil
	}
	return resolved{}, fs.ErrNotExist
}

// serve writes r as the response: the headers describing it and, for
// methods other than HEAD, its body. It returns only the error of writing
// the body.
func (me *Handler) serve(ctx *moon.Context, r resolved) error {
	me.setResponseHeaders(ctx, r)

	if ctx.GetMethod() == moon.MethodHead {
		return nil
	}

	_, err := io.Copy(ctx.GetHttpResponseWriter(), r.file)
	return err
}

// setResponseHeaders sets the headers describing r: the cache lifetime, the
// content type, the content length and, when known, the modification time,
// plus the download disposition when enabled. Content-Length is always set,
// so it is correct for HEAD responses too, which carry no body.
func (me *Handler) setResponseHeaders(ctx *moon.Context, r resolved) {
	if me.options.maxAge >= 0 {
		ctx.SetHeader("Cache-Control", "max-age="+strconv.Itoa(me.options.maxAge))
	}
	ctx.SetHeader("Content-Type", contentType(r.name, r.file))
	ctx.SetHeader("Content-Length", strconv.FormatInt(r.info.Size(), 10))
	if modTime := r.info.ModTime(); !isZeroTime(modTime) {
		ctx.SetHeader("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	}
	if me.options.download {
		setDownloadHeader(ctx, r.name)
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

// notFound answers a request that resolved to no file, through
// [WithNotFoundHandler] when configured and [moon.ErrNotFound] otherwise.
func (me *Handler) notFound(ctx *moon.Context) error {
	if me.options.notFoundHandler != nil {
		return me.options.notFoundHandler(ctx)
	}
	return moon.ErrNotFound
}

// open opens name from the configured filesystem, or from the operating
// system filesystem when none is configured.
func (me *Handler) open(name string) (fs.File, error) {
	if me.options.fs != nil {
		return me.options.fs.Open(name)
	}
	return os.Open(name)
}

// stripPathPrefix removes prefix from name: the path is expected to be
// normalized (lowercase, no trailing slash, leading '/'), so the prefix may
// be followed by a slash or the end of the path.
func stripPathPrefix(name, prefix string) string {
	if name == prefix {
		return "/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if after, found := strings.CutPrefix(name, prefix); found {
		return "/" + after
	}
	return name
}
