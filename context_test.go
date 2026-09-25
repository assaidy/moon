package moon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestContext_GetMethod(t *testing.T) {
	expected := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodHead,
		http.MethodOptions,
		http.MethodConnect,
		http.MethodTrace,
	}
	var acutal []string

	app := New()
	app.Use("/", func(ctx *Context) error {
		acutal = append(acutal, ctx.GetMethod())
		return nil
	})

	for _, method := range expected {
		app.Test(httptest.NewRequest(method, "/", nil))
	}

	require.Equal(t, expected, acutal)
}

func TestContext_GetPath(t *testing.T) {
	expected := []string{
		"/",
		"/api",
		"/api/v1/users",
		"/users",
		"/users/123",
		"/users/123/profile",
		"/a-b_c/d-e_f",
		"/files/a/b/c.txt",
	}
	var acutal []string

	app := New()
	app.Use("/", func(ctx *Context) error {
		acutal = append(acutal, ctx.GetPath())
		return nil
	})

	for _, path := range expected {
		app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	}

	require.Equal(t, expected, acutal)
}

func TestContext_GetPattern(t *testing.T) {
	t.Run("with Map()", func(t *testing.T) {
		testCases := []struct {
			pattern string
			path    string
		}{
			{pattern: "/", path: "/"},
			{pattern: "/api", path: "/api"},
			{pattern: "/api/v1/users", path: "/api/v1/users"},
			{pattern: "/users/:id", path: "/users/123"},
			{pattern: "/users/:user_id/posts/:post_id", path: "/users/123/posts/456"},
			{pattern: "/files/*", path: "/files/a/b/c.txt"},
			{pattern: "/static/*/posts", path: "/static/abc/posts"},
		}
		expected := make([]string, 0, len(testCases))
		for _, tc := range testCases {
			expected = append(expected, tc.pattern)
		}
		var acutal []string

		app := New()
		for _, tc := range testCases {
			app.Map(http.MethodGet, tc.pattern, func(ctx *Context) error {
				acutal = append(acutal, ctx.GetPattern())
				return nil
			})
		}

		for _, tc := range testCases {
			app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
		}

		require.Equal(t, expected, acutal)
	})

	t.Run("with Use() pattern is empty", func(t *testing.T) {
		paths := []string{
			"/",
			"/api",
			"/api/v1/users",
			"/users/123",
			"/files/a/b/c.txt",
		}
		var actualPatterns []string
		var actualPaths []string

		useApp := New()
		useApp.Use("/", func(ctx *Context) error {
			actualPatterns = append(actualPatterns, ctx.GetPattern())
			actualPaths = append(actualPaths, ctx.GetPath())
			return nil
		})

		for _, path := range paths {
			useApp.Test(httptest.NewRequest(http.MethodGet, path, nil))
		}

		require.Equal(t, []string{"", "", "", "", ""}, actualPatterns)
		require.Equal(t, paths, actualPaths)
	})
}

func TestContext_GetUrl(t *testing.T) {
	expected := []string{
		"/",
		"/api",
		"/api/v1/users",
		"/users/123?verbose=true",
		"/api/v1/users?page=1&limit=10",
		"/files/a/b/c.txt?download=1",
		"/search?q=hello+world&lang=en",
	}
	var actual []string

	app := New()
	app.Use("/", func(ctx *Context) error {
		actual = append(actual, ctx.GetUrl().String())
		return nil
	})

	for _, target := range expected {
		app.Test(httptest.NewRequest(http.MethodGet, target, nil))
	}

	require.Equal(t, expected, actual)
}

