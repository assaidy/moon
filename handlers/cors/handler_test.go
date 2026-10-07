package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

const defaultAllowMethods = "HEAD, GET, POST, PUT, PATCH, DELETE, QUERY"

func newTestApp(handler *Handler) *moon.App {
	app := moon.New()
	app.UseAll(handler.Handle)
	app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
		return ctx.Write(http.StatusOK, "downstream")
	})
	return app
}

func request(app *moon.App, method, path string, headers map[string]string) *http.Response {
	req := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return app.Test(req)
}

func requireHeaders(t *testing.T, resp *http.Response, want map[string]string) {
	t.Helper()
	for name, value := range want {
		require.Equal(t, value, resp.Header.Get(name), "header %s", name)
	}
}

func requireAbsentHeaders(t *testing.T, resp *http.Response, names ...string) {
	t.Helper()
	for _, name := range names {
		require.Empty(t, resp.Header.Values(name), "header %s", name)
	}
}

func TestNew(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		require.NotPanics(t, func() { New() })
	})

	t.Run("credentials with the default wildcard origin panics", func(t *testing.T) {
		require.PanicsWithValue(t,
			"credentials cannot be allowed when allowed origins contain a wildcard '*'",
			func() { New(NewOptions().WithAllowCredentials(true)) },
		)
	})

	t.Run("credentials with an explicit wildcard origin panics", func(t *testing.T) {
		require.PanicsWithValue(t,
			"credentials cannot be allowed when allowed origins contain a wildcard '*'",
			func() {
				New(NewOptions().WithAllowedOrigins([]string{"*"}).WithAllowCredentials(true))
			},
		)
	})

	t.Run("credentials with explicit origins are allowed", func(t *testing.T) {
		require.NotPanics(t, func() {
			New(NewOptions().
				WithAllowedOrigins([]string{"https://app.example"}).
				WithAllowCredentials(true))
		})
	})

	t.Run("more than one options panics", func(t *testing.T) {
		require.Panics(t, func() { New(NewOptions(), NewOptions()) })
	})
}

func TestNewOptions(t *testing.T) {
	opts := NewOptions()

	require.Equal(t, []string{"*"}, opts.allowedOrogins)
	require.Equal(t, []string{
		moon.MethodHead,
		moon.MethodGet,
		moon.MethodPost,
		moon.MethodPut,
		moon.MethodPatch,
		moon.MethodDelete,
		moon.MethodQuery,
	}, opts.allowedMethods)
	require.Equal(t, defaultAllowMethods, opts.allowedMethodsString)
	require.Equal(t, -1, opts.maxAge, "max age is off by default")
	require.Nil(t, opts.allowedOriginsFunc)
	require.Nil(t, opts.skip)
	require.False(t, opts.allowCredentials)
	require.False(t, opts.allowPrivateNetwork)
	require.Empty(t, opts.allowedHeaders)
	require.Empty(t, opts.allowedHeadersString)
	require.Empty(t, opts.exposedHeaders)
	require.Empty(t, opts.exposedHeadersString)
}

