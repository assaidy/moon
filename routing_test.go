package moon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouter_ParsePattern(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		valid := []string{
			"/",
			"/api",
			"/api/v1/users",
			"/file.json",
			"/:id",
			"/:name.json",
			"/users/:id",
			"/user/:name?",
			"/a/:x?/b",
			"/*",
			"/api/*",
			"/+",
			"/user/+",
			"/v1/*/shop/*",
			"/files/*.json",
			"/a\\\\b",
			`/v1/some/resource/name\:customVerb`,
			`/\*`,
			"/*v1*/proxy",
			"/api/:id/x*",
		}
		for _, pattern := range valid {
			_, _, err := ParsePattern(pattern)
			require.NoError(t, err, "pattern: %s", pattern)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		invalid := []string{
			"",
			"api",
			"/api/",
			"/api//users",
			"/:",
			"/:?",
			"/:sign:param",
			"/:a:b",
			"/:a?:b",
			"/:from-:to",
			"/:genus.:species",
			"/api-:name",
			"/shop/product/color::color",
			"/*:id",
			"/:id*",
			"/*+",
			"/**",
			"/user/*?",
			"/user/+?",
			"/trailing\\",
			"/\\q",
			"/hello world",
			"/users?",
			"/api/:id/x y",
		}
		for _, pattern := range invalid {
			_, _, err := ParsePattern(pattern)
			require.Error(t, err, "pattern: %s", pattern)
		}
	})

	t.Run("structure", func(t *testing.T) {
		segs, names, err := ParsePattern(`/v1/some/resource/name\:customVerb`)
		require.NoError(t, err)
		require.Len(t, segs, 4)
		require.Equal(t, Segment{
			{Kind: PartLiteral, Text: "name"},
			{Kind: PartLiteral, Text: ":"},
			{Kind: PartLiteral, Text: "customVerb"},
		}, segs[3])

		segs, _, err = ParsePattern(`/\*`)
		require.NoError(t, err)
		require.Len(t, segs, 1)
		require.Equal(t, Segment{{Kind: PartLiteral, Text: "*"}}, segs[0])

		segs, _, err = ParsePattern("/*v1*/proxy")
		require.NoError(t, err)
		require.Len(t, segs, 2)
		require.Equal(t, Segment{
			{Kind: PartWildcard, Text: "*"},
			{Kind: PartLiteral, Text: "v1"},
			{Kind: PartWildcard, Text: "*"},
		}, segs[0])

		segs, names, err = ParsePattern("/:version-id")
		require.NoError(t, err)
		require.Equal(t, []string{"version-id"}, names)
		require.Equal(t, Segment{
			{Kind: PartParam, Text: "version-id"},
		}, segs[0])

		segs, _, err = ParsePattern("/files/*.json")
		require.NoError(t, err)
		require.Len(t, segs, 2)
		require.Equal(t, Segment{
			{Kind: PartWildcard, Text: "*"},
			{Kind: PartLiteral, Text: ".json"},
		}, segs[1])
	})

	t.Run("duplicate names", func(t *testing.T) {
		_, names, err := ParsePattern("/api/:id/posts/:id")
		require.NoError(t, err)
		require.False(t, areNamesUnique(names))
	})
}

func TestRouter_MapPanics(t *testing.T) {
	dummy := func(ctx *Context) error { return nil }

	t.Run("invalid method", func(t *testing.T) {
		app := New()
		require.Panics(t, func() { app.Map("GETX", "/api", dummy) })
	})

	t.Run("invalid pattern", func(t *testing.T) {
		app := New()
		require.Panics(t, func() { app.Map(http.MethodGet, "/api/", dummy) })
	})

	t.Run("duplicate param names", func(t *testing.T) {
		app := New()
		require.Panics(t, func() { app.Map(http.MethodGet, "/api/:id/api/:id", dummy) })
	})

	t.Run("nil handler", func(t *testing.T) {
		app := New()
		require.Panics(t, func() { app.Map(http.MethodGet, "/api", nil) })
		require.Panics(t, func() { app.MapAll("/api", nil) })
		require.Panics(t, func() { app.MapGet("/api", nil) })
	})

	t.Run("nil route middleware", func(t *testing.T) {
		app := New()
		require.Panics(t, func() {
			app.Map(http.MethodGet, "/api", dummy, WithMiddlewares(nil))
		})
	})
}

