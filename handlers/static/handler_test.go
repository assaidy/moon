package static

import (
	"bytes"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

// testModTime is a fixed modification time used to assert Last-Modified.
var testModTime = time.Date(2026, time.October, 16, 8, 30, 0, 0, time.UTC)

// newTestFileSystem returns the files mounted below the test root. The
// handler tests register it under the "/files" path prefix.
func newTestFileSystem() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<h1>home</h1>")},
		"hello.txt":             &fstest.MapFile{Data: []byte("hello world")},
		"range.txt":             &fstest.MapFile{Data: []byte("0123456789")},
		"empty.txt":             &fstest.MapFile{},
		"dated.txt":             &fstest.MapFile{Data: []byte("dated"), ModTime: testModTime},
		"epoch.txt":             &fstest.MapFile{Data: []byte("epoch"), ModTime: time.Unix(0, 0)},
		"noext":                 &fstest.MapFile{Data: []byte("plain text without extension")},
		"blob":                  &fstest.MapFile{Data: []byte{0x00, 0x01, 0x02, 0x03}},
		"dir/index.html":        &fstest.MapFile{Data: []byte("<h1>dir</h1>")},
		"dir/notes.txt":         &fstest.MapFile{Data: []byte("notes")},
		"custom/default.htm":    &fstest.MapFile{Data: []byte("<h1>custom</h1>")},
		"assets/logo.svg":       &fstest.MapFile{Data: []byte("<svg/>")},
		"browse/plain.txt":      &fstest.MapFile{Data: []byte("plain")},
		"browse/with space.txt": &fstest.MapFile{Data: []byte("spaced")},
		"browse/hash#tag.txt":   &fstest.MapFile{Data: []byte("hashed")},
		"browse/a&b.txt":        &fstest.MapFile{Data: []byte("amped")},
		"browse/sub/child.txt":  &fstest.MapFile{Data: []byte("child")},
		"résumé.txt":            &fstest.MapFile{Data: []byte("cv")},
	}
}

// newTestHandler returns a handler serving the test filesystem under the
// "/files" path prefix.
func newTestHandler(opts ...Option) *Handler {
	opts = append([]Option{
		WithFileSystem(newTestFileSystem()),
		WithStripPathPrefix("/files"),
	}, opts...)
	return New(opts...)
}

// newTestApp returns an app with the test handler registered for GET and
// HEAD requests under "/files/*".
func newTestApp(t *testing.T, opts ...Option) *moon.App {
	t.Helper()
	app := moon.New()
	h := newTestHandler(opts...)
	app.Map(http.MethodGet, "/files/*", h.Handle)
	app.Map(http.MethodHead, "/files/*", h.Handle)
	return app
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func TestHandle_ServesFile(t *testing.T) {
	app := newTestApp(t)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/hello.txt", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "11", resp.Header.Get("Content-Length"))
	require.Empty(t, resp.Header.Get("Last-Modified"))
	require.Empty(t, resp.Header.Get("Cache-Control"))
	require.Empty(t, resp.Header.Get("Content-Disposition"))
	require.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
	require.Equal(t, "hello world", readBody(t, resp))
}

func TestHandle_ServesIndexFile(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		wantBody string
	}{
		{"root index", "/files", "<h1>home</h1>"},
		{"child index", "/files/dir", "<h1>dir</h1>"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			resp := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
			require.Equal(t, tc.wantBody, readBody(t, resp))
		})
	}
}

func TestHandle_ServesCustomIndexName(t *testing.T) {
	app := newTestApp(t, WithIndexNames([]string{"default.htm"}))

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/custom", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "<h1>custom</h1>", readBody(t, resp))
}

func TestHandle_HeadServesHeadersWithoutBody(t *testing.T) {
	app := newTestApp(t)

	resp := app.Test(httptest.NewRequest(http.MethodHead, "/files/hello.txt", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "11", resp.Header.Get("Content-Length"))
	require.Empty(t, readBody(t, resp))
}

func TestHandle_NotFound(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		app := newTestApp(t)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/missing.txt", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("directory without index", func(t *testing.T) {
		app := newTestApp(t)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/assets", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("custom not-found handler", func(t *testing.T) {
		app := newTestApp(t, WithNotFoundHandler(func(ctx *moon.Context) error {
			return ctx.WriteStatus(http.StatusTeapot)
		}))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/missing.txt", nil))
		require.Equal(t, http.StatusTeapot, resp.StatusCode)
	})
}

