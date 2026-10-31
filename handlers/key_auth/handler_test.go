package key_auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

const (
	defaultAuthorizationChallenge = `Bearer realm="Restricted"`
	defaultApiKeyChallenge        = `ApiKey realm="Restricted"`
)

func allowAll(*moon.Context, string) (bool, error) {
	return true, nil
}

func newTestApp(h *Handler) (*moon.App, *bool) {
	next := new(bool)
	app := moon.New()
	app.UseAll(h.Handle)
	app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
		*next = true
		return ctx.Write(http.StatusOK, "ok")
	})
	return app, next
}

func doRequest(app *moon.App, set func(*http.Request)) *http.Response {
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	if set != nil {
		set(req)
	}
	return app.Test(req)
}

func TestNew(t *testing.T) {
	t.Run("validator is required", func(t *testing.T) {
		require.Panics(t, func() { New() })
	})

	t.Run("nil validator panics", func(t *testing.T) {
		require.Panics(t, func() { WithValidator(nil) })
	})

	t.Run("defaults", func(t *testing.T) {
		h := New(WithValidator(allowAll))

		require.NotNil(t, h.options.source.Extractor)
		require.Equal(t, "Bearer", h.options.source.Challenge.Scheme)
		require.Equal(t, "Restricted", h.options.source.Challenge.Realm)
		require.Nil(t, h.options.skip)
	})

	t.Run("custom key source is normalized", func(t *testing.T) {
		h := New(
			WithValidator(allowAll),
			WithKeySource(KeySource{
				Extractor: func(*moon.Context) string { return "" },
				Challenge: Challenge{
					Scheme: "  ",
					Realm:  "  My Realm  ",
					Error:  " invalid_token ",
				},
			}),
		)

		require.Equal(t, "ApiKey", h.options.source.Challenge.Scheme)
		require.Equal(t, "My Realm", h.options.source.Challenge.Realm)
		require.Equal(t, "invalid_token", h.options.source.Challenge.Error)
	})
}

func TestOptionValidation(t *testing.T) {
	require.Panics(t, func() { WithKeySource(KeySource{}) })
	require.Panics(t, func() {
		WithKeySource(KeySource{
			Extractor: func(*moon.Context) string { return "" },
			Challenge: Challenge{Scheme: "bad scheme"},
		})
	})
	require.Panics(t, func() {
		WithKeySource(KeySource{
			Extractor: func(*moon.Context) string { return "" },
			Challenge: Challenge{Realm: "bad\x00realm"},
		})
	})
	require.Panics(t, func() {
		WithKeySource(KeySource{
			Extractor: func(*moon.Context) string { return "" },
			Challenge: Challenge{Error: `bad"error`},
		})
	})
	require.Panics(t, func() {
		WithKeySource(KeySource{
			Extractor: func(*moon.Context) string { return "" },
			Challenge: Challenge{ErrorDescription: `bad\description`},
		})
	})
	require.Panics(t, func() {
		WithKeySource(KeySource{
			Extractor: func(*moon.Context) string { return "" },
			Challenge: Challenge{ErrorUrl: "/relative"},
		})
	})
	require.Panics(t, func() {
		WithKeySource(KeySource{
			Extractor: func(*moon.Context) string { return "" },
			Challenge: Challenge{Scope: "read  write"},
		})
	})
	require.Panics(t, func() { SourceCookie("") })
	require.Panics(t, func() { SourceCookie("   ") })

	require.NotPanics(t, func() {
		New(WithValidator(allowAll), WithSkip(nil))
	})
	require.NotPanics(t, func() {
		New(WithValidator(allowAll), WithKeySource(SourceCookie("session")))
	})
}

