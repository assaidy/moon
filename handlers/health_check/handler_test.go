package health_check

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

func TestHandle_Defaults(t *testing.T) {
	testCases := []struct {
		name       string
		probeOk    bool
		wantStatus int
	}{
		{"probe ok gives 200", true, http.StatusOK},
		{"probe fails gives 503", false, http.StatusServiceUnavailable},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := moon.New()
			app.Map(http.MethodGet, "/healthz", New(NewOptions().WithProbe(func(*moon.Context) bool {
				return tc.probeOk
			})).Handle)

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
			require.Equal(t, tc.wantStatus, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Empty(t, body)
		})
	}
}

func TestHandle_DefaultProbeReportsOk(t *testing.T) {
	app := moon.New()
	app.Map(http.MethodGet, "/healthz", New().Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandle_ProbeReceivesContext(t *testing.T) {
	app := moon.New()
	var gotPath string
	app.Map(http.MethodGet, "/readyz", New(NewOptions().WithProbe(func(ctx *moon.Context) bool {
		gotPath = ctx.GetPath()
		return true
	})).Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "/readyz", gotPath)
}

func TestHandle_CustomResponse(t *testing.T) {
	app := moon.New()
	app.Map(http.MethodGet, "/healthz", New(NewOptions().
		WithProbe(func(*moon.Context) bool { return false }).
		WithResponse(func(ctx *moon.Context, ok bool) error {
			if ok {
				return ctx.Write(http.StatusOK, "up")
			}
			return ctx.Write(http.StatusServiceUnavailable, "down")
		})).Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "down", string(body))
}

// The probe result flows into the response func.
func TestHandle_ResponseReceivesProbeResult(t *testing.T) {
	testCases := []struct {
		name    string
		probeOk bool
		want    string
	}{
		{"ok reaches response", true, "true"},
		{"failure reaches response", false, "false"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := moon.New()
			app.Map(http.MethodGet, "/healthz", New(NewOptions().
				WithProbe(func(*moon.Context) bool { return tc.probeOk }).
				WithResponse(func(ctx *moon.Context, ok bool) error {
					return ctx.Write(http.StatusOK, map[bool]string{true: "true", false: "false"}[ok])
				})).Handle)

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(body))
		})
	}
}

// Terminal: the handler never calls Next, so a following handler in the
// chain must not run.
func TestHandle_DoesNotContinueChain(t *testing.T) {
	app := moon.New()
	downstreamRan := false
	app.Map(http.MethodGet, "/healthz",
		New().Handle,
		func(ctx *moon.Context) error {
			downstreamRan = true
			return ctx.Write(http.StatusOK, "downstream")
		},
	)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.False(t, downstreamRan)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Empty(t, body)
}

func TestWithProbe_NilPanics(t *testing.T) {
	require.Panics(t, func() { New(NewOptions().WithProbe(nil)) })
}

func TestWithResponse_NilPanics(t *testing.T) {
	require.Panics(t, func() { New(NewOptions().WithResponse(nil)) })
}

func TestEndpoints(t *testing.T) {
	require.Equal(t, "/livez", LivenessEndpoint)
	require.Equal(t, "/readyz", ReadinessEndpoint)
	require.Equal(t, "/startupz", StartupEndpoint)
}