func TestHandle_SetsCacheControl(t *testing.T) {
	testCases := []struct {
		name       string
		maxAge     int
		wantHeader string
	}{
		{"positive max age", 60, "max-age=60"},
		{"zero max age", 0, "max-age=0"},
		{"negative max age omits the header", -1, ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t, WithMaxAge(tc.maxAge))

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/hello.txt", nil))
			require.Equal(t, tc.wantHeader, resp.Header.Get("Cache-Control"))
		})
	}
}

func TestHandle_SetsLastModified(t *testing.T) {
	testCases := []struct {
		name       string
		path       string
		wantHeader string
	}{
		{"known modification time", "/files/dated.txt", testModTime.Format(http.TimeFormat)},
		{"zero modification time omits the header", "/files/hello.txt", ""},
		{"unix epoch modification time omits the header", "/files/epoch.txt", ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			resp := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, tc.wantHeader, resp.Header.Get("Last-Modified"))
		})
	}
}

func TestHandle_SetsContentType(t *testing.T) {
	testCases := []struct {
		name       string
		path       string
		wantHeader string
		wantBody   string
	}{
		{
			name:       "known extension",
			path:       "/files/hello.txt",
			wantHeader: "text/plain; charset=utf-8",
			wantBody:   "hello world",
		},
		{
			name:       "sniffed text",
			path:       "/files/noext",
			wantHeader: "text/plain; charset=utf-8",
			wantBody:   "plain text without extension",
		},
		{
			name:       "sniffed binary",
			path:       "/files/blob",
			wantHeader: "application/octet-stream",
			wantBody:   string([]byte{0x00, 0x01, 0x02, 0x03}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			resp := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, tc.wantHeader, resp.Header.Get("Content-Type"))
			require.Equal(t, tc.wantBody, readBody(t, resp))
		})
	}
}

func TestHandle_SetsContentDisposition(t *testing.T) {
	t.Run("ascii filename", func(t *testing.T) {
		app := newTestApp(t, WithDownload(true))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/dir/notes.txt", nil))
		require.Equal(t, "attachment; filename=notes.txt", resp.Header.Get("Content-Disposition"))
	})

	t.Run("non-ascii filename", func(t *testing.T) {
		app := newTestApp(t, WithDownload(true))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/résumé.txt", nil))
		require.Equal(t,
			"attachment; filename*=utf-8''r%C3%A9sum%C3%A9.txt",
			resp.Header.Get("Content-Disposition"),
		)
	})

	t.Run("disabled", func(t *testing.T) {
		app := newTestApp(t)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/hello.txt", nil))
		require.Empty(t, resp.Header.Get("Content-Disposition"))
	})
}

func TestHandle_OtherMethodsContinueChain(t *testing.T) {
	app := moon.New()
	h := newTestHandler()
	app.Use("/files", h.Handle)
	app.Map(http.MethodPost, "/files/*", func(ctx *moon.Context) error {
		return ctx.WriteStatus(http.StatusAccepted)
	})

	resp := app.Test(httptest.NewRequest(http.MethodPost, "/files/hello.txt", nil))
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
}

func TestHandle_ServesByteRange(t *testing.T) {
	testCases := []struct {
		name       string
		header     string
		wantRange  string
		wantLength string
		wantBody   string
	}{
		{"start and end", "bytes=0-3", "bytes 0-3/10", "4", "0123"},
		{"open ended", "bytes=4-", "bytes 4-9/10", "6", "456789"},
		{"suffix", "bytes=-3", "bytes 7-9/10", "3", "789"},
		{"suffix longer than the file", "bytes=-100", "bytes 0-9/10", "10", "0123456789"},
		{"end past the file", "bytes=4-100", "bytes 4-9/10", "6", "456789"},
		{"single last byte", "bytes=9-9", "bytes 9-9/10", "1", "9"},
		{"whole file", "bytes=0-", "bytes 0-9/10", "10", "0123456789"},
		{"unit and whitespace insensitive", "Bytes = 0-3", "bytes 0-3/10", "4", "0123"},
		{"trailing empty element", "bytes=0-3,", "bytes 0-3/10", "4", "0123"},
		{"unsatisfiable ranges are skipped", "bytes=0-3,10-", "bytes 0-3/10", "4", "0123"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
			req.Header.Set("Range", tc.header)
			resp := app.Test(req)
			require.Equal(t, http.StatusPartialContent, resp.StatusCode)
			require.Equal(t, tc.wantRange, resp.Header.Get("Content-Range"))
			require.Equal(t, tc.wantLength, resp.Header.Get("Content-Length"))
			require.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
			require.Equal(t, tc.wantBody, readBody(t, resp))
		})
	}
}