func TestHandleAuthorizationHeader(t *testing.T) {
	testCases := []struct {
		name          string
		authorization string
		wantStatus    int
		wantNext      bool
		wantChallenge string
		wantKey       string
	}{
		{
			name:          "missing header challenges",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultAuthorizationChallenge,
		},
		{
			name:          "wrong scheme challenges",
			authorization: "Basic secret",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultAuthorizationChallenge,
		},
		{
			name:          "bare scheme challenges",
			authorization: "Bearer",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultAuthorizationChallenge,
		},
		{
			name:          "empty token challenges",
			authorization: "Bearer   ",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultAuthorizationChallenge,
		},
		{
			name:          "extra fields challenge",
			authorization: "Bearer secret extra",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultAuthorizationChallenge,
		},
		{
			name:          "unknown key challenges",
			authorization: "Bearer wrong",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultAuthorizationChallenge,
			wantKey:       "wrong",
		},
		{
			name:          "valid key passes",
			authorization: "Bearer secret",
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantKey:       "secret",
		},
		{
			name:          "scheme is case insensitive",
			authorization: "bEaReR secret",
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantKey:       "secret",
		},
		{
			name:          "extra spaces are accepted",
			authorization: "Bearer    secret",
			wantStatus:    http.StatusOK,
			wantNext:      true,
			wantKey:       "secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var gotKey string
			h := New(WithValidator(func(_ *moon.Context, key string) (bool, error) {
				gotKey = key
				return key == "secret", nil
			}))
			app, next := newTestApp(h)

			resp := doRequest(app, func(req *http.Request) {
				if tc.authorization != "" {
					req.Header.Set("Authorization", tc.authorization)
				}
			})

			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Equal(t, tc.wantNext, *next)
			require.Equal(t, tc.wantChallenge, resp.Header.Get("WWW-Authenticate"))
			require.Equal(t, tc.wantKey, gotKey)
		})
	}
}

func TestHandleXApiKeyHeader(t *testing.T) {
	testCases := []struct {
		name          string
		key           string
		wantStatus    int
		wantNext      bool
		wantChallenge string
		wantKey       string
	}{
		{
			name:          "missing header challenges",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultApiKeyChallenge,
		},
		{
			name:          "unknown key challenges",
			key:           "wrong",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultApiKeyChallenge,
			wantKey:       "wrong",
		},
		{
			name:       "valid key passes",
			key:        "secret",
			wantStatus: http.StatusOK,
			wantNext:   true,
			wantKey:    "secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var gotKey string
			h := New(
				WithKeySource(SourceXApiKeyHeader()),
				WithValidator(func(_ *moon.Context, key string) (bool, error) {
					gotKey = key
					return key == "secret", nil
				}),
			)
			app, next := newTestApp(h)

			resp := doRequest(app, func(req *http.Request) {
				if tc.key != "" {
					req.Header.Set("X-API-Key", tc.key)
				}
			})

			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Equal(t, tc.wantNext, *next)
			require.Equal(t, tc.wantChallenge, resp.Header.Get("WWW-Authenticate"))
			require.Equal(t, tc.wantKey, gotKey)
		})
	}
}

func TestHandleCookie(t *testing.T) {
	testCases := []struct {
		name          string
		cookies       []*http.Cookie
		wantStatus    int
		wantNext      bool
		wantChallenge string
		wantKey       string
	}{
		{
			name:          "missing cookie challenges",
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultApiKeyChallenge,
		},
		{
			name:          "different cookie is ignored",
			cookies:       []*http.Cookie{{Name: "other", Value: "secret"}},
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultApiKeyChallenge,
		},
		{
			name:          "unknown key challenges",
			cookies:       []*http.Cookie{{Name: "session", Value: "wrong"}},
			wantStatus:    http.StatusUnauthorized,
			wantChallenge: defaultApiKeyChallenge,
			wantKey:       "wrong",
		},
		{
			name:       "valid key passes",
			cookies:    []*http.Cookie{{Name: "session", Value: "secret"}},
			wantStatus: http.StatusOK,
			wantNext:   true,
			wantKey:    "secret",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var gotKey string
			h := New(
				WithKeySource(SourceCookie("session")),
				WithValidator(func(_ *moon.Context, key string) (bool, error) {
					gotKey = key
					return key == "secret", nil
				}),
			)
			app, next := newTestApp(h)

			resp := doRequest(app, func(req *http.Request) {
				for _, cookie := range tc.cookies {
					req.AddCookie(cookie)
				}
			})

			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Equal(t, tc.wantNext, *next)
			require.Equal(t, tc.wantChallenge, resp.Header.Get("WWW-Authenticate"))
			require.Equal(t, tc.wantKey, gotKey)
		})
	}
}

