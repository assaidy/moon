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

func TestRouter_IsValidMiddlewareRoutePrefix(t *testing.T) {
	var testCases = []struct {
		name   string
		prefix string
		want   bool
	}{
		// ============================================================
		// Empty and root prefixes
		// ============================================================
		{name: "root", prefix: "/", want: true},
		{name: "empty string", prefix: "", want: false},
		// ============================================================
		// Literal segments
		// ============================================================
		{name: "single literal", prefix: "/api", want: true},
		{name: "multiple literal segments", prefix: "/api/v1/users", want: true},
		{name: "digits", prefix: "/123", want: true},
		{name: "mixed letters and digits", prefix: "/api123", want: true},
		{name: "underscore", prefix: "/user_id", want: true},
		{name: "hyphen", prefix: "/user-id", want: true},
		{name: "underscore and hyphen", prefix: "/user_id-123", want: true},
		{name: "single character", prefix: "/a", want: true},
		{name: "multiple segments with special allowed chars", prefix: "/api/v1/user_id/profile-name", want: true},
		// ============================================================
		// Empty segments and repeated slashes
		// ============================================================
		{name: "double slash at beginning", prefix: "//xyz", want: false},
		{name: "double slash in middle", prefix: "/api//users", want: false},
		{name: "double slash at end", prefix: "/api//", want: false},
		{name: "trailing slash", prefix: "/api/", want: false},
		{name: "multiple consecutive slashes", prefix: "///", want: false},
		{name: "slash between every character", prefix: "/a//b//c", want: false},
		// ============================================================
		// Route pattern syntax is not allowed
		// ============================================================
		{name: "parameter", prefix: "/:id", want: false},
		{name: "parameter with underscore", prefix: "/:user_id", want: false},
		{name: "parameter in middle of route", prefix: "/users/:id", want: false},
		{name: "wildcard", prefix: "/*", want: false},
		{name: "wildcard after literal", prefix: "/api/*", want: false},
		{name: "wildcard inside literal", prefix: "/us*er", want: false},
		{name: "multiple wildcards", prefix: "/a*b*c", want: false},
		// ============================================================
		// Invalid characters
		// ============================================================
		{name: "space", prefix: "/hello world", want: false},
		{name: "dot", prefix: "/api.users", want: false},
		{name: "question mark", prefix: "/users?", want: false},
		{name: "hash", prefix: "/users#section", want: false},
		{name: "percent", prefix: "/users%20", want: false},
		{name: "at sign", prefix: "/users@home", want: false},
		{name: "plus sign", prefix: "/users+", want: false},
		{name: "equals sign", prefix: "/users=id", want: false},
		{name: "comma", prefix: "/users,id", want: false},
		{name: "semicolon", prefix: "/users;id", want: false},
		{name: "backslash", prefix: `/users\id`, want: false},
		// ============================================================
		// Unicode characters
		// ============================================================
		{name: "Arabic letters", prefix: "/مستخدم", want: false},
		{name: "Chinese letters", prefix: "/用户", want: false},
		{name: "emoji", prefix: "/🚀", want: false},
		// ============================================================
		// Anchoring and malformed paths
		// ============================================================
		{name: "missing leading slash", prefix: "api/users", want: false},
		{name: "leading whitespace", prefix: " /api", want: false},
		{name: "trailing whitespace", prefix: "/api ", want: false},
		{name: "newline at end", prefix: "/api\n", want: false},
		{name: "newline at beginning", prefix: "\n/api", want: false},
		{name: "query string", prefix: "/api?foo=bar", want: false},
		{name: "fragment", prefix: "/api#users", want: false},
		{name: "full URL", prefix: "https://example.com/api", want: false},
		{name: "leading double slash URL-like", prefix: "//example.com/api", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isValidMiddlewareRoutePrefix(tc.prefix))
		})
	}
}