func TestHandle_ServesMultipartByteRanges(t *testing.T) {
	app := newTestApp(t)

	// The ranges are asked for out of order to assert they are sorted.
	req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
	req.Header.Set("Range", "bytes=4-5,0-1")
	resp := app.Test(req)
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Content-Range"))

	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/byteranges", mediaType)
	require.NotEmpty(t, params["boundary"])

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(len(body)), resp.Header.Get("Content-Length"))

	parts := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	nextPart := func(t *testing.T, wantRange, wantBody string) {
		t.Helper()

		part, err := parts.NextPart()
		require.NoError(t, err)
		require.Equal(t, wantRange, part.Header.Get("Content-Range"))
		require.Equal(t, "text/plain; charset=utf-8", part.Header.Get("Content-Type"))
		partBody, err := io.ReadAll(part)
		require.NoError(t, err)
		require.Equal(t, wantBody, string(partBody))
	}
	nextPart(t, "bytes 0-1/10", "01")
	nextPart(t, "bytes 4-5/10", "45")
	_, err = parts.NextPart()
	require.ErrorIs(t, err, io.EOF)
}

func TestHandle_ByteRangeKeepsFileHeaders(t *testing.T) {
	app := newTestApp(t, WithMaxAge(60))

	req := httptest.NewRequest(http.MethodGet, "/files/dated.txt", nil)
	req.Header.Set("Range", "bytes=0-2")
	resp := app.Test(req)
	require.Equal(t, http.StatusPartialContent, resp.StatusCode)
	require.Equal(t, "bytes 0-2/5", resp.Header.Get("Content-Range"))
	require.Equal(t, testModTime.Format(http.TimeFormat), resp.Header.Get("Last-Modified"))
	require.Equal(t, "max-age=60", resp.Header.Get("Cache-Control"))
	require.Equal(t, "dat", readBody(t, resp))
}

func TestHandle_CoalescesByteRanges(t *testing.T) {
	t.Run("overlapping and touching ranges merge", func(t *testing.T) {
		app := newTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes=0-4,2-6,6-7")
		resp := app.Test(req)
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, "bytes 0-7/10", resp.Header.Get("Content-Range"))
		require.Equal(t, "01234567", readBody(t, resp))
	})

	t.Run("disabled serves the ranges as asked", func(t *testing.T) {
		app := newTestApp(t, WithCoalesceOverlappingRanges(false))

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes=0-4,2-6")
		resp := app.Test(req)
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "multipart/byteranges")
	})
}

func TestHandle_RejectsUnsatisfiableByteRanges(t *testing.T) {
	testCases := []struct {
		name   string
		header string
	}{
		{"start past the file", "bytes=10-"},
		{"start and end past the file", "bytes=200-300"},
		{"zero length suffix", "bytes=-0"},
		{"nothing satisfiable", "bytes=-0,10-"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
			req.Header.Set("Range", tc.header)
			resp := app.Test(req)
			require.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
			require.Equal(t, "bytes */10", resp.Header.Get("Content-Range"))
			// The JSON error body replaces the file body, so the file length
			// set for it must not stick around.
			require.Empty(t, resp.Header.Get("Content-Length"))
			require.Contains(t, readBody(t, resp), "request_range_not_satisfiable")
		})
	}
}

func TestHandle_IgnoresInvalidByteRanges(t *testing.T) {
	testCases := []struct {
		name   string
		header string
	}{
		{"missing unit", "0-3"},
		{"unknown unit", "items=0-3"},
		{"non-numeric position", "bytes=a-b"},
		{"reversed range", "bytes=5-2"},
		{"no ranges", "bytes="},
		{"no separator", "bytes"},
		{"negative suffix", "bytes=--1"},
		{"overflowing position", "bytes=99999999999999999999-"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
			req.Header.Set("Range", tc.header)
			resp := app.Test(req)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Empty(t, resp.Header.Get("Content-Range"))
			require.Equal(t, "0123456789", readBody(t, resp))
		})
	}
}

