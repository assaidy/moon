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
// type, content length and, when known, their modification time; GET
// requests carrying a Range header are answered with the requested bytes
// when the file can be seeked, following the rules of [WithByteRanges].
// Requests with a method other than GET and HEAD continue the chain.
package static

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
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
// [WithNotFoundHandler] or [moon.ErrNotFound]. Errors are returned to the
// caller: the error of writing the body, or
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
// methods other than HEAD, its body, either whole or the ranges asked for
// by a Range header. It returns the error of writing the body, or
// [moon.ErrRequestedRangeNotSatisfiable] when the requested ranges cannot
// be satisfied.
func (me *Handler) serve(ctx *moon.Context, r resolved) error {
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

// setResponseHeaders sets the headers describing r: the cache lifetime, the
// content type, the content length and, when known, the modification time,
// plus the download disposition when enabled and Accept-Ranges when byte
// ranges are enabled and the file can be served partially. Content-Length
// is always set, so it is correct for HEAD responses too, which carry no
// body.
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