func TestRouter_IsValidRoutePattern(t *testing.T) {
	var testCases = []struct {
		name  string
		route string
		want  bool
	}{
		// ============================================================
		// Empty and root routes
		// ============================================================
		{name: "root", route: "/", want: true},
		{name: "empty string", route: "", want: false},
		// ============================================================
		// Literal segments
		// ============================================================
		{name: "single literal", route: "/api", want: true},
		{name: "multiple literal segments", route: "/api/v1/users", want: true},
		{name: "digits", route: "/123", want: true},
		{name: "mixed letters and digits", route: "/api123", want: true},
		{name: "underscore", route: "/user_id", want: true},
		{name: "hyphen", route: "/user-id", want: true},
		{name: "underscore and hyphen", route: "/user_id-123", want: true},
		{name: "single character", route: "/a", want: true},
		{name: "multiple segments with special allowed chars", route: "/api/v1/user_id/profile-name", want: true},
		// ============================================================
		// Parameter segments
		// ============================================================
		{name: "single parameter", route: "/:id", want: true},
		{name: "parameter with underscore", route: "/:user_id", want: true},
		{name: "parameter with hyphen", route: "/:user-id", want: true},
		{name: "parameter with digits", route: "/:user123", want: true},
		{name: "literal followed by parameter", route: "/users/:id", want: true},
		{name: "parameter followed by literal", route: "/:id/profile", want: true},
		{name: "multiple parameters", route: "/:user_id/:post_id", want: true},
		{name: "parameter between literals", route: "/api/:version/users/:id", want: true},
		{name: "parameter at root", route: "/:id", want: true},
		// ============================================================
		// Wildcard segments
		// ============================================================
		{name: "standalone wildcard", route: "/*", want: true},
		{name: "wildcard after literal", route: "/api/*", want: true},
		{name: "wildcard before literal", route: "/*/users", want: true},
		{name: "wildcard between literals", route: "/api/*/users", want: true},
		{name: "wildcard before name", route: "/*user", want: true},
		{name: "wildcard after name", route: "/user*", want: true},
		{name: "wildcard inside name", route: "/us*er", want: true},
		{name: "wildcard surrounding name", route: "/*user*", want: true},
		{name: "multiple wildcards", route: "/a*b*c", want: true},
		{name: "only wildcards", route: "/***", want: true},
		{name: "wildcard in multiple segments", route: "/api/*user*/posts/*id*", want: true},
		{name: "wildcard with underscore and hyphen", route: "/user_*-id", want: true},
		// ============================================================
		// Parameters must not contain wildcards
		// ============================================================
		{name: "parameter ending with wildcard", route: "/:user_*", want: false},
		{name: "parameter starting with wildcard", route: "/:*user", want: false},
		{name: "parameter containing wildcard", route: "/:us*er", want: false},
		{name: "parameter only wildcard", route: "/:*", want: false},
		{name: "parameter with multiple wildcards", route: "/:a*b*c", want: false},
		{name: "parameter wildcard in middle of route", route: "/api/:user_*/posts", want: false},
		{name: "wildcard followed by parameter", route: "/*/:id", want: true},
		// ============================================================
		// Empty segments and repeated slashes
		// ============================================================
		{name: "double slash at beginning", route: "//xyz", want: false},
		{name: "double slash in middle", route: "/api//users", want: false},
		{name: "double slash at end", route: "/api//", want: false},
		{name: "trailing slash", route: "/api/", want: false},
		{name: "trailing slash after parameter", route: "/api/:id/", want: false},
		{name: "trailing slash after wildcard", route: "/api/*/", want: false},
		{name: "multiple consecutive slashes", route: "///", want: false},
		{name: "slash between every character", route: "/a//b//c", want: false},
		// ============================================================
		// Invalid parameter syntax
		// ============================================================
		{name: "empty parameter", route: "/:", want: false},
		{name: "parameter with only underscore", route: "/:_", want: true},
		{name: "parameter with only hyphen", route: "/:-", want: true},
		{name: "parameter with space", route: "/:user id", want: false},
		{name: "parameter with dot", route: "/:user.id", want: false},
		{name: "parameter with dollar sign", route: "/:$id", want: false},
		{name: "parameter with percent", route: "/:%id", want: false},
		{name: "parameter with slash", route: "/:user/id", want: true},
		// ============================================================
		// Invalid characters in literal/wildcard segments
		// ============================================================
		{name: "space", route: "/hello world", want: false},
		{name: "dot", route: "/api.users", want: false},
		{name: "question mark", route: "/users?", want: false},
		{name: "hash", route: "/users#section", want: false},
		{name: "percent", route: "/users%20", want: false},
		{name: "at sign", route: "/users@home", want: false},
		{name: "plus sign", route: "/users+", want: false},
		{name: "equals sign", route: "/users=id", want: false},
		{name: "comma", route: "/users,id", want: false},
		{name: "semicolon", route: "/users;id", want: false},
		{name: "backslash", route: `/users\id`, want: false},
		// ============================================================
		// Unicode characters
		// ============================================================
		{name: "Arabic letters", route: "/مستخدم", want: false},
		{name: "Chinese letters", route: "/用户", want: false},
		{name: "emoji", route: "/🚀", want: false},
		// ============================================================
		// Anchoring and malformed paths
		// ============================================================
		{name: "missing leading slash", route: "api/users", want: false},
		{name: "leading whitespace", route: " /api", want: false},
		{name: "trailing whitespace", route: "/api ", want: false},
		{name: "newline at end", route: "/api\n", want: false},
		{name: "newline at beginning", route: "\n/api", want: false},
		{name: "query string", route: "/api?foo=bar", want: false},
		{name: "fragment", route: "/api#users", want: false},
		{name: "full URL", route: "https://example.com/api", want: false},
		{name: "leading double slash URL-like", route: "//example.com/api", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isValidRoutePattern(tc.route))
		})
	}
}