func TestHandle_RejectsByteRangesOnEmptyFile(t *testing.T) {
	testCases := []struct {
		name   string
		header string
	}{
		{"position range", "bytes=0-"},
		{"suffix range", "bytes=-1"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			req := httptest.NewRequest(http.MethodGet, "/files/empty.txt", nil)
			req.Header.Set("Range", tc.header)
			resp := app.Test(req)
			require.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
			require.Equal(t, "bytes */0", resp.Header.Get("Content-Range"))
			require.Empty(t, resp.Header.Get("Content-Length"))
			require.Contains(t, readBody(t, resp), "request_range_not_satisfiable")
		})
	}
}

// unseekableFS hides the [io.Seeker] implementation of the files it opens,
// as a filesystem that cannot serve ranges would.
type unseekableFS struct{ fs.FS }

// Open implements [fs.FS].
func (me unseekableFS) Open(name string) (fs.File, error) {
	file, err := me.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return unseekableFile{File: file}, nil
}

// unseekableFile is an [fs.File] that does not implement [io.Seeker].
type unseekableFile struct{ fs.File }

func TestHandle_IgnoresByteRangesOnUnseekableFile(t *testing.T) {
	app := newTestApp(t, WithFileSystem(unseekableFS{FS: newTestFileSystem()}))

	req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp := app.Test(req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Accept-Ranges"))
	require.Empty(t, resp.Header.Get("Content-Range"))
	require.Equal(t, "0123456789", readBody(t, resp))
}

func TestHandle_HeadIgnoresByteRange(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodHead, "/files/range.txt", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp := app.Test(req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "10", resp.Header.Get("Content-Length"))
	require.Empty(t, resp.Header.Get("Content-Range"))
	require.Empty(t, readBody(t, resp))
}

func TestHandle_ByteRangesDisabled(t *testing.T) {
	app := newTestApp(t, WithByteRanges(false))

	req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp := app.Test(req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Accept-Ranges"))
	require.Empty(t, resp.Header.Get("Content-Range"))
	require.Equal(t, "0123456789", readBody(t, resp))
}

func TestHandle_LimitsDistinctByteRanges(t *testing.T) {
	repeated := strings.TrimSuffix(strings.Repeat("0-0,", 101), ",")

	t.Run("default limit", func(t *testing.T) {
		app := newTestApp(t, WithCoalesceOverlappingRanges(false))

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes="+repeated)
		resp := app.Test(req)
		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
	})

	t.Run("coalesced under the limit", func(t *testing.T) {
		app := newTestApp(t)

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes="+repeated)
		resp := app.Test(req)
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Equal(t, "bytes 0-0/10", resp.Header.Get("Content-Range"))
	})

	t.Run("custom limit", func(t *testing.T) {
		app := newTestApp(t, WithCoalesceOverlappingRanges(false), WithMaxDistinctRanges(2))

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes=0-0,1-1,2-2")
		resp := app.Test(req)
		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
	})

	t.Run("within the limit", func(t *testing.T) {
		app := newTestApp(t, WithCoalesceOverlappingRanges(false), WithMaxDistinctRanges(2))

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes=0-0,2-2")
		resp := app.Test(req)
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "multipart/byteranges")
	})

	t.Run("negative limit removes the limit", func(t *testing.T) {
		app := newTestApp(t, WithCoalesceOverlappingRanges(false), WithMaxDistinctRanges(-1))

		req := httptest.NewRequest(http.MethodGet, "/files/range.txt", nil)
		req.Header.Set("Range", "bytes="+repeated)
		resp := app.Test(req)
		require.Equal(t, http.StatusPartialContent, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "multipart/byteranges")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, strconv.Itoa(len(body)), resp.Header.Get("Content-Length"))
	})
}

func TestWithMaxDistinctRanges_PanicsOnZero(t *testing.T) {
	require.Panics(t, func() { WithMaxDistinctRanges(0) })
}