func TestContext_Queries(t *testing.T) {
	testCases := []struct {
		target      string
		queryString string
		queries     map[string][]string
	}{
		{
			target:      "/",
			queryString: "",
			queries:     map[string][]string{},
		},
		{
			target:      "/api",
			queryString: "",
			queries:     map[string][]string{},
		},
		{
			target:      "/users?page=1",
			queryString: "page=1",
			queries:     map[string][]string{"page": {"1"}},
		},
		{
			target:      "/users?page=1&limit=10",
			queryString: "page=1&limit=10",
			queries:     map[string][]string{"page": {"1"}, "limit": {"10"}},
		},
		{
			target:      "/search?q=hello+world&lang=en",
			queryString: "q=hello+world&lang=en",
			queries:     map[string][]string{"q": {"hello world"}, "lang": {"en"}},
		},
		{
			target:      "/tags?tag=a&tag=b",
			queryString: "tag=a&tag=b",
			queries:     map[string][]string{"tag": {"a", "b"}},
		},
	}

	app := New()
	index := 0
	app.Use("/", func(ctx *Context) error {
		tc := testCases[index]
		index++

		require.Equal(t, tc.queryString, ctx.GetQueryString(), "target: %s", tc.target)
		for key, values := range tc.queries {
			require.Equal(t, values[0], ctx.GetQuery(key), "target: %s key: %s", tc.target, key)
		}
		require.Equal(t, tc.queries, ctx.GetAllQueries(), "target: %s", tc.target)
		require.Equal(t, "", ctx.GetQuery("missing"), "target: %s", tc.target)
		return nil
	})

	for _, tc := range testCases {
		app.Test(httptest.NewRequest(http.MethodGet, tc.target, nil))
	}

	require.Equal(t, len(testCases), index)
}

func TestContext_Params(t *testing.T) {
	testCases := []struct {
		pattern string
		path    string
		params  map[string]string
	}{
		{
			pattern: "/api",
			path:    "/api",
			params:  map[string]string{},
		},
		{
			pattern: "/users/:id",
			path:    "/users/123",
			params:  map[string]string{"id": "123"},
		},
		{
			pattern: "/users/:user_id/posts/:post_id",
			path:    "/users/123/posts/456",
			params:  map[string]string{"user_id": "123", "post_id": "456"},
		},
		{
			pattern: "/api/:version-id",
			path:    "/api/v2",
			params:  map[string]string{"version-id": "v2"},
		},
		{
			pattern: "/:category/:slug",
			path:    "/books/go-programming",
			params:  map[string]string{"category": "books", "slug": "go-programming"},
		},
	}

	app := New()
	for _, tc := range testCases {
		app.Map(http.MethodGet, tc.pattern, func(ctx *Context) error {
			for key, value := range tc.params {
				require.Equal(t, value, ctx.GetParam(key), "pattern: %s path: %s key: %s", tc.pattern, tc.path, key)
			}
			require.Equal(t, tc.params, ctx.GetAllParams(), "pattern: %s path: %s", tc.pattern, tc.path)
			require.Equal(t, "", ctx.GetParam("missing"), "pattern: %s path: %s", tc.pattern, tc.path)
			return nil
		})
	}

	for _, tc := range testCases {
		app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
	}
}

func TestContext_GetRemoteAddress(t *testing.T) {
	expected := []string{
		"192.168.1.1:1234",
		"10.0.0.1:8080",
		"[2001:db8::1]:443",
	}
	var actual []string

	app := New()
	app.Use("/", func(ctx *Context) error {
		actual = append(actual, ctx.GetRemoteAddress())
		return nil
	})

	for _, addr := range expected {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		app.Test(req)
	}

	require.Equal(t, expected, actual)
}