// Each method shortcut must register exactly its own method via Map.
func TestRouter_MethodShortcuts(t *testing.T) {
	testCases := []struct {
		name     string
		register func(app *App, pattern string, handler Handler)
		method   string
	}{
		{"MapGet", func(app *App, p string, h Handler) { app.MapGet(p, h) }, http.MethodGet},
		{"MapHead", func(app *App, p string, h Handler) { app.MapHead(p, h) }, http.MethodHead},
		{"MapPost", func(app *App, p string, h Handler) { app.MapPost(p, h) }, http.MethodPost},
		{"MapPut", func(app *App, p string, h Handler) { app.MapPut(p, h) }, http.MethodPut},
		{"MapPatch", func(app *App, p string, h Handler) { app.MapPatch(p, h) }, http.MethodPatch},
		{"MapDelete", func(app *App, p string, h Handler) { app.MapDelete(p, h) }, http.MethodDelete},
		{"MapConnect", func(app *App, p string, h Handler) { app.MapConnect(p, h) }, http.MethodConnect},
		{"MapOptions", func(app *App, p string, h Handler) { app.MapOptions(p, h) }, http.MethodOptions},
		{"MapTrace", func(app *App, p string, h Handler) { app.MapTrace(p, h) }, http.MethodTrace},
		{"MapQuery", func(app *App, p string, h Handler) { app.MapQuery(p, h) }, MethodQuery},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := New()
			tc.register(app, "/api/users", func(ctx *Context) error {
				return ctx.Write(http.StatusOK, "ok")
			})

			resp := app.Test(httptest.NewRequest(tc.method, "/api/users", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)

			// an unregistered method on the same pattern is not routed
			other := http.MethodGet
			if tc.method == http.MethodGet {
				other = http.MethodPost
			}
			resp = app.Test(httptest.NewRequest(other, "/api/users", nil))
			require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		})
	}
}

// MapAll registers the handlers for every valid HTTP method.
func TestRouter_MapAll(t *testing.T) {
	app := New()
	app.MapAll("/api/users", func(ctx *Context) error {
		return ctx.Write(http.StatusOK, "ok")
	})

	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace, MethodQuery,
	}
	for _, method := range methods {
		resp := app.Test(httptest.NewRequest(method, "/api/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode, method)
	}
}

// UseAll registers the middleware for every path, like Use("/").
func TestRouter_UseAll(t *testing.T) {
	app := New()

	var called int
	app.UseAll(func(ctx *Context) error {
		called++
		return ctx.Next()
	})

	app.MapGet("/", func(ctx *Context) error {
		return ctx.Write(http.StatusOK, "ok")
	})
	app.MapGet("/api/users", func(ctx *Context) error {
		return ctx.Write(http.StatusOK, "ok")
	})

	// runs for matched paths and for unmatched ones too
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/", http.StatusOK},
		{"/api/users", http.StatusOK},
		{"/nope", http.StatusNotFound},
	} {
		resp := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.status, resp.StatusCode, tc.path)
	}
	require.Equal(t, 3, called)

	// UseAll with no handlers does nothing, like Use.
	require.NotPanics(t, func() { app.UseAll() })
}

func TestRouter_UsePanics(t *testing.T) {
	dummy := func(ctx *Context) error { return nil }

	invalidPrefixes := []string{
		"",
		"api",
		"/api/",
		"/api//users",
		"/:",
		"/:?",
		"/api/:a b",
	}

	for _, prefix := range invalidPrefixes {
		t.Run("invalid pattern "+prefix, func(t *testing.T) {
			app := New()
			require.Panics(t, func() { app.Use(prefix, dummy) })
		})
	}

	t.Run("nil middleware", func(t *testing.T) {
		app := New()
		require.Panics(t, func() { app.Use("/", nil) })
		require.Panics(t, func() { app.UseAll(nil) })
	})
}