func TestHandleCustomSourceAndChallenge(t *testing.T) {
	const fullChallenge = `Bearer realm="say \"hi\" \\ bye", error="invalid_token", error_description="expired", error_uri="https://example.com/errors/invalid-token", scope="read write"`

	h := New(
		WithKeySource(KeySource{
			Extractor: func(ctx *moon.Context) string {
				return ctx.GetHeader("X-Custom-Key")
			},
			Challenge: Challenge{
				Scheme:           " Bearer ",
				Realm:            `say "hi" \ bye`,
				Error:            " invalid_token ",
				ErrorDescription: "expired",
				ErrorUrl:         "https://example.com/errors/invalid-token",
				Scope:            "read write",
			},
		}),
		WithValidator(func(ctx *moon.Context, key string) (bool, error) {
			return key == "secret" && ctx.GetHeader("X-Probe") == "probe", nil
		}),
	)
	app, next := newTestApp(h)

	resp := doRequest(app, func(req *http.Request) {
		req.Header.Set("X-Custom-Key", "secret")
		req.Header.Set("X-Probe", "probe")
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, *next)
	require.Empty(t, resp.Header.Get("WWW-Authenticate"))

	*next = false
	resp = doRequest(app, nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.False(t, *next)
	require.Equal(t, fullChallenge, resp.Header.Get("WWW-Authenticate"))
}

func TestHandleSkip(t *testing.T) {
	t.Run("skip bypasses authentication", func(t *testing.T) {
		called := false
		h := New(
			WithSkip(func(*moon.Context) bool { return true }),
			WithValidator(func(*moon.Context, string) (bool, error) {
				called = true
				return false, nil
			}),
		)
		app, next := newTestApp(h)

		resp := doRequest(app, nil)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, *next)
		require.False(t, called)
	})

	t.Run("skip false still enforces authentication", func(t *testing.T) {
		h := New(
			WithSkip(func(*moon.Context) bool { return false }),
			WithValidator(allowAll),
		)
		app, next := newTestApp(h)

		resp := doRequest(app, nil)

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		require.False(t, *next)
		require.Equal(t, defaultAuthorizationChallenge, resp.Header.Get("WWW-Authenticate"))
	})
}

func TestHandleValidatorError(t *testing.T) {
	h := New(WithValidator(func(*moon.Context, string) (bool, error) {
		return false, errors.New("key store unavailable")
	}))
	app, next := newTestApp(h)

	resp := doRequest(app, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer secret")
	})

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.False(t, *next)
	require.Empty(t, resp.Header.Get("WWW-Authenticate"))
}

func TestHandleDownstreamError(t *testing.T) {
	h := New(WithValidator(allowAll))

	next := false
	app := moon.New()
	app.UseAll(h.Handle)
	app.Map(http.MethodGet, "/resource", func(*moon.Context) error {
		next = true
		return moon.ErrTeapot
	})

	resp := doRequest(app, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer secret")
	})

	require.Equal(t, http.StatusTeapot, resp.StatusCode)
	require.True(t, next)
}