func TestContext_Headers(t *testing.T) {
	app := New()
	var ran bool
	app.Use("/", func(ctx *Context) error {
		ran = true

		require.Equal(t, "req-val", ctx.GetHeader("X-Req"))
		require.Equal(t, "", ctx.GetHeader("X-Missing"))
		require.Equal(t, "a", ctx.GetHeader("X-Multi"))
		require.Equal(t, []string{"a", "b"}, ctx.GetAllHeaders("X-Multi"))
		require.Empty(t, ctx.GetAllHeaders("X-Missing"))

		ctx.SetHeader("X-Resp", "c")
		require.Equal(t, "c", ctx.response.Header().Get("X-Resp"))
		ctx.SetHeader("X-Resp", "d")
		require.Equal(t, "d", ctx.response.Header().Get("X-Resp"))

		ctx.AddHeader("X-Multi-Resp", "1")
		ctx.AddHeader("X-Multi-Resp", "2")
		require.Equal(t, "1", ctx.response.Header().Get("X-Multi-Resp"))
		require.Equal(t, []string{"1", "2"}, ctx.response.Header()["X-Multi-Resp"])

		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Req", "req-val")
	req.Header.Add("X-Multi", "a")
	req.Header.Add("X-Multi", "b")
	resp := app.Test(req)

	require.True(t, ran)
	require.Equal(t, "d", resp.Header.Get("X-Resp"))
	require.Equal(t, []string{"1", "2"}, resp.Header.Values("X-Multi-Resp"))
}

func TestContext_ContextMirror(t *testing.T) {
	t.Run("value mirrors request context", func(t *testing.T) {
		type ctxKey string
		const key ctxKey = "k"

		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "v", ctx.Value(key))
			require.Equal(t, ctx.request.Context().Value(key), ctx.Value(key))
			require.Nil(t, ctx.Value("missing"))
			return nil
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), key, "v"))
		app.Test(req)
	})

	t.Run("deadline mirrors request context", func(t *testing.T) {
		t.Run("with deadline", func(t *testing.T) {
			deadline := time.Now().Add(time.Hour)

			app := New()
			app.Use("/", func(ctx *Context) error {
				gotDeadline, gotOK := ctx.Deadline()
				wantDeadline, wantOK := ctx.request.Context().Deadline()
				require.Equal(t, wantOK, gotOK)
				require.True(t, gotOK)
				require.True(t, wantDeadline.Equal(gotDeadline))
				require.NoError(t, ctx.Err())
				return nil
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			reqCtx, cancel := context.WithDeadline(req.Context(), deadline)
			defer cancel()
			app.Test(req.WithContext(reqCtx))
		})

		t.Run("without deadline", func(t *testing.T) {
			app := New()
			app.Use("/", func(ctx *Context) error {
				_, ok := ctx.Deadline()
				require.False(t, ok)
				require.NoError(t, ctx.Err())
				return nil
			})
			app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		})
	})

	t.Run("cancelled context mirrors done and err", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.NotNil(t, ctx.Done())
			require.Equal(t, context.Canceled, ctx.Err())
			require.Equal(t, ctx.request.Context().Err(), ctx.Err())
			select {
			case <-ctx.Done():
			default:
				t.Fatal("expected done channel to be closed")
			}
			return nil
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		cancel()
		app.Test(req.WithContext(reqCtx))
	})
}

type failCodec struct{}

func (failCodec) Encode(any) ([]byte, error) { return nil, errors.New("encode boom") }
func (failCodec) Decode([]byte, any) error   { return errors.New("decode boom") }
func (failCodec) ContentType() string        { return "application/fail" }