func TestRouter_IsValidHttpMethod(t *testing.T) {
	var testCases = []struct {
		input    string
		expected bool
	}{
		// valid
		{"GET", true},
		{"HEAD", true},
		{"POST", true},
		{"PUT", true},
		{"PATCH", true},
		{"DELETE", true},
		{"CONNECT", true},
		{"OPTIONS", true},
		{"TRACE", true},
		{"QUERY", true},
		// invalid
		{"", false},
		{"INVALID", false},
		{"get", false},
		{"Get", false},
		{"POST ", false},
		{" POST", false},
		{"  GET  ", false},
		{"GET\n", false},
		{"GET\t", false},
		{"GET/POST", false},
		{"GET POST", false},
		{"OPTIONS!", false},
		{"CONNECT_", false},
	}

	for _, tc := range testCases {
		require.Equal(t, tc.expected, IsValidHttpMethod(tc.input))
	}
}

func TestRouter_MatchPath(t *testing.T) {
	var testCases = []struct {
		name       string
		pattern    string
		path       string
		wantParams map[string]string
		wantMatch  bool
	}{
		{name: "static match", pattern: "/users", path: "/users", wantParams: map[string]string{}, wantMatch: true},
		{name: "static multi-segment match", pattern: "/api/users", path: "/api/users", wantParams: map[string]string{}, wantMatch: true},
		{name: "single parameter", pattern: "/users/:id", path: "/users/123", wantParams: map[string]string{"id": "123"}, wantMatch: true},
		{name: "parameter with string value", pattern: "/users/:id", path: "/users/abc", wantParams: map[string]string{"id": "abc"}, wantMatch: true},
		{name: "parameter stops at slash", pattern: "/users/:id", path: "/users/123/profile", wantParams: nil, wantMatch: false},
		{name: "multiple parameters", pattern: "/users/:user_id/posts/:post_id", path: "/users/123/posts/456", wantParams: map[string]string{"user_id": "123", "post_id": "456"}, wantMatch: true},
		{name: "multiple parameters with strings", pattern: "/:category/:slug", path: "/books/go-programming", wantParams: map[string]string{"category": "books", "slug": "go-programming"}, wantMatch: true},
		{name: "different static segment", pattern: "/users", path: "/posts", wantParams: nil, wantMatch: false},
		{name: "different segment with parameter route", pattern: "/users/:id", path: "/posts/123", wantParams: nil, wantMatch: false},
		{name: "missing segment", pattern: "/users/:id", path: "/users", wantParams: nil, wantMatch: false},
		{name: "extra segment", pattern: "/users/:id", path: "/users/123/profile", wantParams: nil, wantMatch: false},
		{name: "prefix is not enough", pattern: "/users", path: "/users/123", wantParams: nil, wantMatch: false},
		{name: "missing leading slash does not match", pattern: "/users", path: "prefix/users", wantParams: nil, wantMatch: false},
		{name: "optional present", pattern: "/user/:name?", path: "/user/bob", wantParams: map[string]string{"name": "bob"}, wantMatch: true},
		{name: "optional missing", pattern: "/user/:name?", path: "/user", wantParams: map[string]string{"name": ""}, wantMatch: true},
		{name: "optional with extra segment", pattern: "/user/:name?", path: "/user/a/b", wantParams: nil, wantMatch: false},
		{name: "optional in the middle skipped", pattern: "/a/:x?/b", path: "/a/b", wantParams: map[string]string{"x": ""}, wantMatch: true},
		{name: "optional in the middle taken", pattern: "/a/:x?/b", path: "/a/c/b", wantParams: map[string]string{"x": "c"}, wantMatch: true},
		{name: "standalone wildcard", pattern: "/files/*", path: "/files/a/b/c.txt", wantParams: map[string]string{"*": "a/b/c.txt"}, wantMatch: true},
		{name: "wildcard matches empty", pattern: "/files/*", path: "/files", wantParams: map[string]string{"*": ""}, wantMatch: true},
		{name: "wildcard mismatch", pattern: "/files/*/123", path: "/files/a/b", wantParams: nil, wantMatch: false},
		{name: "wildcard in the middle", pattern: "/v1/*/shop", path: "/v1/a/b/shop", wantParams: map[string]string{"*": "a/b"}, wantMatch: true},
		{name: "wildcard in the middle mismatch", pattern: "/v1/*/shop", path: "/v1/a/b/store", wantParams: nil, wantMatch: false},
		{name: "multiple wildcards", pattern: "/v1/*/shop/*", path: "/v1/a/b/shop/c/d", wantParams: map[string]string{"*1": "a/b", "*2": "c/d"}, wantMatch: true},
		{name: "plus needs content", pattern: "/user/+", path: "/user/a/b", wantParams: map[string]string{"+": "a/b"}, wantMatch: true},
		{name: "plus rejects empty", pattern: "/user/+", path: "/user", wantParams: nil, wantMatch: false},
		{name: "wildcard with literal suffix", pattern: "/files/*.json", path: "/files/a/b.json", wantParams: map[string]string{"*": "a/b"}, wantMatch: true},
		{name: "wildcard with literal suffix mismatch", pattern: "/files/*.json", path: "/files/a.txt", wantParams: nil, wantMatch: false},
		{name: "wildcard after literal", pattern: "/x*", path: "/xabc", wantParams: map[string]string{"*": "abc"}, wantMatch: true},
		{name: "escaped star is literal", pattern: `/\*`, path: "/*", wantParams: map[string]string{}, wantMatch: true},
		{name: "escaped colon is literal", pattern: `/v1/some/resource/name\:customVerb`, path: "/v1/some/resource/name:customVerb", wantParams: map[string]string{}, wantMatch: true},
		{name: "root match", pattern: "/", path: "/", wantParams: map[string]string{}, wantMatch: true},
		{name: "root does not match extra path", pattern: "/", path: "/users", wantParams: nil, wantMatch: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			segs, names, err := ParsePattern(tc.pattern)
			require.NoError(t, err)
			route := routeEntry{pattern: tc.pattern, segments: segs, names: names}
			gotParams, gotMatch := route.matchPath(tc.path)
			require.Equal(t, tc.wantMatch, gotMatch)
			require.Equal(t, tc.wantParams, gotParams)
		})
	}
}