func TestWithAllowedOrigins(t *testing.T) {
	t.Run("normalizes entries", func(t *testing.T) {
		opts := NewOptions().WithAllowedOrigins([]string{
			" HTTPS://Example.COM/ ",
			"http://sub.Example.com:80",
			"HTTPS://example.com:443",
			"null",
		})
		require.Equal(t, []string{
			"https://example.com",
			"http://sub.example.com",
			"https://example.com",
			"null",
		}, opts.allowedOrogins)
	})

	t.Run("keeps non default ports", func(t *testing.T) {
		opts := NewOptions().WithAllowedOrigins([]string{"https://example.com:8443"})
		require.Equal(t, []string{"https://example.com:8443"}, opts.allowedOrogins)
	})

	t.Run("lone wildcard is kept verbatim", func(t *testing.T) {
		opts := NewOptions().WithAllowedOrigins([]string{" * "})
		require.Equal(t, []string{"*"}, opts.allowedOrogins)
	})

	t.Run("wildcard combined with other origins panics", func(t *testing.T) {
		require.PanicsWithValue(t, "wildcard '*' cannot be combined with other origins", func() {
			NewOptions().WithAllowedOrigins([]string{"*", "https://app.example"})
		})
	})

	t.Run("normalizes subdomain wildcards", func(t *testing.T) {
		opts := NewOptions().WithAllowedOrigins([]string{
			" HTTPS://*.Example.COM/ ",
			"http://*.example.com:80",
			"https://*.example.com:8443",
		})
		require.Equal(t, []string{
			"https://*.example.com",
			"http://*.example.com",
			"https://*.example.com:8443",
		}, opts.allowedOrogins)
	})

	t.Run("subdomain wildcards combine with other origins", func(t *testing.T) {
		opts := NewOptions().WithAllowedOrigins([]string{
			"https://*.example.com",
			"https://app.example",
			"null",
		})
		require.Equal(t, []string{
			"https://*.example.com",
			"https://app.example",
			"null",
		}, opts.allowedOrogins)
	})

	t.Run("subdomain wildcards may be paired with credentials", func(t *testing.T) {
		require.NotPanics(t, func() {
			New(NewOptions().
				WithAllowedOrigins([]string{"https://*.example.com"}).
				WithAllowCredentials(true))
		})
	})

	invalidOrigins := []struct {
		name   string
		origin string
	}{
		{"empty", ""},
		{"bare host", "example.com"},
		{"missing scheme", "www.example.com"},
		{"star glued to the host", "https://*example.com"},
		{"bare star host", "*example.com"},
		{"star in place of the TLD", "https://example.*"},
		{"star as the whole host", "https://*"},
		{"pattern without a domain", "https://*."},
		{"pattern with empty label", "https://*..example.com"},
		{"two wildcards", "https://*.*.example.com"},
		{"wildcard in the middle", "https://sub.*.example.com"},
		{"path", "https://example.com/app"},
		{"userinfo", "https://user@example.com"},
		{"query", "https://example.com?a=1"},
		{"fragment", "https://example.com#frag"},
		{"bad port", "http://example.com:port"},
		{"no host", "https:///path"},
	}
	for _, tc := range invalidOrigins {
		t.Run("invalid origin "+tc.name+" panics", func(t *testing.T) {
			require.PanicsWithValue(t, `invalid origin "`+tc.origin+`" at index 0`, func() {
				NewOptions().WithAllowedOrigins([]string{tc.origin})
			})
		})
	}

	t.Run("reports the index of the invalid origin", func(t *testing.T) {
		require.PanicsWithValue(t, `invalid origin "bad" at index 1`, func() {
			NewOptions().WithAllowedOrigins([]string{"https://app.example", "bad"})
		})
	})
}

func TestNormalizeOrigin(t *testing.T) {
	valid := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases scheme and host", "HTTPS://Example.COM", "https://example.com"},
		{"trims and drops trailing slash", " http://example.com/ ", "http://example.com"},
		{"drops the http default port", "http://example.com:80", "http://example.com"},
		{"drops the https default port", "https://example.com:443", "https://example.com"},
		{"keeps other ports", "https://example.com:8443", "https://example.com:8443"},
		{"keeps explicit root path", "https://example.com/", "https://example.com"},
		{"null origin", "null", "null"},
		{"non http scheme", "chrome-extension://abcdef", "chrome-extension://abcdef"},
		{"ipv6 literal", "http://[::1]:8080", "http://[::1]:8080"},
	}
	for _, tc := range valid {
		t.Run("valid "+tc.name, func(t *testing.T) {
			got, ok := normalizeOrigin(tc.in)
			require.True(t, ok, "normalizeOrigin(%q)", tc.in)
			require.Equal(t, tc.want, got)
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"wildcard", "*"},
		{"pattern is not a request origin", "https://*.example.com"},
		{"star glued to the host", "https://*example.com"},
		{"bare star host", "*example.com"},
		{"star in place of the TLD", "https://example.*"},
		{"star as the whole host", "https://*"},
		{"bare host", "example.com"},
		{"path", "https://example.com/app"},
		{"userinfo", "https://user@example.com"},
		{"query", "https://example.com?a=1"},
		{"fragment", "https://example.com#f"},
		{"bad port", "http://example.com:notaport"},
		{"host without value", "chrome-extension://"},
		{"scheme without host", "https:///path"},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			got, ok := normalizeOrigin(tc.in)
			require.False(t, ok, "normalizeOrigin(%q) = %q", tc.in, got)
			require.Empty(t, got)
		})
	}
}

