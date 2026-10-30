package basic_auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

const defaultChallenge = `Basic realm="Restricted", charset="UTF-8"`

func encodeCredentials(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

func basicAuthorization(user, password string) string {
	return "Basic " + encodeCredentials(user, password)
}

func rawBasicAuthorization(userPass []byte) string {
	return "Basic " + base64.StdEncoding.EncodeToString(userPass)
}

func allowAll(*moon.Context, string, string) (bool, error) {
	return true, nil
}

func TestHandle(t *testing.T) {
	validAuth := basicAuthorization("alice", "secret")
	longAuth := basicAuthorization("alice", strings.Repeat("x", 2048))

	testCases := []struct {
		name         string
		handler      *Handler
		headers      []string
		probe        string
		routeErr     error
		wantStatus   int
		wantNext     bool
		wantLocal    string
		wantLocalSet bool
		localKey     string
		wantRealm    string
	}{
		{
			name:       "missing header challenges",
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "empty header challenges",
			headers:    []string{""},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:         "valid credentials pass",
			headers:      []string{validAuth},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name: "wrong password challenges",
			handler: New(WithAuthorizer(func(_ *moon.Context, user, password string) (bool, error) {
				return user == "alice" && password == "secret", nil
			})),
			headers:    []string{basicAuthorization("alice", "wrong")},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name: "unknown user challenges",
			handler: New(WithAuthorizer(func(_ *moon.Context, user, _ string) (bool, error) {
				return user == "alice", nil
			})),
			headers:    []string{basicAuthorization("bob", "secret")},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name: "authorizer failure is internal error",
			handler: New(WithAuthorizer(func(*moon.Context, string, string) (bool, error) {
				return false, errors.New("user store unavailable")
			})),
			headers:    []string{validAuth},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:         "empty user and password accepted by authorizer",
			headers:      []string{basicAuthorization("", "")},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocalSet: true,
		},
		{
			name: "password may contain colons",
			handler: New(WithAuthorizer(func(_ *moon.Context, _, password string) (bool, error) {
				return password == "pa:ss", nil
			})),
			headers:      []string{basicAuthorization("alice", "pa:ss")},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name:         "utf8 credentials pass through",
			headers:      []string{basicAuthorization("tëst", "123£")},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "tëst",
			wantLocalSet: true,
		},
		{
			name:       "other scheme challenges",
			headers:    []string{"Bearer abc"},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:         "scheme is case insensitive",
			headers:      []string{"basic " + encodeCredentials("alice", "secret")},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name:         "mixed case scheme is accepted",
			headers:      []string{"bAsIc " + encodeCredentials("alice", "secret")},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name:       "bare scheme is partial credentials",
			headers:    []string{"Basic"},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "scheme followed by spaces is partial credentials",
			headers:    []string{"Basic   "},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "invalid token68 is bad request",
			headers:    []string{"Basic !!!"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "undecodable token challenges",
			headers:    []string{"Basic a"},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "token without colon challenges",
			headers:    []string{"Basic " + base64.StdEncoding.EncodeToString([]byte("alice"))},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:         "unpadded base64 is accepted",
			headers:      []string{"Basic " + strings.TrimRight(encodeCredentials("alice", "pa"), "=")},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name:       "multiple authorization fields are rejected",
			headers:    []string{validAuth, validAuth},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "header limit exceeded is 431",
			handler: New(
				WithAuthorizer(func(*moon.Context, string, string) (bool, error) {
					return false, errors.New("authorizer must not run")
				}),
				WithHeaderLimit(10),
			),
			headers:    []string{validAuth},
			wantStatus: http.StatusRequestHeaderFieldsTooLarge,
		},
		{
			name:         "header at limit passes",
			handler:      New(WithAuthorizer(allowAll), WithHeaderLimit(len(validAuth))),
			headers:      []string{validAuth},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name:         "negative limit disables cap",
			handler:      New(WithAuthorizer(allowAll), WithHeaderLimit(-1)),
			headers:      []string{longAuth},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name: "skip bypasses authentication",
			handler: New(
				WithSkip(func(*moon.Context) bool { return true }),
				WithAuthorizer(func(*moon.Context, string, string) (bool, error) {
					return false, errors.New("authorizer must not run")
				}),
			),
			wantStatus: http.StatusOK,
			wantNext:   true,
		},
		{
			name: "skip false still enforces authentication",
			handler: New(
				WithSkip(func(*moon.Context) bool { return false }),
				WithAuthorizer(allowAll),
			),
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "realm is quoted and escaped",
			handler:    New(WithRealm(`say "hi" \ bye`), WithAuthorizer(allowAll)),
			wantStatus: http.StatusUnauthorized,
			wantRealm:  `Basic realm="say \"hi\" \\ bye", charset="UTF-8"`,
		},
		{
			name:         "custom local key stores user",
			handler:      New(WithLocalKey("custom.user"), WithAuthorizer(allowAll)),
			headers:      []string{validAuth},
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
			localKey:     "custom.user",
		},
		{
			name:       "invalid utf8 user challenges",
			headers:    []string{rawBasicAuthorization([]byte{0xff, ':', 's', 'e', 'c', 'r', 'e', 't'})},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "invalid utf8 password challenges",
			headers:    []string{rawBasicAuthorization(append([]byte("alice:"), 0xff))},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "control character in user challenges",
			headers:    []string{basicAuthorization("ali\x00ce", "secret")},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name:       "control character in password challenges",
			headers:    []string{basicAuthorization("alice", "sec\tret")},
			wantStatus: http.StatusUnauthorized,
			wantRealm:  defaultChallenge,
		},
		{
			name: "authorizer receives request context",
			handler: New(WithAuthorizer(func(ctx *moon.Context, _, _ string) (bool, error) {
				return ctx.GetHeader("X-Probe") == "probe-value", nil
			})),
			headers:      []string{validAuth},
			probe:        "probe-value",
			wantStatus:   http.StatusOK,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
		{
			name:         "downstream error propagates",
			headers:      []string{validAuth},
			routeErr:     moon.ErrTeapot,
			wantStatus:   http.StatusTeapot,
			wantNext:     true,
			wantLocal:    "alice",
			wantLocalSet: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.handler
			if h == nil {
				h = New(WithAuthorizer(allowAll))
			}

			key := tc.localKey
			if key == "" {
				key = DefaultLocalKey
			}

			var (
				nextRan  bool
				local    string
				localSet bool
			)

			app := moon.New()
			app.Use("/*", h.Handle)
			app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
				nextRan = true
				local, localSet = ctx.GetLocal[string](key)
				if tc.routeErr != nil {
					return tc.routeErr
				}
				return ctx.Write(http.StatusOK, "ok")
			})

			req := httptest.NewRequest(http.MethodGet, "/resource", nil)
			for _, v := range tc.headers {
				req.Header.Add("Authorization", v)
			}
			if tc.probe != "" {
				req.Header.Set("X-Probe", tc.probe)
			}

			resp := app.Test(req)

			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Equal(t, tc.wantNext, nextRan)
			require.Equal(t, tc.wantLocalSet, localSet)
			require.Equal(t, tc.wantLocal, local)
			require.Equal(t, tc.wantRealm, resp.Header.Get("WWW-Authenticate"))

			if tc.wantStatus == http.StatusUnauthorized {
				require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
				require.Contains(t, resp.Header.Values("Vary"), "Authorization")
			}
		})
	}
}