func TestRouter_MiddlewareMatchPath(t *testing.T) {
	var testCases = []struct {
		name   string
		prefix string
		path   string
		want   bool
	}{
		{name: "root matches everything", prefix: "/", path: "/anything/at/all", want: true},
		{name: "exact prefix", prefix: "/api", path: "/api", want: true},
		{name: "prefix with sub path", prefix: "/api", path: "/api/users", want: true},
		{name: "prefix with deep sub path", prefix: "/api", path: "/api/users/42", want: true},
		{name: "no slash boundary", prefix: "/api", path: "/apiv2", want: false},
		{name: "different prefix", prefix: "/api", path: "/other", want: false},
		{name: "prefix longer than path", prefix: "/api", path: "/", want: false},
		{name: "param prefix", prefix: "/users/:id", path: "/users/42", want: true},
		{name: "param prefix with sub path", prefix: "/users/:id", path: "/users/42/posts", want: true},
		{name: "param prefix missing value", prefix: "/users/:id", path: "/users", want: false},
		{name: "optional prefix", prefix: "/a/:x?/b", path: "/a/b/c", want: true},
		{name: "optional prefix taken", prefix: "/a/:x?/b", path: "/a/c/b/d", want: true},
		{name: "optional prefix mismatch", prefix: "/a/:x?/b", path: "/a/c/d", want: false},
		{name: "wildcard prefix", prefix: "/api/*", path: "/api/x/y", want: true},
		{name: "wildcard prefix exact", prefix: "/api/*", path: "/api", want: true},
		{name: "wildcard prefix mismatch", prefix: "/api/*", path: "/other", want: false},
		{name: "plus prefix", prefix: "/u/+", path: "/u/a", want: true},
		{name: "plus prefix empty", prefix: "/u/+", path: "/u", want: false},
		{name: "mixed prefix", prefix: "/files/*.json", path: "/files/a.json/x", want: true},
		{name: "mixed prefix mismatch", prefix: "/files/*.json", path: "/files/a.txt", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			segs, _, err := ParsePattern(tc.prefix)
			require.NoError(t, err)
			mw := middlewareEntry{prefix: tc.prefix, segments: segs}
			require.Equal(t, tc.want, mw.matchPath(tc.path))
		})
	}
}

