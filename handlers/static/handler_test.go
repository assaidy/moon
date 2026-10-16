package static

import (
	"io"
	"net/http"
	"net/http/httptest"
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
		"index.html":         &fstest.MapFile{Data: []byte("<h1>home</h1>")},
		"hello.txt":          &fstest.MapFile{Data: []byte("hello world")},
		"dated.txt":          &fstest.MapFile{Data: []byte("dated"), ModTime: testModTime},
		"epoch.txt":          &fstest.MapFile{Data: []byte("epoch"), ModTime: time.Unix(0, 0)},
		"noext":              &fstest.MapFile{Data: []byte("plain text without extension")},
		"blob":               &fstest.MapFile{Data: []byte{0x00, 0x01, 0x02, 0x03}},
		"dir/index.html":     &fstest.MapFile{Data: []byte("<h1>dir</h1>")},
		"dir/notes.txt":      &fstest.MapFile{Data: []byte("notes")},
		"custom/default.htm": &fstest.MapFile{Data: []byte("<h1>custom</h1>")},
		"assets/logo.svg":    &fstest.MapFile{Data: []byte("<svg/>")},
		"résumé.txt":         &fstest.MapFile{Data: []byte("cv")},
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
