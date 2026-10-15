package static

import (
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/assaidy/moon"
)

type Handler struct {
	options options
}

func New(opts ...Option) *Handler {
	options := options{
		root:       ".",
		indexNames: []string{"index.html"},
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

type options struct {
	skip            func(*moon.Context) bool
	root            string
	indexNames      []string
	stripPathPrefix string
	fs              fs.FS
	download        bool
}

type Option func(*options)

func WithSkip(f func(*moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

func WithRoot(s string) Option {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "root cannot be empty")

	return func(o *options) {
		o.root = path.Clean(s)
	}
}

func WithIndexNames(names []string) Option {
	return func(o *options) {
		o.indexNames = names
	}
}

func WithStripPathPrefix(s string) Option {
	return func(o *options) {
		o.stripPathPrefix = path.Clean(s)
	}
}

func WithFileSystem(fs fs.FS) Option {
	return func(o *options) {
		o.fs = fs
	}
}

func WithDownload(b bool) Option {
	return func(o *options) {
		o.download = b
	}
}

func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	if m := ctx.GetMethod(); m != moon.MethodHead && m != moon.MethodGet {
		return ctx.Next()
	}

	// TODO: only write headers for HEAD requests.
	// TODO: set headers like content-length, content-type

	p := ctx.GetPath()
	if me.options.stripPathPrefix != "" {
		p = stripPathPrefix(p, me.options.stripPathPrefix)
	}

	filePath := path.Join(me.options.root, p)
	file, err := me.open(filePath)
	if err != nil {
		return ctx.Next()
	}

	// Check index files if a directory.
	if stat, err := file.Stat(); err != nil {
		return ctx.Next()
	} else if stat.IsDir() {
		for _, index := range me.options.indexNames {
			indexPath := path.Join(filePath, index)
			indexFile, err := me.open(indexPath)
			if err != nil {
				continue
			}
			me.handleDownload(ctx, indexPath)
			_, err = io.Copy(ctx.GetHttpResponseWriter(), indexFile)
			return err
		}
		return ctx.Next()
	}

	me.handleDownload(ctx, filePath)
	_, err = io.Copy(ctx.GetHttpResponseWriter(), file)
	return err
}

func (me *Handler) open(name string) (fs.File, error) {
	if me.options.fs != nil {
		return me.options.fs.Open(name)
	}
	return os.Open(name)
}

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

func (me *Handler) handleDownload(ctx *moon.Context, name string) {
	if me.options.download {
		if hasNonAscii(name) {
			ctx.SetHeader("Content-Disposition", "attachment; filename*="+name)
		} else {
			ctx.SetHeader("Content-Disposition", "attachment; filename="+name)
		}
	}
}

func hasNonAscii(s string) bool {
	for i := range s {
		if s[i] > 127 {
			return true
		}
	}
	return false
}