func TestContext_Body(t *testing.T) {
	t.Run("Read empty", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			raw, err := ctx.Read()
			require.NoError(t, err)
			require.Empty(t, raw)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodPost, "/", nil))
	})

	t.Run("Read plain", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			raw, err := ctx.Read()
			require.NoError(t, err)
			require.Equal(t, "hello world", string(raw))
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello world")))
	})

	t.Run("Read twice second empty", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			first, err := ctx.Read()
			require.NoError(t, err)
			require.Equal(t, "hello", string(first))

			second, err := ctx.Read()
			require.NoError(t, err)
			require.Empty(t, second)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello")))
	})

	t.Run("ReadAs json ok", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			var out map[string]string
			require.NoError(t, ctx.ReadAs(CodecJson, &out))
			require.Equal(t, map[string]string{"hello": "world"}, out)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hello":"world"}`)))
	})

	t.Run("ReadAs invalid err", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			var out map[string]string
			require.Error(t, ctx.ReadAs(CodecJson, &out))
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{invalid")))
	})

	t.Run("Write string", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			return ctx.Write(http.StatusCreated, "hello")
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "hello", string(raw))
	})

	t.Run("Write bytes", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, []byte("raw-bytes"))
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "raw-bytes", string(raw))
	})

	t.Run("WriteStatus", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			return ctx.WriteStatus(http.StatusNotFound)
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusText(http.StatusNotFound), string(raw))
	})

	t.Run("WriteAs json", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			return ctx.WriteAs(http.StatusOK, CodecJson, map[string]string{"hello": "world"})
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, `{"hello":"world"}`, string(raw))
	})

	t.Run("WriteAs encode err", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			return ctx.WriteAs(http.StatusOK, failCodec{}, map[string]string{"hello": "world"})
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

func TestContext_StatusCode(t *testing.T) {
	t.Run("unwritten is 0, wire is 200", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, 0, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("after Write", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.NoError(t, ctx.Write(http.StatusCreated, "hello"))
			require.Equal(t, http.StatusCreated, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	})

	t.Run("after WriteStatus", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.NoError(t, ctx.WriteStatus(http.StatusTeapot))
			require.Equal(t, http.StatusTeapot, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusTeapot, resp.StatusCode)
	})

	t.Run("observed after Next", func(t *testing.T) {
		app := New()
		app.Map(http.MethodGet, "/code",
			func(ctx *Context) error {
				require.Equal(t, 0, ctx.GetStatusCode())
				require.NoError(t, ctx.Next())
				require.Equal(t, http.StatusCreated, ctx.GetStatusCode())
				return nil
			},
			func(ctx *Context) error {
				return ctx.Write(http.StatusCreated, "hello")
			},
		)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/code", nil))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	})

	t.Run("zero means writable", func(t *testing.T) {
		app := New()
		var before, after int
		app.Map(http.MethodGet, "/writable",
			func(ctx *Context) error {
				before = ctx.GetStatusCode()
				require.NoError(t, ctx.Next())
				after = ctx.GetStatusCode()
				return nil
			},
			func(ctx *Context) error { return nil },
		)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/writable", nil))
		require.Equal(t, 0, before)
		require.Equal(t, 0, after)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("SetStatusCode sets code without body", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetStatusCode(http.StatusNoContent)
			require.Equal(t, http.StatusNoContent, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Empty(t, raw)
	})

	t.Run("SetStatusCode buffers, headers before and after apply", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetHeader("X-Custom", "yes")
			ctx.SetStatusCode(http.StatusAccepted)
			ctx.SetHeader("X-Custom", "no")
			require.Equal(t, http.StatusAccepted, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusAccepted, resp.StatusCode)
		require.Equal(t, "no", resp.Header.Get("X-Custom"))
	})

	t.Run("header set after Next applies", func(t *testing.T) {
		app := New()
		app.Map(http.MethodGet, "/timed",
			func(ctx *Context) error {
				require.NoError(t, ctx.Next())
				ctx.SetHeader("X-Response-Time", "1ms")
				return nil
			},
			func(ctx *Context) error {
				return ctx.Write(http.StatusOK, "hello")
			},
		)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/timed", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "1ms", resp.Header.Get("X-Response-Time"))
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "hello", string(raw))
	})

	t.Run("last status wins", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.NoError(t, ctx.Write(http.StatusCreated, "hello"))
			ctx.SetStatusCode(http.StatusAccepted)
			require.Equal(t, http.StatusAccepted, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusAccepted, resp.StatusCode)
	})
}

func TestContext_RawWriter(t *testing.T) {
	t.Run("pure raw path skips buffered flush", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			raw := ctx.response.Unwrap()
			raw.Header().Set("X-Raw", "yes")
			raw.WriteHeader(http.StatusCreated)
			_, err := raw.Write([]byte("raw-body"))
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, ctx.GetStatusCode())
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		require.Equal(t, "yes", resp.Header.Get("X-Raw"))
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "raw-body", string(raw))
	})

	t.Run("raw flush marks raw without panic", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			raw := ctx.response.Unwrap()
			raw.Header().Set("X-Flush", "yes")
			http.NewResponseController(raw).Flush()
			_, err := raw.Write([]byte("stream"))
			require.NoError(t, err)
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, "yes", resp.Header.Get("X-Flush"))
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "stream", string(raw))
	})

	t.Run("mixed raw wins over buffered", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.NoError(t, ctx.Write(http.StatusOK, "buffered"))
			_, err := ctx.response.Unwrap().Write([]byte("raw"))
			require.NoError(t, err)
			return nil
		})

		var resp *http.Response
		require.NotPanics(t, func() {
			resp = app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		})
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "raw", string(raw))
	})

	t.Run("raw header use discards buffered write", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.response.Unwrap().Header().Set("X-Raw", "yes")
			return ctx.Write(http.StatusOK, "buffered")
		})

		var resp *http.Response
		require.NotPanics(t, func() {
			resp = app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		})
		require.Equal(t, "yes", resp.Header.Get("X-Raw"))
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Empty(t, raw)
	})

	t.Run("raw headers only defaults status to 200", func(t *testing.T) {
		var captured *Context

		app := New()
		app.Use("/", func(ctx *Context) error {
			captured = ctx
			ctx.response.Unwrap().Header().Set("X-Raw", "yes")
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "yes", resp.Header.Get("X-Raw"))
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Empty(t, raw)
		require.NotNil(t, captured)
		require.Equal(t, http.StatusOK, captured.GetStatusCode())
	})
}