func TestNormalizeOriginPattern(t *testing.T) {
	valid := []struct {
		name string
		in   string
		want string
	}{
		{"plain origin delegates", "https://app.example.com", "https://app.example.com"},
		{"subdomain wildcard", "https://*.example.com", "https://*.example.com"},
		{"normalizes case and trailing slash", " HTTPS://*.Example.COM/ ", "https://*.example.com"},
		{"drops the default port", "http://*.example.com:80", "http://*.example.com"},
		{"keeps other ports", "https://*.example.com:8443", "https://*.example.com:8443"},
		{"nested domain", "https://*.v2.example.com", "https://*.v2.example.com"},
	}
	for _, tc := range valid {
		t.Run("valid "+tc.name, func(t *testing.T) {
			got, ok := normalizeOriginPattern(tc.in)
			require.True(t, ok, "normalizeOriginPattern(%q)", tc.in)
			require.Equal(t, tc.want, got)
		})
	}

	invalid := []struct {
		name string
		in   string
	}{
		{"lone wildcard", "*"},
		{"no scheme", "*.example.com"},
		{"star glued to the host", "https://*example.com"},
		{"star in place of the TLD", "https://example.*"},
		{"no domain", "https://*"},
		{"empty domain", "https://*."},
		{"empty label", "https://*..example.com"},
		{"trailing dot", "https://*.example.com."},
		{"two wildcards", "https://*.*.example.com"},
		{"wildcard in the middle", "https://sub.*.example.com"},
		{"path", "https://*.example.com/app"},
		{"userinfo", "https://user@*.example.com"},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			got, ok := normalizeOriginPattern(tc.in)
			require.False(t, ok, "normalizeOriginPattern(%q) = %q", tc.in, got)
			require.Empty(t, got)
		})
	}
}

func TestOriginMatches(t *testing.T) {
	testCases := []struct {
		name      string
		allowed   []string
		origin    string
		wantMatch bool
	}{
		{
			name:      "exact origin",
			allowed:   []string{"https://app.example.com"},
			origin:    "https://app.example.com",
			wantMatch: true,
		},
		{
			name:      "exact origin mismatch",
			allowed:   []string{"https://app.example.com"},
			origin:    "https://evil.example.com",
			wantMatch: false,
		},
		{
			name:      "wildcard matches one label",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://app.example.com",
			wantMatch: true,
		},
		{
			name:      "wildcard matches nested labels",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://api.v2.example.com",
			wantMatch: true,
		},
		{
			name:      "wildcard never matches the apex",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://example.com",
			wantMatch: false,
		},
		{
			name:      "wildcard requires the same scheme",
			allowed:   []string{"https://*.example.com"},
			origin:    "http://app.example.com",
			wantMatch: false,
		},
		{
			name:      "wildcard requires the same port",
			allowed:   []string{"https://*.example.com:8443"},
			origin:    "https://app.example.com:8443",
			wantMatch: true,
		},
		{
			name:      "wildcard with port does not match the default port",
			allowed:   []string{"https://*.example.com:8443"},
			origin:    "https://app.example.com",
			wantMatch: false,
		},
		{
			name:      "look alike host is not a subdomain",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://example.com.evil.com",
			wantMatch: false,
		},
		{
			name:      "suffix glued host is not a subdomain",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://notexample.com",
			wantMatch: false,
		},
		{
			name:      "empty label does not match",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://sub..example.com",
			wantMatch: false,
		},
		{
			name:      "empty origin label does not match",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://.example.com",
			wantMatch: false,
		},
		{
			name:      "pattern with empty domain does not match the scheme only",
			allowed:   []string{"https://*.example.com"},
			origin:    "https://",
			wantMatch: false,
		},
		{
			name:      "wildcard combined with a concrete origin",
			allowed:   []string{"https://*.example.com", "https://other.example"},
			origin:    "https://other.example",
			wantMatch: true,
		},
		{
			name:      "lone wildcard matches every origin",
			allowed:   []string{"*"},
			origin:    "https://any.example",
			wantMatch: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.wantMatch, originMatches(tc.origin, tc.allowed))
		})
	}
}