func TestRouter_AreParamNamesUnique(t *testing.T) {
	var testCases = []struct {
		name    string
		pattern string
		want    bool
	}{
		{name: "root", pattern: "/", want: true},
		{name: "static", pattern: "/api", want: true},
		{name: "single parameter", pattern: "/users/:id", want: true},
		{name: "distinct parameters", pattern: "/users/:user_id/posts/:post_id", want: true},
		{name: "distinct root parameters", pattern: "/:category/:slug", want: true},
		{name: "parameter with hyphen", pattern: "/api/:version-id", want: true},
		{name: "duplicate parameters", pattern: "/api/:id/api/:id", want: false},
		{name: "duplicate root parameters", pattern: "/:id/:id", want: false},
		{name: "duplicate nested parameters", pattern: "/users/:id/posts/:id", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, areParamNamesUnique(tc.pattern))
		})
	}
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

	t.Run("method already registered", func(t *testing.T) {
		app := New()
		app.Map(http.MethodGet, "/api/users", dummy)
		require.Panics(t, func() { app.Map(http.MethodGet, "/api/users", dummy) })
	})
}

// Each method shortcut must register exactly its own method via Map.
func TestRouter_MethodShortcuts(t *testing.T) {
	testCases := []struct {
		name     string
		register func(app *App, pattern string, handlers ...Handler)
		method   string
	}{
		{"MapGet", func(app *App, p string, h ...Handler) { app.MapGet(p, h...) }, http.MethodGet},
		{"MapHead", func(app *App, p string, h ...Handler) { app.MapHead(p, h...) }, http.MethodHead},
		{"MapPost", func(app *App, p string, h ...Handler) { app.MapPost(p, h...) }, http.MethodPost},
		{"MapPut", func(app *App, p string, h ...Handler) { app.MapPut(p, h...) }, http.MethodPut},
		{"MapPatch", func(app *App, p string, h ...Handler) { app.MapPatch(p, h...) }, http.MethodPatch},
		{"MapDelete", func(app *App, p string, h ...Handler) { app.MapDelete(p, h...) }, http.MethodDelete},
		{"MapConnect", func(app *App, p string, h ...Handler) { app.MapConnect(p, h...) }, http.MethodConnect},
		{"MapOptions", func(app *App, p string, h ...Handler) { app.MapOptions(p, h...) }, http.MethodOptions},
		{"MapTrace", func(app *App, p string, h ...Handler) { app.MapTrace(p, h...) }, http.MethodTrace},
		{"MapQuery", func(app *App, p string, h ...Handler) { app.MapQuery(p, h...) }, MethodQuery},
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

			// duplicate registration on the same method panics
			require.Panics(t, func() {
				tc.register(app, "/api/users", func(ctx *Context) error { return nil })
			})
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

	// MapAll panics once any method on the pattern is taken
	require.Panics(t, func() {
		app.MapAll("/api/users", func(ctx *Context) error { return nil })
	})
	require.Panics(t, func() {
		app.MapGet("/api/users", func(ctx *Context) error { return nil })
	})
}

// MapAll with no handlers does nothing, like Map.
func TestRouter_MapAll_NoHandlers(t *testing.T) {
	app := New()
	require.NotPanics(t, func() { app.MapAll("/api/users") })
	require.NotPanics(t, func() { app.MapGet("/api/users") })
}

func TestRouter_UsePanics(t *testing.T) {
	dummy := func(ctx *Context) error { return nil }

	invalidPrefixes := []string{
		"",
		"api",
		"/api/",
		"/api//users",
		"/:id",
		"/api/:id",
		"/*",
		"/api/*",
	}

	for _, prefix := range invalidPrefixes {
		t.Run("invalid prefix "+prefix, func(t *testing.T) {
			app := New()
			require.Panics(t, func() { app.Use(prefix, dummy) })
		})
	}
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
		require.Equal(t, tc.expected, isValidHttpMethod(tc.input))
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
		// ============================================================
		// Static match
		// ============================================================
		{
			name:       "static match",
			pattern:    "/users",
			path:       "/users",
			wantParams: map[string]string{},
			wantMatch:  true,
		},
		{
			name:       "static multi-segment match",
			pattern:    "/api/users",
			path:       "/api/users",
			wantParams: map[string]string{},
			wantMatch:  true,
		},
		// ============================================================
		// Parameter extraction
		// ============================================================
		{
			name:       "single parameter",
			pattern:    "/users/:id",
			path:       "/users/123",
			wantParams: map[string]string{"id": "123"},
			wantMatch:  true,
		},
		{
			name:       "parameter stops at slash",
			pattern:    "/users/:id",
			path:       "/users/123/profile",
			wantParams: nil,
			wantMatch:  false,
		},
		{
			name:       "parameter with string value",
			pattern:    "/users/:id",
			path:       "/users/abc",
			wantParams: map[string]string{"id": "abc"},
			wantMatch:  true,
		},
		// ============================================================
		// Multiple parameters
		// ============================================================
		{
			name:       "multiple parameters",
			pattern:    "/users/:user_id/posts/:post_id",
			path:       "/users/123/posts/456",
			wantParams: map[string]string{"user_id": "123", "post_id": "456"},
			wantMatch:  true,
		},
		{
			name:       "multiple parameters with strings",
			pattern:    "/:category/:slug",
			path:       "/books/go-programming",
			wantParams: map[string]string{"category": "books", "slug": "go-programming"},
			wantMatch:  true,
		},
		// ============================================================
		// Mismatch
		// ============================================================
		{
			name:       "different static segment",
			pattern:    "/users",
			path:       "/posts",
			wantParams: nil,
			wantMatch:  false,
		},
		{
			name:       "different segment with parameter route",
			pattern:    "/users/:id",
			path:       "/posts/123",
			wantParams: nil,
			wantMatch:  false,
		},
		{
			name:       "missing segment",
			pattern:    "/users/:id",
			path:       "/users",
			wantParams: nil,
			wantMatch:  false,
		},
		{
			name:       "extra segment",
			pattern:    "/users/:id",
			path:       "/users/123/profile",
			wantParams: nil,
			wantMatch:  false,
		},
		// ============================================================
		// Strict ^$ matching
		// ============================================================
		{
			name:       "prefix is not enough",
			pattern:    "/users",
			path:       "/users/123",
			wantParams: nil,
			wantMatch:  false,
		},
		{
			name:       "suffix is not allowed",
			pattern:    "/users",
			path:       "prefix/users",
			wantParams: nil,
			wantMatch:  false,
		},
		{
			name:       "trailing slash does not match",
			pattern:    "/users",
			path:       "/users/",
			wantParams: nil,
			wantMatch:  false,
		},
		// ============================================================
		// Wildcard
		// ============================================================
		{
			name:       "standalone wildcard",
			pattern:    "/files/*",
			path:       "/files/a/b/c.txt",
			wantParams: map[string]string{},
			wantMatch:  true,
		},
		{
			name:       "wildcard inside segment",
			pattern:    "/users/*-profile",
			path:       "/users/123-profile",
			wantParams: map[string]string{},
			wantMatch:  true,
		},
		{
			name:       "wildcard before parameter",
			pattern:    "/files/*/:id",
			path:       "/files/a/b/123",
			wantParams: map[string]string{"id": "123"},
			wantMatch:  true,
		},
		{
			name:       "wildcard mismatch",
			pattern:    "/files/*/123",
			path:       "/files/a/b",
			wantParams: nil,
			wantMatch:  false,
		},
		// ============================================================
		// Root route
		// ============================================================
		{
			name:       "root match",
			pattern:    "/",
			path:       "/",
			wantParams: map[string]string{},
			wantMatch:  true,
		},
		{
			name:       "root does not match extra path",
			pattern:    "/",
			path:       "/users",
			wantParams: nil,
			wantMatch:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			route := Route{pattern: tc.pattern}
			gotParams, gotMatch := route.matchPath(tc.path)
			require.Equal(t, tc.wantMatch, gotMatch)
			require.Equal(t, tc.wantParams, gotParams)
		})
	}
}