// unlistableFS hides the [fs.ReadDirFile] implementation of the directories
// it opens, as a filesystem that cannot browse directories would.
type unlistableFS struct{ fs.FS }

// Open implements [fs.FS].
func (me unlistableFS) Open(name string) (fs.File, error) {
	file, err := me.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return unlistableFile{File: file}, nil
}

// unlistableFile is an [fs.File] that does not implement [fs.ReadDirFile].
type unlistableFile struct{ fs.File }

func TestHandle_BrowsesDirectory(t *testing.T) {
	app := newTestApp(t, WithDirectoryBrowsing(true), WithMaxAge(60))

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/browse", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "max-age=60", resp.Header.Get("Cache-Control"))
	require.Empty(t, resp.Header.Get("Content-Disposition"))
	require.Empty(t, resp.Header.Get("Content-Range"))
	require.Empty(t, resp.Header.Get("Accept-Ranges"))
	require.Empty(t, resp.Header.Get("Last-Modified"))

	body := readBody(t, resp)
	require.Equal(t, strconv.Itoa(len(body)), resp.Header.Get("Content-Length"))
	require.Equal(t, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>Index of /files/browse</title></head>
<body>
<h1>Index of /files/browse</h1>
<ul>
<li><a href="/files">../</a></li>
<li><a href="/files/browse/sub">sub/</a></li>
<li><a href="/files/browse/a&amp;b.txt">a&amp;b.txt</a></li>
<li><a href="/files/browse/hash%23tag.txt">hash#tag.txt</a></li>
<li><a href="/files/browse/plain.txt">plain.txt</a></li>
<li><a href="/files/browse/with%20space.txt">with space.txt</a></li>
</ul>
</body>
</html>
`, body)
}

func TestHandle_BrowsesCustomFileSystem(t *testing.T) {
	filesystem := fstest.MapFS{"docs/readme.txt": &fstest.MapFile{Data: []byte("readme")}}

	t.Run("root has no parent link", func(t *testing.T) {
		app := newTestApp(t, WithDirectoryBrowsing(true), WithFileSystem(filesystem))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>Index of /files</title></head>
<body>
<h1>Index of /files</h1>
<ul>
<li><a href="/files/docs">docs/</a></li>
</ul>
</body>
</html>
`, readBody(t, resp))
	})

	t.Run("nested links to the parent", func(t *testing.T) {
		app := newTestApp(t, WithDirectoryBrowsing(true), WithFileSystem(filesystem))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/docs", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>Index of /files/docs</title></head>
<body>
<h1>Index of /files/docs</h1>
<ul>
<li><a href="/files">../</a></li>
<li><a href="/files/docs/readme.txt">readme.txt</a></li>
</ul>
</body>
</html>
`, readBody(t, resp))
	})
}

func TestHandle_BrowsePrefersIndexFile(t *testing.T) {
	app := newTestApp(t, WithDirectoryBrowsing(true))

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/dir", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "<h1>dir</h1>", readBody(t, resp))
}

func TestHandle_HeadBrowsesDirectory(t *testing.T) {
	app := newTestApp(t, WithDirectoryBrowsing(true))

	get := app.Test(httptest.NewRequest(http.MethodGet, "/files/browse", nil))
	wantLength := get.Header.Get("Content-Length")
	require.NotEmpty(t, wantLength)

	resp := app.Test(httptest.NewRequest(http.MethodHead, "/files/browse", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, wantLength, resp.Header.Get("Content-Length"))
	require.Empty(t, readBody(t, resp))
}

func TestHandle_BrowseIgnoresRangeAndDownload(t *testing.T) {
	app := newTestApp(t, WithDirectoryBrowsing(true), WithDownload(true))

	req := httptest.NewRequest(http.MethodGet, "/files/browse", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp := app.Test(req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Content-Range"))
	require.Empty(t, resp.Header.Get("Accept-Ranges"))
	require.Empty(t, resp.Header.Get("Content-Disposition"))
	require.Contains(t, readBody(t, resp), "Index of /files/browse")
}

func TestHandle_BrowseWithoutReadDirFile(t *testing.T) {
	app := newTestApp(t,
		WithDirectoryBrowsing(true),
		WithFileSystem(unlistableFS{FS: newTestFileSystem()}),
	)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/browse", nil))
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