func TestNormalizeChallenge(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c := normalizeChallenge(Challenge{})

		require.Equal(t, "ApiKey", c.Scheme)
		require.Equal(t, "Restricted", c.Realm)
		require.Empty(t, c.Error)
		require.Empty(t, c.ErrorDescription)
		require.Empty(t, c.ErrorUrl)
		require.Empty(t, c.Scope)
	})

	t.Run("trims every field", func(t *testing.T) {
		c := normalizeChallenge(Challenge{
			Scheme:           " Bearer ",
			Realm:            "  My Realm  ",
			Error:            " invalid_token ",
			ErrorDescription: " expired ",
			ErrorUrl:         " https://example.com/errors/invalid-token ",
			Scope:            " read write ",
		})

		require.Equal(t, "Bearer", c.Scheme)
		require.Equal(t, "My Realm", c.Realm)
		require.Equal(t, "invalid_token", c.Error)
		require.Equal(t, "expired", c.ErrorDescription)
		require.Equal(t, "https://example.com/errors/invalid-token", c.ErrorUrl)
		require.Equal(t, "read write", c.Scope)
	})
}

func TestChallengeGenerate(t *testing.T) {
	t.Run("all parameters", func(t *testing.T) {
		c := normalizeChallenge(Challenge{
			Scheme:           "Bearer",
			Realm:            `say "hi" \ bye`,
			Error:            "invalid_token",
			ErrorDescription: "The access token expired",
			ErrorUrl:         "https://example.com/errors/invalid-token",
			Scope:            "read write",
		})

		require.Equal(t,
			`Bearer realm="say \"hi\" \\ bye", error="invalid_token", error_description="The access token expired", error_uri="https://example.com/errors/invalid-token", scope="read write"`,
			c.generate(),
		)
	})

	t.Run("omits empty optional parameters", func(t *testing.T) {
		c := normalizeChallenge(Challenge{})

		require.Equal(t, `ApiKey realm="Restricted"`, c.generate())
	})
}

func TestExtractAuthorization(t *testing.T) {
	testCases := []struct {
		name   string
		header string
		scheme string
		want   string
	}{
		{name: "exact scheme", header: "Bearer secret", scheme: "Bearer", want: "secret"},
		{name: "case insensitive scheme", header: "bEaReR secret", scheme: "Bearer", want: "secret"},
		{name: "extra spaces", header: "Bearer    secret", scheme: "Bearer", want: "secret"},
		{name: "wrong scheme", header: "ApiKey secret", scheme: "Bearer", want: ""},
		{name: "missing token", header: "Bearer", scheme: "Bearer", want: ""},
		{name: "extra fields", header: "Bearer secret extra", scheme: "Bearer", want: ""},
		{name: "empty header", header: "", scheme: "Bearer", want: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, extractAuthorization(tc.header, tc.scheme))
		})
	}
}

func TestSourceConstructors(t *testing.T) {
	t.Run("authorization header uses the given scheme", func(t *testing.T) {
		source := SourceAuthorizationHeader("Bearer")

		require.Equal(t, "Bearer", source.Challenge.Scheme)
		require.Equal(t, "Restricted", source.Challenge.Realm)
		require.NotNil(t, source.Extractor)
	})

	t.Run("authorization header scheme defaults to ApiKey", func(t *testing.T) {
		source := SourceAuthorizationHeader("")

		require.Equal(t, "ApiKey", source.Challenge.Scheme)
	})

	t.Run("x-api-key header", func(t *testing.T) {
		source := SourceXApiKeyHeader()

		require.Equal(t, "ApiKey", source.Challenge.Scheme)
		require.Equal(t, "Restricted", source.Challenge.Realm)
		require.NotNil(t, source.Extractor)
	})

	t.Run("cookie", func(t *testing.T) {
		source := SourceCookie("  session  ")

		require.Equal(t, "ApiKey", source.Challenge.Scheme)
		require.Equal(t, "Restricted", source.Challenge.Realm)
		require.NotNil(t, source.Extractor)
	})
}

func TestQuoteString(t *testing.T) {
	require.Equal(t, `"plain"`, quoteString("plain"))
	require.Equal(t, `"a\"b\\c"`, quoteString(`a"b\c`))
}