func TestSubdomainWildcards(t *testing.T) {
	handler := New(NewOptions().
		WithAllowedOrigins([]string{"https://*.example.com"}).
		WithAllowCredentials(true))
	app := newTestApp(handler)

	allowedOrigins := []string{
		"https://app.example.com",
		"https://api.v2.example.com",
	}
	for _, origin := range allowedOrigins {
		t.Run("allows "+origin, func(t *testing.T) {
			resp := request(app, http.MethodGet, "/resource", map[string]string{headerOrigin: origin})
			require.Equal(t, http.StatusOK, resp.StatusCode)
			requireHeaders(t, resp, map[string]string{
				headerAccessControlAllowOrigin:      origin,
				headerAccessControlAllowCredentials: "true",
				headerVary:                          "Origin",
			})
		})
	}

	deniedOrigins := []string{
		"https://example.com",          // the apex is not a subdomain
		"http://app.example.com",       // wrong scheme
		"https://example.com.evil.com", // look alike host
		"https://app.example.com:8443", // wrong port
		"https://other.example",        // unrelated host
	}
	for _, origin := range deniedOrigins {
		t.Run("denies "+origin, func(t *testing.T) {
			resp := request(app, http.MethodGet, "/resource", map[string]string{headerOrigin: origin})
			require.Equal(t, http.StatusOK, resp.StatusCode)
			requireHeaders(t, resp, map[string]string{headerVary: "Origin"})
			requireAbsentHeaders(t, resp, headerAccessControlAllowOrigin, headerAccessControlAllowCredentials)
		})
	}

	t.Run("preflight allows a subdomain", func(t *testing.T) {
		resp := request(app, http.MethodOptions, "/resource", map[string]string{
			headerOrigin:                     "https://app.example.com",
			headerAccessControlRequestMethod: moon.MethodPost,
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		requireHeaders(t, resp, map[string]string{
			headerAccessControlAllowOrigin:      "https://app.example.com",
			headerAccessControlAllowCredentials: "true",
			headerAccessControlAllowMethods:     defaultAllowMethods,
		})
	})

	t.Run("preflight denies the apex", func(t *testing.T) {
		resp := request(app, http.MethodOptions, "/resource", map[string]string{
			headerOrigin:                     "https://example.com",
			headerAccessControlRequestMethod: moon.MethodPost,
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		requireAbsentHeaders(t, resp, headerAccessControlAllowOrigin, headerAccessControlAllowCredentials)
		require.Equal(t, defaultAllowMethods, resp.Header.Get(headerAccessControlAllowMethods))
	})
}

func TestHandleGeneralRequest(t *testing.T) {
	testCases := []struct {
		name       string
		handler    *Handler
		headers    map[string]string
		wantHeader map[string]string
		wantAbsent []string
	}{
		{
			name:    "wildcard allows any origin",
			handler: New(),
			headers: map[string]string{headerOrigin: "https://any.example"},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin: "*",
			},
			wantAbsent: []string{
				headerVary,
				headerAccessControlAllowCredentials,
				headerAccessControlExposeHeaders,
			},
		},
		{
			name:       "no origin header with wildcard emits nothing",
			handler:    New(),
			wantAbsent: []string{headerAccessControlAllowOrigin, headerVary},
		},
		{
			name:    "listed origin is echoed and varies",
			handler: New(NewOptions().WithAllowedOrigins([]string{"https://app.example"})),
			headers: map[string]string{headerOrigin: "https://app.example"},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin: "https://app.example",
				headerVary:                     "Origin",
			},
			wantAbsent: []string{headerAccessControlAllowCredentials},
		},
		{
			name:    "request origin is normalized before matching",
			handler: New(NewOptions().WithAllowedOrigins([]string{"https://app.example"})),
			headers: map[string]string{headerOrigin: "HTTPS://App.Example/"},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin: "https://app.example",
				headerVary:                     "Origin",
			},
		},
		{
			name:    "unlisted origin gets no allow origin",
			handler: New(NewOptions().WithAllowedOrigins([]string{"https://app.example"})),
			headers: map[string]string{headerOrigin: "https://evil.example"},
			wantHeader: map[string]string{
				headerVary: "Origin",
			},
			wantAbsent: []string{
				headerAccessControlAllowOrigin,
				headerAccessControlAllowCredentials,
				headerAccessControlExposeHeaders,
			},
		},
		{
			name:       "no origin header with restricted origins still varies",
			handler:    New(NewOptions().WithAllowedOrigins([]string{"https://app.example"})),
			wantHeader: map[string]string{headerVary: "Origin"},
			wantAbsent: []string{headerAccessControlAllowOrigin},
		},
		{
			name: "malformed request origin is rejected",
			handler: New(NewOptions().
				WithAllowedOrigins([]string{"https://app.example"})),
			headers:    map[string]string{headerOrigin: "not an origin"},
			wantHeader: map[string]string{headerVary: "Origin"},
			wantAbsent: []string{headerAccessControlAllowOrigin},
		},
		{
			name:    "literal null origin is matched",
			handler: New(NewOptions().WithAllowedOrigins([]string{"null"})),
			headers: map[string]string{headerOrigin: "null"},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin: "null",
				headerVary:                     "Origin",
			},
		},
		{
			name: "credentials and exposed headers",
			handler: New(NewOptions().
				WithAllowedOrigins([]string{"https://app.example"}).
				WithAllowCredentials(true).
				WithExposedHeaders([]string{"X-Total-Count", "X-Page"})),
			headers: map[string]string{headerOrigin: "https://app.example"},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin:      "https://app.example",
				headerAccessControlAllowCredentials: "true",
				headerAccessControlExposeHeaders:    "X-Total-Count, X-Page",
				headerVary:                          "Origin",
			},
		},
		{
			name: "exposed headers are not set when the origin is denied",
			handler: New(NewOptions().
				WithAllowedOrigins([]string{"https://app.example"}).
				WithExposedHeaders([]string{"X-Total-Count"})),
			headers: map[string]string{headerOrigin: "https://evil.example"},
			wantHeader: map[string]string{
				headerVary: "Origin",
			},
			wantAbsent: []string{
				headerAccessControlAllowOrigin,
				headerAccessControlAllowCredentials,
				headerAccessControlExposeHeaders,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := request(newTestApp(tc.handler), http.MethodGet, "/resource", tc.headers)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			requireHeaders(t, resp, tc.wantHeader)
			requireAbsentHeaders(t, resp, tc.wantAbsent...)
		})
	}
}