func TestRouter_Dispatch(t *testing.T) {
	var newHandler = func(name string, called *[]string) Handler {
		return func(ctx *Context) error {
			*called = append(*called, name)
			return ctx.Next()
		}
	}

	t.Run("Use() is order-sensitive", func(t *testing.T) {
		t.Run("use before handle", func(t *testing.T) {
			app := New()
			var called []string

			app.Use("/api", newHandler("middleware", &called))
			app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users/123", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, []string{"middleware", "handler"}, called)
		})

		t.Run("use after handle", func(t *testing.T) {
			app := New()
			var called []string

			app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))
			app.Use("/api", newHandler("middleware", &called))

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users/123", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, []string{"handler"}, called)
		})
	})

	t.Run("nested prefixes", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api", newHandler("api", &called))
		app.Use("/api/users", newHandler("users", &called))
		app.Map(http.MethodGet, "/api/users/:id", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users/123", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"api", "users", "handler"}, called)
	})

	t.Run("use merges parent prefixes", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api", newHandler("api", &called))
		app.Use("/api/v1", newHandler("v1", &called))
		app.Use("/api/v1/admin", newHandler("admin", &called))
		app.Map(http.MethodGet, "/api/v1/admin/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"api", "v1", "admin", "handler"}, called)
	})

	t.Run("middleware applies to each method", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api", newHandler("middleware", &called))
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

	t.Run("raw string prefix matching", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/api", newHandler("middleware", &called))
		app.Map(http.MethodGet, "/api/users", newHandler("api", &called))
		app.Map(http.MethodGet, "/api2/users", newHandler("api2", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/api/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "api"}, called)

		called = nil
		resp = app.Test(httptest.NewRequest(http.MethodGet, "/api2/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware", "api2"}, called)
	})

	t.Run("subsequent prefixes inherit parent middleware", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/x", newHandler("x", &called))
		app.Use("/x/y", newHandler("x/y", &called))
		app.Map(http.MethodGet, "/x/y/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x/y/users", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"x", "x/y", "handler"}, called)
	})

	t.Run("middleware-only route handles request", func(t *testing.T) {
		app := New()
		var called []string

		app.Use("/x", newHandler("middleware", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, []string{"middleware"}, called)
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

	t.Run("unknown path returns 404 invalid_endpoint", func(t *testing.T) {
		app := New()
		var called []string
		app.Map(http.MethodGet, "/users", newHandler("handler", &called))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
		require.Equal(t, ErrInvalidEndpoint.StatusCode, resp.StatusCode)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)

		var body struct {
			Kind string `json:"kind"`
		}
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &body))
		require.Equal(t, ErrInvalidEndpoint.Kind, body.Kind)
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

	t.Run("unknown path reaches custom error handler as invalid_endpoint", func(t *testing.T) {
		var captured error
		var pattern string

		app := New(
			WithErrorHandler(func(ctx *Context, err error) {
				captured = err
				pattern = ctx.GetPattern()
				ctx.WriteAs(http.StatusNotFound, CodecJson, ErrInvalidEndpoint)
			}),
		)
		app.Map(http.MethodGet, "/users", newHandler("handler", &[]string{}))

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/nope", nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.ErrorIs(t, captured, ErrInvalidEndpoint)
		require.Equal(t, "", pattern)
	})

	t.Run("wrong method reaches custom error handler as method_not_allowed", func(t *testing.T) {
		var captured error
		var pattern string

		app := New(
			WithErrorHandler(func(ctx *Context, err error) {
				captured = err
				pattern = ctx.GetPattern()
				ctx.WriteAs(http.StatusMethodNotAllowed, CodecJson, ErrMethodNotAllowed)
			}),
		)
		app.Map(http.MethodGet, "/users", newHandler("handler", &[]string{}))

		resp := app.Test(httptest.NewRequest(http.MethodPost, "/users", nil))
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		require.ErrorIs(t, captured, ErrMethodNotAllowed)
		require.Equal(t, "", pattern)
	})

	t.Run("middleware Next chain and error bubbling to errorHandler", func(t *testing.T) {
		sentinel := errors.New("boom")
		var order []string
		var captured error

		app := New(
			WithErrorHandler(func(ctx *Context, err error) {
				captured = err
				ctx.WriteStatus(http.StatusTeapot)
			}),
		)

		app.Use("/", func(ctx *Context) error {
			order = append(order, "mw1-before")
			err := ctx.Next()
			order = append(order, "mw1-after")
			return err
		})
		app.Use("/x", func(ctx *Context) error {
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
		require.Equal(t, "200", loggedStatus(t, logs))
	})

	t.Run("logs written status", func(t *testing.T) {
		logs := &captureLogHandler{}
		app := New(WithLogger(slog.New(logs)), WithRequestLogging(true))
		app.Map(http.MethodGet, "/x", func(ctx *Context) error {
			return ctx.Write(http.StatusCreated, "hello")
		})

		resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		require.Equal(t, "201", loggedStatus(t, logs))
	})
}
