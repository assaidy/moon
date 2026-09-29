package helmet

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

func TestHandle(t *testing.T) {
	testCases := []struct {
		name       string
		handler    *Handler
		wantHeader map[string]string
		wantAbsent []string
	}{
		{
			name:    "defaults",
			handler: New(),
			wantHeader: map[string]string{
				"X-XSS-Protection":                  "0",
				"X-Content-Type-Options":            "nosniff",
				"X-Frame-Options":                   "SAMEORIGIN",
				"Referrer-Policy":                   "no-referrer",
				"Cross-Origin-Embedder-Policy":      "require-corp",
				"Cross-Origin-Opener-Policy":        "same-origin",
				"Cross-Origin-Resource-Policy":      "same-origin",
				"Origin-Agent-Cluster":              "?1",
				"X-DNS-Prefetch-Control":            "off",
				"X-Download-Options":                "noopen",
				"X-Permitted-Cross-Domain-Policies": "none",
			},
			wantAbsent: []string{
				"Strict-Transport-Security",
				"Content-Security-Policy",
				"Content-Security-Policy-Report-Only",
				"Permissions-Policy",
			},
		},
		{
			name: "custom values",
			handler: New().
				WithXssProtection("1; mode=block").
				WithContentTypeNoSniff("nosniff").
				WithXFrameOptions("DENY").
				WithHstsMaxAge(31536000).
				WithHstsIncludeSubdomains(true).
				WithHstsPreloadEnabled(true).
				WithContentSecurityPolicy("default-src 'self'").
				WithReferrerPolicy("same-origin").
				WithPermissionPolicy("camera=()").
				WithCrossOriginEmbedderPolicy("credentialless").
				WithCrossOriginOpenerPolicy("same-origin-allow-popups").
				WithCrossOriginResourcePolicy("cross-origin").
				WithOriginAgentCluster("?0").
				WithXDnsPrefetchControl("on").
				WithXDownloadOptions("noopen").
				WithXPermittedCrossDomainPolicies("master-only"),
			wantHeader: map[string]string{
				"X-XSS-Protection":                  "1; mode=block",
				"X-Content-Type-Options":            "nosniff",
				"X-Frame-Options":                   "DENY",
				"Content-Security-Policy":           "default-src 'self'",
				"Referrer-Policy":                   "same-origin",
				"Permissions-Policy":                "camera=()",
				"Cross-Origin-Embedder-Policy":      "credentialless",
				"Cross-Origin-Opener-Policy":        "same-origin-allow-popups",
				"Cross-Origin-Resource-Policy":      "cross-origin",
				"Origin-Agent-Cluster":              "?0",
				"X-DNS-Prefetch-Control":            "on",
				"X-Download-Options":                "noopen",
				"X-Permitted-Cross-Domain-Policies": "master-only",
			},
			wantAbsent: []string{
				"Content-Security-Policy-Report-Only",
			},
		},
		{
			name: "csp report only",
			handler: New().
				WithContentSecurityPolicy("default-src 'self'").
				WithCspReportOnly(true),
			wantHeader: map[string]string{
				"Content-Security-Policy-Report-Only": "default-src 'self'",
			},
			wantAbsent: []string{
				"Content-Security-Policy",
			},
		},
		{
			name: "disabled headers are omitted",
			handler: New().
				WithXssProtection("").
				WithXFrameOptions("").
				WithReferrerPolicy(""),
			wantHeader: map[string]string{
				"X-Content-Type-Options": "nosniff",
			},
			wantAbsent: []string{
				"X-XSS-Protection",
				"X-Frame-Options",
				"Referrer-Policy",
			},
		},
		{
			name: "skipped request omits headers",
			handler: New().WithSkip(func(ctx *moon.Context) bool {
				return true
			}),
			wantHeader: nil,
			wantAbsent: []string{
				"X-XSS-Protection",
				"X-Content-Type-Options",
				"X-Frame-Options",
				"Strict-Transport-Security",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := moon.New()
			app.Use("/*", tc.handler.Handle)
			app.Map(http.MethodGet, "/secure", func(ctx *moon.Context) error {
				return ctx.Write(http.StatusOK, "hello")
			})

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/secure", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)

			for header, want := range tc.wantHeader {
				require.Equal(t, want, resp.Header.Get(header), "header %s", header)
			}
			for _, header := range tc.wantAbsent {
				require.Empty(t, resp.Header.Values(header), "header %s", header)
			}
		})
	}
}

func TestHandleHsts(t *testing.T) {
	testCases := []struct {
		name    string
		handler *Handler
		want    []string
	}{
		{
			name:    "disabled by default",
			handler: New(),
			want:    nil,
		},
		{
			name:    "max age only",
			handler: New().WithHstsMaxAge(31536000).WithHstsIncludeSubdomains(false),
			want:    []string{"max-age=31536000"},
		},
		{
			name:    "include subdomains",
			handler: New().WithHstsMaxAge(31536000).WithHstsIncludeSubdomains(true),
			want:    []string{"max-age=31536000", "includeSubDomains"},
		},
		{
			name:    "include subdomains and preload",
			handler: New().WithHstsMaxAge(31536000).WithHstsIncludeSubdomains(true).WithHstsPreloadEnabled(true),
			want:    []string{"max-age=31536000", "includeSubDomains", "preload"},
		},
		{
			name:    "zero max age omits directives",
			handler: New().WithHstsMaxAge(0).WithHstsIncludeSubdomains(true).WithHstsPreloadEnabled(true),
			want:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := moon.New()
			app.Use("/*", tc.handler.Handle)
			app.Map(http.MethodGet, "/secure", func(ctx *moon.Context) error {
				return ctx.Write(http.StatusOK, "hello")
			})

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/secure", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, tc.want, resp.Header.Values("Strict-Transport-Security"))
		})
	}
}