// Route and middleware matching ignore letter case: App.Map and App.Use
// lowercase their patterns at registration, and the request path is
// lowercased before dispatch, so "/API/Users" and "/api/users" are one route.
func TestRouter_CaseInsensitivePaths(t *testing.T) {
	t.Run("request path case is ignored", func(t *testing.T) {
		app := New()
		app.MapGet("/api/users", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "route")
		})

		for _, path := range []string{
			"/api/users",
			"/API/USERS",
			"/Api/Users",
			"/api/Users",
			"/API/users",
		} {
			resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
		}
	})

	t.Run("registered pattern case is ignored", func(t *testing.T) {
		app := New()
		app.MapGet("/API/Users", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "route")
		})

		for _, path := range []string{"/api/users", "/Api/UsErS", "/API/USERS"} {
			resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
		}
	})

	t.Run("root and trailing slash still match", func(t *testing.T) {
		app := New()
		app.MapGet("/", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "root")
		})
		app.MapGet("/users", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "users")
		})

		for _, path := range []string{"/", "/USERS", "/Users/"} {
			resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
		}
	})

	t.Run("parameter route matches case-insensitively", func(t *testing.T) {
		app := New()
		var gotPath, gotParam string
		app.MapGet("/USERS/:id", func(ctx *Context) error {
			gotPath = ctx.GetPath()
			gotParam = ctx.GetParam("id")
			return ctx.Write(http.StatusOK, "route")
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/users/AbC", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// the whole request path is lowercased before dispatch,
		// so captured values are lowercase too
		require.Equal(t, "/users/abc", gotPath)
		require.Equal(t, "abc", gotParam)
	})

	t.Run("wildcard route matches case-insensitively", func(t *testing.T) {
		app := New()
		app.MapGet("/FILES/*", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "route")
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/a/B.txt", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("middleware pattern case is ignored", func(t *testing.T) {
		testCases := []struct {
			name    string
			pattern string
			path    string
		}{
			{"uppercase pattern", "/API/*", "/api/users"},
			{"uppercase path", "/api/*", "/API/USERS"},
			{"mixed case on both sides", "/Api/*", "/aPi/UsErS"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				app := New()
				var ran bool
				app.Use(tc.pattern, func(ctx *Context) error {
					ran = true
					return ctx.Next()
				})
				app.MapGet("/api/users", func(ctx *Context) error {
					return ctx.Write(http.StatusOK, "route")
				})

				resp := app.Test(httptest.NewRequest(http.MethodGet, tc.path, nil))
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.True(t, ran, "middleware %q should run for %q", tc.pattern, tc.path)
			})
		}
	})

	t.Run("case variant of a known path keeps 405 and 404", func(t *testing.T) {
		app := New()
		app.MapGet("/users", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "route")
		})

		resp := app.Test(httptest.NewRequest(http.MethodPost, "/USERS", nil))
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

		resp = app.Test(httptest.NewRequest(http.MethodGet, "/NOPE", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("route patterns are lowercased too", func(t *testing.T) {
		app := New()
		app.MapGet("/API/Users", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "route")
		})

		for _, path := range []string{"/api/users", "/API/USERS", "/Api/Users"} {
			resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
		}
	})
}