func TestAllowedOriginsFunc(t *testing.T) {
	t.Run("consulted when the list does not match", func(t *testing.T) {
		calledWith := ""
		handler := New(NewOptions().
			WithAllowedOrigins([]string{"https://app.example"}).
			WithAllowedOriginsFunc(func(origin string) bool {
				calledWith = origin
				return origin == "https://dynamic.example"
			}))

		resp := request(newTestApp(handler), http.MethodGet, "/resource",
			map[string]string{headerOrigin: "https://dynamic.example"})

		require.Equal(t, "https://dynamic.example", calledWith)
		require.Equal(t, "https://dynamic.example", resp.Header.Get(headerAccessControlAllowOrigin))
		require.Equal(t, "Origin", resp.Header.Get(headerVary))
	})

	t.Run("raw origin is passed to the func", func(t *testing.T) {
		calledWith := ""
		handler := New(NewOptions().
			WithAllowedOrigins([]string{"https://app.example"}).
			WithAllowedOriginsFunc(func(origin string) bool {
				calledWith = origin
				return true
			}))

		resp := request(newTestApp(handler), http.MethodGet, "/resource",
			map[string]string{headerOrigin: "https://Dynamic.Example"})

		require.Equal(t, "https://Dynamic.Example", calledWith)
		require.Equal(t, "https://Dynamic.Example", resp.Header.Get(headerAccessControlAllowOrigin))
	})

	t.Run("rejecting func emits no allow origin", func(t *testing.T) {
		handler := New(NewOptions().
			WithAllowedOrigins([]string{"https://app.example"}).
			WithAllowedOriginsFunc(func(string) bool { return false }))

		resp := request(newTestApp(handler), http.MethodGet, "/resource",
			map[string]string{headerOrigin: "https://dynamic.example"})

		requireAbsentHeaders(t, resp, headerAccessControlAllowOrigin, headerAccessControlAllowCredentials)
		require.Equal(t, "Origin", resp.Header.Get(headerVary))
	})

	t.Run("the list wins and the func is not consulted", func(t *testing.T) {
		called := false
		handler := New(NewOptions().
			WithAllowedOrigins([]string{"https://app.example"}).
			WithAllowedOriginsFunc(func(string) bool {
				called = true
				return false
			}))

		resp := request(newTestApp(handler), http.MethodGet, "/resource",
			map[string]string{headerOrigin: "https://app.example"})

		require.False(t, called, "the func must not run when the list matches")
		require.Equal(t, "https://app.example", resp.Header.Get(headerAccessControlAllowOrigin))
	})

	t.Run("wildcard wins and the func is not consulted", func(t *testing.T) {
		called := false
		handler := New(NewOptions().
			WithAllowedOrigins([]string{"*"}).
			WithAllowedOriginsFunc(func(string) bool {
				called = true
				return false
			}))

		resp := request(newTestApp(handler), http.MethodGet, "/resource",
			map[string]string{headerOrigin: "https://any.example"})

		require.False(t, called, "the func must not run when every origin is allowed")
		require.Equal(t, "*", resp.Header.Get(headerAccessControlAllowOrigin))
		requireAbsentHeaders(t, resp, headerVary)
	})

	t.Run("malformed origin is rejected before the func", func(t *testing.T) {
		called := false
		handler := New(NewOptions().
			WithAllowedOrigins([]string{"https://app.example"}).
			WithAllowedOriginsFunc(func(string) bool {
				called = true
				return true
			}))

		resp := request(newTestApp(handler), http.MethodGet, "/resource",
			map[string]string{headerOrigin: "not an origin"})

		require.False(t, called, "the func must not run for a malformed origin")
		requireAbsentHeaders(t, resp, headerAccessControlAllowOrigin)
	})
}