func TestContext_HeadersSetBeforeRawWriteArePreserved(t *testing.T) {
	app := New()
	app.Use("/", func(ctx *Context) error {
		ctx.SetHeader("Content-Type", "text/event-stream")
		ctx.SetHeader("Cache-Control", "no-cache")
		_, err := ctx.GetHttpResponseWriter().Unwrap().Write([]byte("data: hi\n\n"))
		require.NoError(t, err)
		return nil
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	require.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "data: hi\n\n", string(raw))
}

func TestContext_Forms(t *testing.T) {
	newFormRequest := func(target, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req
	}

	t.Run("GetFormValue from query", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "ann", ctx.GetFormValue("name"))
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/?name=ann", nil))
	})

	t.Run("GetFormValue from body", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "bob", ctx.GetFormValue("name"))
			return nil
		})

		app.Test(newFormRequest("/", "name=bob"))
	})

	t.Run("GetFormValue multiple query values first wins", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "q1", ctx.GetFormValue("tag"))
			require.Equal(t, []string{"q1", "q2"}, map[string][]string(ctx.GetAllFormValues())["tag"])
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/?tag=q1&tag=q2", nil))
	})

	t.Run("GetFormValue multiple body values first wins", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "b1", ctx.GetFormValue("tag"))
			require.Equal(t, []string{"b1", "b2"}, map[string][]string(ctx.GetAllFormValues())["tag"])
			return nil
		})

		app.Test(newFormRequest("/", "tag=b1&tag=b2"))
	})

	t.Run("GetFormValue body values before query values", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "b1", ctx.GetFormValue("tag"))
			require.Equal(t, []string{"b1", "b2", "q1", "q2"}, map[string][]string(ctx.GetAllFormValues())["tag"])
			return nil
		})

		app.Test(newFormRequest("/?tag=q1&tag=q2", "tag=b1&tag=b2"))
	})
	t.Run("GetFormValue missing", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "", ctx.GetFormValue("missing"))
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("GetAllFormValues merged query and body", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t,
				map[string][]string{"a": {"1"}, "b": {"3", "2"}, "c": {"4"}},
				map[string][]string(ctx.GetAllFormValues()),
			)
			return nil
		})

		app.Test(newFormRequest("/?a=1&b=2", "b=3&c=4"))
	})

	t.Run("GetAllFormValues empty", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Empty(t, ctx.GetAllFormValues())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestContext_Cookies(t *testing.T) {
	t.Run("SetCookie header", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetCookie(&http.Cookie{Name: "sess", Value: "abc", Path: "/"})
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Contains(t, resp.Header.Get("Set-Cookie"), "sess=abc")
		found := false
		for _, c := range resp.Cookies() {
			if c.Name == "sess" {
				found = true
				require.Equal(t, "abc", c.Value)
			}
		}
		require.True(t, found, "expected sess cookie in response")
	})

	t.Run("GetCookie hit and miss", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Equal(t, "abc", ctx.GetCookie("sess"))
			require.Equal(t, "", ctx.GetCookie("missing"))
			return nil
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "sess", Value: "abc"})
		app.Test(req)
	})

	t.Run("GetManyCookies", func(t *testing.T) {
		cases := []struct {
			name    string
			cookies [][2]string
			want    []string
		}{
			{name: "none", cookies: nil, want: nil},
			{name: "one", cookies: [][2]string{{"sess", "a"}}, want: []string{"a"}},
			{name: "many", cookies: [][2]string{{"sess", "a"}, {"sess", "b"}}, want: []string{"a", "b"}},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				app := New()
				app.Use("/", func(ctx *Context) error {
					require.Equal(t, tc.want, ctx.GetManyCookies("sess"))
					return nil
				})

				req := httptest.NewRequest(http.MethodGet, "/", nil)
				for _, c := range tc.cookies {
					req.AddCookie(&http.Cookie{Name: c[0], Value: c[1]})
				}
				app.Test(req)
			})
		}
	})

	t.Run("GetAllCookies grouped and nil", func(t *testing.T) {
		t.Run("grouped", func(t *testing.T) {
			app := New()
			app.Use("/", func(ctx *Context) error {
				require.Equal(t,
					map[string][]string{"sess": {"a", "b"}, "other": {"c"}},
					ctx.GetAllCookies(),
				)
				return nil
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.AddCookie(&http.Cookie{Name: "sess", Value: "a"})
			req.AddCookie(&http.Cookie{Name: "sess", Value: "b"})
			req.AddCookie(&http.Cookie{Name: "other", Value: "c"})
			app.Test(req)
		})

		t.Run("nil", func(t *testing.T) {
			app := New()
			app.Use("/", func(ctx *Context) error {
				require.Nil(t, ctx.GetAllCookies())
				return nil
			})

			app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		})
	})

	t.Run("ClearCookie expired output", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.ClearCookie("sess")
			return nil
		})

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: "sess", Value: "abc"})
		resp := app.Test(req)

		header := resp.Header.Get("Set-Cookie")
		require.Contains(t, header, "sess=")
		require.Contains(t, header, "Max-Age=0")
		require.Contains(t, header, "Expires=Thu, 01 Jan 1970 00:00:00 GMT")
	})

	t.Run("ClearCookie missing no output", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.ClearCookie("nope")
			return nil
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, "", resp.Header.Get("Set-Cookie"))
	})
}