func TestRouter_Dispatch(t *testing.T) {
	var newHandler = func(name string, called *[]string) Handler {
		return func(ctx *Context) error {
			*called = append(*called, name)
			if ctx.IsMiddleware() {
				return ctx.Next()
			}
			return nil
		}
	}

	t.Run("Use() is order-sensitive", func(t *testing.T) {
		t.Run("use before handle", func(t *testing.T) {
			app := New()
			var called []string

			app.Use("/api/*", newHandler("middleware", &called))
			app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users/123", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, []string{"middleware", "handler"}, called)
		})

		t.Run("use after handle", func(t *testing.T) {
			app := New()
			var called []string

			app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))
			app.Use("/api/*", newHandler("middleware", &called))

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users/123", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, []string{"handler"}, called)
		})
	})

	t.Run("nested patterns", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api/*", newHandler("api", &called))
		app.Use("/api/users/*", newHandler("users", &called))
		app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users/123", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"api", "users", "handler"}, called)
	})

	t.Run("use merges parent patterns", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api/*", newHandler("api", &called))
		app.Use("/api/v1/*", newHandler("v1", &called))
		app.Use("/api/v1/admin/*", newHandler("admin", &called))
		app.Map(http.MethodGet, "/api/v1/admin/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"api", "v1", "admin", "handler"}, called)
	})

	t.Run("middleware applies to each method", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api/users", newHandler("middleware", &called))
		app.Map(http.MethodGet, "/api/users", newHandler("get", &called))
		app.Map(http.MethodPost, "/api/users", newHandler("post", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "get"}, called)

		called = nil

		resp = app.Test(httptest.NewRequest(http.MethodPost, "/api/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "post"}, called)
	})

	t.Run("prefix matching", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api", newHandler("middleware", &called))
		app.Map(http.MethodGet, "/api", newHandler("api", &called))
		app.Map(http.MethodGet, "/api/users", newHandler("users", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "api"}, called)

		called = nil
		resp = app.Test(httptest.NewRequest(http.MethodGet, "/api/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "users"}, called)

		called = nil
		resp = app.Test(httptest.NewRequest(http.MethodGet, "/apiv2", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Empty(t, called)
	})

	t.Run("catch-all runs for every endpoint", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/*", newHandler("middleware", &called))
		app.Map(http.MethodGet, "/api", newHandler("api", &called))
		app.Map(http.MethodGet, "/other", newHandler("other", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "api"}, called)

		called = nil
		resp = app.Test(httptest.NewRequest(http.MethodGet, "/other", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "other"}, called)
	})

	t.Run("subsequent patterns inherit parent middleware", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/x/*", newHandler("x", &called))
		app.Use("/x/y/*", newHandler("x/y", &called))
		app.Map(http.MethodGet, "/x/y/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x/y/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"x", "x/y", "handler"}, called)
	})

	t.Run("middleware runs without an endpoint", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/*", newHandler("middleware", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Equal(t, []string{"middleware"}, called)
	})

	t.Run("middlewares run on unmatched paths", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/*", newHandler("middleware", &called))
		app.Map(http.MethodGet, "/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Equal(t, []string{"middleware"}, called)

		called = nil
		resp = app.Test(httptest.NewRequest(http.MethodPost, "/users", nil))
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		require.Equal(t, []string{"middleware"}, called)
	})

	t.Run("middleware runs when no route matches", func(t *testing.T) {
		app := New()
		var ran bool

		app.Use("/api/*", func(ctx *Context) error {
			ran = true
			return ctx.Next()
		})
		app.Map(http.MethodGet, "/other", func(ctx *Context) error { return nil })

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/missing", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.True(t, ran)
	})

	t.Run("unmatched with no middleware", func(t *testing.T) {
		var ran bool
		app := New(WithErrorHandler(func(ctx *Context, err error) error {
			ran = true
			ctx.WriteStatus(http.StatusNotFound)
			return err
		}))
		app.Map(http.MethodGet, "/users", func(ctx *Context) error { return nil })

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.True(t, ran)
	})

	t.Run("middleware can short-circuit without a route", func(t *testing.T) {
		app := New()

		app.Use("/*", func(ctx *Context) error {
			return ctx.Write(http.StatusOK, "from-middleware")
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "from-middleware", string(raw))
	})

	t.Run("wildcard matches", func(t *testing.T) {
		app := New()
		var called []string

		app.Map(http.MethodGet, "/files/*", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/files/a/b/c.txt", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"handler"}, called)

		resp = app.Test(httptest.NewRequest(http.MethodGet, "/other/a/b", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("trailing slash is trimmed", func(t *testing.T) {
		app := New()
		var called []string

		app.Map(http.MethodGet, "/", newHandler("root", &called))
		app.Map(http.MethodGet, "/users", newHandler("users", &called))

		for _, path := range []string{"/", "/users", "/users/"} {
			resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
			require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
		}

		require.Equal(t, []string{"root", "users", "users"}, called)
	})

	t.Run("path case is ignored", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api/*", newHandler("middleware", &called))
		app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/API/Users/123", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "handler"}, called)
	})

	t.Run("unknown path returns 404 not_found", func(t *testing.T) {
		app := New()
		var called []string
		app.Map(http.MethodGet, "/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
		require.Equal(t, ErrNotFound.StatusCode, resp.StatusCode)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		var body struct {
			Kind string `json:"kind"`
		}
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &body))
		require.Equal(t, ErrNotFound.Kind, body.Kind)
	})

	t.Run("wrong method returns 405 method_not_allowed", func(t *testing.T) {
		app := New()
		var called []string
		app.Map(http.MethodGet, "/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodPost, "/users", nil))
		require.Equal(t, ErrMethodNotAllowed.StatusCode, resp.StatusCode)
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

		var body struct {
			Kind string `json:"kind"`
		}
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &body))
		require.Equal(t, ErrMethodNotAllowed.Kind, body.Kind)
	})

	t.Run("unknown path reaches custom error handler as not_found", func(t *testing.T) {
		var captured error

		app := New(WithErrorHandler(func(ctx *Context, err error) error {
			captured = err
			ctx.WriteAs(http.StatusNotFound, CodecJson, ErrNotFound)
			return err
		}))
		app.Map(http.MethodGet, "/users", newHandler("handler", &[]string{}))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.ErrorIs(t, captured, ErrNotFound)
	})

	t.Run("wrong method reaches custom error handler as method_not_allowed", func(t *testing.T) {
		var captured error

		app := New(WithErrorHandler(func(ctx *Context, err error) error {
			captured = err
			ctx.WriteAs(http.StatusMethodNotAllowed, CodecJson, ErrMethodNotAllowed)
			return err
		}))
		app.Map(http.MethodGet, "/users", newHandler("handler", &[]string{}))

		resp := app.Test(httptest.NewRequest(http.MethodPost, "/users", nil))
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		require.ErrorIs(t, captured, ErrMethodNotAllowed)
	})

	t.Run("middleware Next chain and error bubbling to errorHandler", func(t *testing.T) {
		sentinel := errors.New("boom")
		var order []string
		var captured error

		app := New(WithErrorHandler(func(ctx *Context, err error) error {
			captured = err
			ctx.WriteStatus(http.StatusTeapot)
			return err
		}))

		app.Use("/*", func(ctx *Context) error {
			order = append(order, "mw1-before")
			err := ctx.Next()
			order = append(order, "mw1-after")
			return err
		})
		app.Use("/x/*", func(ctx *Context) error {
			order = append(order, "mw2")
			return ctx.Next()
		})
		app.Map(http.MethodGet, "/x/y", func(ctx *Context) error {
			order = append(order, "handler")
			return sentinel
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x/y", nil))
		require.Equal(t, http.StatusTeapot, resp.StatusCode)
		require.Equal(t, sentinel, captured)
		require.Equal(t, []string{"mw1-before", "mw2", "handler", "mw1-after"}, order)
	})
}

func TestRouter_RouteOptions(t *testing.T) {
	t.Run("route middlewares run after app middlewares and before handler", func(t *testing.T) {
		var order []string

		app := New()
		app.Use("/*", func(ctx *Context) error {
			order = append(order, "app")
			return ctx.Next()
		})
		app.Map(http.MethodGet, "/x",
			func(ctx *Context) error {
				order = append(order, "handler")
				return nil
			},
			WithMiddlewares(
				func(ctx *Context) error {
					order = append(order, "route-1")
					return ctx.Next()
				},
				func(ctx *Context) error {
					order = append(order, "route-2")
					return ctx.Next()
				},
			),
		)

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"app", "route-1", "route-2", "handler"}, order)
	})

	t.Run("route middlewares only run for their route", func(t *testing.T) {
		var called []string

		app := New()
		routeMw := func(ctx *Context) error {
			called = append(called, "route")
			return ctx.Next()
		}
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			called = append(called, "x")
			return nil
		}, WithMiddlewares(routeMw))
		app.Map(http.MethodGet, "/y", func(ctx *Context) error {
			called = append(called, "y")
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/y", nil))
		require.Equal(t, []string{"y"}, called)
	})

	t.Run("route id is visible in handler and middlewares", func(t *testing.T) {
		var inMw, inHandler string

		app := New()
		app.Map(http.MethodGet, "/x",
			func(ctx *Context) error {
				inHandler = ctx.GetRouteId()
				return nil
			},
			WithMiddlewares(func(ctx *Context) error {
				inMw = ctx.GetRouteId()
				return ctx.Next()
			}),
			WithId("get_x"),
		)

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, "get_x", inMw)
		require.Equal(t, "get_x", inHandler)
	})

	t.Run("route id is stored as given", func(t *testing.T) {
		for _, id := range []string{"", "  get_x  ", " "} {
			var got string

			app := New()
			app.MapGet("/x", func(ctx *Context) error {
				got = ctx.GetRouteId()
				return nil
			}, WithId(id))

			app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
			require.Equal(t, id, got)
		}
	})

	t.Run("route id is empty when unmatched or method not allowed", func(t *testing.T) {
		var ids []string

		app := New()
		app.Use("/*", func(ctx *Context) error {
			ids = append(ids, ctx.GetRouteId())
			return ctx.Next()
		})
		app.MapGet("/x", func(ctx *Context) error { return nil }, WithId("get_x"))

		for _, tc := range []struct {
			method string
			path   string
		}{
			{http.MethodGet, "/missing"},
			{http.MethodPost, "/x"},
		} {
			app.Test(httptest.NewRequest(tc.method, tc.path, nil))
		}

		require.Equal(t, []string{"", ""}, ids)
	})

	t.Run("route id is empty by default", func(t *testing.T) {
		var got = "unset"

		app := New()
		app.MapGet("/x", func(ctx *Context) error {
			got = ctx.GetRouteId()
			return nil
		})

		app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, "", got)
	})

	t.Run("MapAll applies the same options to every method", func(t *testing.T) {
		var ids []string

		app := New()
		app.MapAll("/x", func(ctx *Context) error {
			ids = append(ids, ctx.GetRouteId())
			return nil
		}, WithId("all_x"))

		for _, method := range []string{http.MethodGet, http.MethodPost, MethodQuery} {
			app.Test(httptest.NewRequest(method, "/x", nil))
		}
		require.Equal(t, []string{"all_x", "all_x", "all_x"}, ids)
	})
}

type captureLogHandler struct {
	records []slog.Record
}

func (h *captureLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureLogHandler) WithGroup(string) slog.Handler      { return h }

func loggedStatus(t *testing.T, h *captureLogHandler) any {
	t.Helper()
	require.Len(t, h.records, 1)
	var status any
	h.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "status" {
			status = a.Value.Any()
			return false
		}
		return true
	})
	return status
}

func TestRouter_RequestLoggingStatus(t *testing.T) {
	t.Run("defaults to 200 when nothing written", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(WithLogger(slog.New(logs)), WithRequestLogging(true))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error { return nil })

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, int64(http.StatusOK), loggedStatus(t, logs))
	})

	t.Run("logs written status", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(WithLogger(slog.New(logs)), WithRequestLogging(true))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			return ctx.Write(http.StatusCreated, "hello")
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		require.Equal(t, int64(http.StatusCreated), loggedStatus(t, logs))
	})
}