func TestHandlePreflight(t *testing.T) {
	testCases := []struct {
		name       string
		handler    *Handler
		headers    map[string]string
		wantHeader map[string]string
		wantAbsent []string
	}{
		{
			name:    "defaults",
			handler: New(),
			headers: map[string]string{
				headerOrigin:                     "https://any.example",
				headerAccessControlRequestMethod: moon.MethodPost,
			},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin:  "*",
				headerAccessControlAllowMethods: defaultAllowMethods,
				headerVary:                      preflightVary,
			},
			wantAbsent: []string{
				headerAccessControlMaxAge,
				headerAccessControlAllowHeaders,
				headerAccessControlAllowCredentials,
				headerAccessControlAllowPrivateNetwork,
			},
		},
		{
			name:    "request headers are not required to detect a preflight",
			handler: New(),
			headers: map[string]string{
				headerOrigin:                      "https://any.example",
				headerAccessControlRequestMethod:  moon.MethodPost,
				headerAccessControlRequestHeaders: "Content-Type, X-Custom",
			},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin:  "*",
				headerAccessControlAllowMethods: defaultAllowMethods,
				headerVary:                      preflightVary,
			},
			wantAbsent: []string{headerAccessControlMaxAge, headerAccessControlAllowHeaders},
		},
		{
			name: "allowed headers and max age",
			handler: New(NewOptions().
				WithAllowedHeaders([]string{"Content-Type", "Authorization"}).
				WithMaxAge(600)),
			headers: map[string]string{
				headerOrigin:                      "https://any.example",
				headerAccessControlRequestMethod:  moon.MethodPost,
				headerAccessControlRequestHeaders: "Content-Type",
			},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin:  "*",
				headerAccessControlAllowMethods: defaultAllowMethods,
				headerAccessControlAllowHeaders: "Content-Type, Authorization",
				headerAccessControlMaxAge:       "600",
				headerVary:                      preflightVary,
			},
		},
		{
			name:    "max age zero is written",
			handler: New(NewOptions().WithMaxAge(0)),
			headers: map[string]string{
				headerOrigin:                     "https://any.example",
				headerAccessControlRequestMethod: moon.MethodGet,
			},
			wantHeader: map[string]string{headerAccessControlMaxAge: "0"},
		},
		{
			name:    "negative max age is omitted",
			handler: New(NewOptions().WithMaxAge(-1)),
			headers: map[string]string{
				headerOrigin:                     "https://any.example",
				headerAccessControlRequestMethod: moon.MethodGet,
			},
			wantAbsent: []string{headerAccessControlMaxAge},
		},
		{
			name: "private network is answered when requested",
			handler: New(NewOptions().
				WithAllowPrivateNetwork(true)),
			headers: map[string]string{
				headerOrigin:                             "https://any.example",
				headerAccessControlRequestMethod:         moon.MethodPost,
				headerAccessControlRequestPrivateNetwork: "true",
			},
			wantHeader: map[string]string{headerAccessControlAllowPrivateNetwork: "true"},
		},
		{
			name:    "private network is absent when not requested",
			handler: New(NewOptions().WithAllowPrivateNetwork(true)),
			headers: map[string]string{
				headerOrigin:                     "https://any.example",
				headerAccessControlRequestMethod: moon.MethodPost,
			},
			wantAbsent: []string{headerAccessControlAllowPrivateNetwork},
		},
		{
			name:    "listed origin is echoed",
			handler: New(NewOptions().WithAllowedOrigins([]string{"https://app.example"})),
			headers: map[string]string{
				headerOrigin:                     "https://app.example",
				headerAccessControlRequestMethod: moon.MethodPut,
			},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin: "https://app.example",
				headerVary:                     preflightVary,
			},
		},
		{
			name:    "unlisted origin gets no allow origin",
			handler: New(NewOptions().WithAllowedOrigins([]string{"https://app.example"})),
			headers: map[string]string{
				headerOrigin:                     "https://evil.example",
				headerAccessControlRequestMethod: moon.MethodPost,
			},
			wantHeader: map[string]string{headerVary: preflightVary},
			wantAbsent: []string{
				headerAccessControlAllowOrigin,
				headerAccessControlAllowCredentials,
			},
		},
		{
			name: "credentials are advertised on the preflight",
			handler: New(NewOptions().
				WithAllowedOrigins([]string{"https://app.example"}).
				WithAllowCredentials(true)),
			headers: map[string]string{
				headerOrigin:                     "https://app.example",
				headerAccessControlRequestMethod: moon.MethodPost,
			},
			wantHeader: map[string]string{
				headerAccessControlAllowOrigin:      "https://app.example",
				headerAccessControlAllowCredentials: "true",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := request(newTestApp(tc.handler), http.MethodOptions, "/resource", tc.headers)
			require.Equal(t, http.StatusNoContent, resp.StatusCode, "preflight ends the chain")
			requireHeaders(t, resp, tc.wantHeader)
			requireAbsentHeaders(t, resp, tc.wantAbsent...)
		})
	}
}