func TestOptionValidation(t *testing.T) {
	require.Panics(t, func() { New() })
	require.Panics(t, func() { WithAuthorizer(nil) })
	require.Panics(t, func() { WithHeaderLimit(0) })
	require.Panics(t, func() { WithRealm("") })
	require.Panics(t, func() { WithRealm("   ") })
	require.Panics(t, func() { WithRealm("bad\x00realm") })
	require.Panics(t, func() { WithRealm("bad\x7frealm") })
	require.Panics(t, func() { WithRealm("bad\nrealm") })
	require.Panics(t, func() { WithLocalKey("") })

	require.NotPanics(t, func() { New(WithAuthorizer(allowAll), WithSkip(nil)) })
	require.NotPanics(t, func() { New(WithAuthorizer(allowAll), WithHeaderLimit(-1)) })
	require.NotPanics(t, func() { New(WithAuthorizer(allowAll), WithRealm("réalm")) })
}

func TestDefaults(t *testing.T) {
	h := New(WithAuthorizer(allowAll))

	require.Equal(t, "Restricted", h.options.realm)
	require.Equal(t, DefaultLocalKey, h.options.localKey)
	require.Nil(t, h.options.skip)
	require.Equal(t, -1, h.options.headerLimit, "no limit by default")
}

func TestWithRealmTrims(t *testing.T) {
	h := New(WithAuthorizer(allowAll), WithRealm("  My Realm  "))
	require.Equal(t, "My Realm", h.options.realm)
}

func TestQuoteString(t *testing.T) {
	require.Equal(t, `"plain"`, quoteString("plain"))
	require.Equal(t, `"a\"b\\c"`, quoteString(`a"b\c`))
}