func TestContext_Locals(t *testing.T) {
	t.Run("set get hit", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetLocal("user", "ann")

			v, ok := ctx.GetLocal[string]("user")
			require.True(t, ok)
			require.Equal(t, "ann", v)

			anyV, anyOK := ctx.GetAnyLocal[string]("user")
			require.True(t, anyOK)
			require.Equal(t, "ann", anyV)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("wrong type miss", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetLocal("n", 42)

			v, ok := ctx.GetLocal[string]("n")
			require.False(t, ok)
			require.Equal(t, "", v)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("missing miss", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			v, ok := ctx.GetLocal[string]("nope")
			require.False(t, ok)
			require.Equal(t, "", v)

			anyV, anyOK := ctx.GetAnyLocal[string]("nope")
			require.False(t, anyOK)
			require.Nil(t, anyV)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("empty key nil value panic", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.Panics(t, func() { ctx.SetLocal("", "x") })
			require.Panics(t, func() { ctx.SetLocal("k", nil) })
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("delete and double delete", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetLocal("user", "ann")
			ctx.DeleteLocal("user")

			v, ok := ctx.GetLocal[string]("user")
			require.False(t, ok)
			require.Equal(t, "", v)

			require.NotPanics(t, func() { ctx.DeleteLocal("user") })
			v, ok = ctx.GetLocal[string]("user")
			require.False(t, ok)
			require.Equal(t, "", v)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("per request isolation", func(t *testing.T) {
		var seen []bool

		app := New()
		app.Use("/", func(ctx *Context) error {
			_, ok := ctx.GetLocal[string]("user")
			seen = append(seen, ok)
			ctx.SetLocal("user", "ann")
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, []bool{false, false}, seen)
	})

	t.Run("passLocalsToContext false hides from Value", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetLocal("k", "v")
			require.Nil(t, ctx.Value("k"))
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("passLocalsToContext true mirrors and delete shadows nil", func(t *testing.T) {
		app := New(WithPassLocalsToContext(true))
		app.Use("/", func(ctx *Context) error {
			ctx.SetLocal("k", "v")
			require.Equal(t, "v", ctx.Value("k"))

			ctx.DeleteLocal("k")
			_, ok := ctx.GetLocal[string]("k")
			require.False(t, ok)
			require.Nil(t, ctx.Value("k"))
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})
}

func TestContext_State(t *testing.T) {
	t.Run("set get hit", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetState("count", 42)

			v, ok := ctx.GetState[int]("count")
			require.True(t, ok)
			require.Equal(t, 42, v)

			a, ok := ctx.GetAnyState("count")
			require.True(t, ok)
			require.Equal(t, 42, a)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("wrong type miss", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetState("count", 42)

			v, ok := ctx.GetState[string]("count")
			require.False(t, ok)
			require.Equal(t, "", v)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("missing miss", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			v, ok := ctx.GetState[int]("nope")
			require.False(t, ok)
			require.Equal(t, 0, v)

			anyV, anyOK := ctx.GetAnyState("nope")
			require.False(t, anyOK)
			require.Nil(t, anyV)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("delete", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			ctx.SetState("count", 42)
			ctx.DeleteState("count")

			v, ok := ctx.GetState[int]("count")
			require.False(t, ok)
			require.Equal(t, 0, v)
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})

	t.Run("shared across contexts", func(t *testing.T) {
		var seen []string

		app := New()
		app.Use("/", func(ctx *Context) error {
			if len(seen) == 0 {
				ctx.SetState("shared", "s1")
			} else {
				v, ok := ctx.GetState[string]("shared")
				require.True(t, ok)
				seen = append(seen, v)

				anyV, anyOK := ctx.GetAnyState("shared")
				require.True(t, anyOK)
				require.Equal(t, "s1", anyV)
				return nil
			}
			seen = append(seen, "set")
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, []string{"set", "s1"}, seen)
	})
}

func TestContext_IsFinal(t *testing.T) {
	t.Run("first false last true", func(t *testing.T) {
		var finals []bool

		app := New()
		app.Map(http.MethodGet, "/chain",
			func(ctx *Context) error {
				finals = append(finals, ctx.IsFinal())
				require.NoError(t, ctx.Next())
				finals = append(finals, ctx.IsFinal())
				return nil
			},
			func(ctx *Context) error {
				finals = append(finals, ctx.IsFinal())
				require.NoError(t, ctx.Next())
				finals = append(finals, ctx.IsFinal())
				return nil
			},
		)

		app.Test(httptest.NewRequest(http.MethodGet, "/chain", nil))
		require.Equal(t, []bool{false, true, true, true}, finals)
	})

	t.Run("single handler is final Next returns nil", func(t *testing.T) {
		app := New()
		app.Use("/", func(ctx *Context) error {
			require.True(t, ctx.IsFinal())
			require.NoError(t, ctx.Next())
			require.True(t, ctx.IsFinal())
			require.NoError(t, ctx.Next())
			require.True(t, ctx.IsFinal())
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	})
}