func TestIsPreflightRequest(t *testing.T) {
	var got bool
	app := moon.New()
	app.Map(http.MethodOptions, "/probe", func(ctx *moon.Context) error {
		got = isPreflightRequest(ctx)
		return ctx.Write(http.StatusOK, "probed")
	})

	testCases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name: "origin and requested method",
			headers: map[string]string{
				headerOrigin:                     "https://app.example",
				headerAccessControlRequestMethod: moon.MethodPost,
			},
			want: true,
		},
		{
			name: "request headers are optional",
			headers: map[string]string{
				headerOrigin:                      "https://app.example",
				headerAccessControlRequestMethod:  moon.MethodPost,
				headerAccessControlRequestHeaders: "Content-Type",
			},
			want: true,
		},
		{
			name: "missing requested method",
			headers: map[string]string{
				headerOrigin: "https://app.example",
			},
			want: false,
		},
		{
			name: "missing origin",
			headers: map[string]string{
				headerAccessControlRequestMethod: moon.MethodPost,
			},
			want: false,
		},
		{
			name: "both request headers but no origin",
			headers: map[string]string{
				headerAccessControlRequestMethod:  moon.MethodPost,
				headerAccessControlRequestHeaders: "Content-Type",
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got = false
			resp := request(app, http.MethodOptions, "/probe", tc.headers)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, tc.want, got)
		})
	}

	t.Run("a non OPTIONS request is never a preflight", func(t *testing.T) {
		got = false
		resp := request(app, http.MethodGet, "/probe", map[string]string{
			headerOrigin:                      "https://app.example",
			headerAccessControlRequestMethod:  moon.MethodPost,
			headerAccessControlRequestHeaders: "Content-Type",
		})
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		require.False(t, got)
	})
}

func TestHandleNonPreflightOptionsIsNotShortCircuited(t *testing.T) {
	app := newTestApp(New())
	resp := request(app, http.MethodOptions, "/resource", map[string]string{
		headerOrigin: "https://app.example",
	})

	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	require.Equal(t, "*", resp.Header.Get(headerAccessControlAllowOrigin))
	requireAbsentHeaders(t, resp, headerAccessControlAllowMethods, headerAccessControlMaxAge)
}

func TestHandleGetWithPreflightHeadersIsNotAPreflight(t *testing.T) {
	app := newTestApp(New())
	resp := request(app, http.MethodGet, "/resource", map[string]string{
		headerOrigin:                      "https://app.example",
		headerAccessControlRequestMethod:  moon.MethodPost,
		headerAccessControlRequestHeaders: "Content-Type",
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "the chain must run")
	require.Equal(t, "*", resp.Header.Get(headerAccessControlAllowOrigin))
	requireAbsentHeaders(t, resp, headerAccessControlAllowMethods, headerAccessControlMaxAge)
}

func TestHandleSkip(t *testing.T) {
	handler := New(NewOptions().WithSkip(func(*moon.Context) bool { return true }))
	resp := request(newTestApp(handler), http.MethodGet, "/resource", map[string]string{
		headerOrigin: "https://app.example",
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "the chain must run")
	requireAbsentHeaders(t, resp,
		headerAccessControlAllowOrigin,
		headerAccessControlAllowCredentials,
		headerVary,
	)

	t.Run("preflight is skipped too", func(t *testing.T) {
		resp := request(newTestApp(handler), http.MethodOptions, "/resource", map[string]string{
			headerOrigin:                     "https://app.example",
			headerAccessControlRequestMethod: moon.MethodPost,
		})
		require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "the chain must run")
		requireAbsentHeaders(t, resp, headerAccessControlAllowMethods, headerAccessControlMaxAge)
	})
}
