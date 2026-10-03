package response_time

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

func TestHandle(t *testing.T) {
	testCases := []struct {
		name        string
		handler     *Handler
		route       moon.Handler
		header      string
		wantStatus  int
		wantPresent bool
	}{
		{
			name:        "default header",
			handler:     nil,
			route:       nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name:        "custom header",
			handler:     New(NewOptions().WithHeader("X-Took")),
			route:       nil,
			header:      "X-Took",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name: "skipped request omits header",
			handler: New(NewOptions().WithSkip(func(ctx *moon.Context) bool {
				return true
			})),
			route:       nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: false,
		},
		{
			name: "non-skipped request keeps header",
			handler: New(NewOptions().WithSkip(func(ctx *moon.Context) bool {
				return false
			})),
			route:       nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name:    "header set on error",
			handler: nil,
			route: func(ctx *moon.Context) error {
				return errors.New("boom")
			},
			header:      "X-Response-Time",
			wantStatus:  http.StatusInternalServerError,
			wantPresent: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			route := tc.route
			if route == nil {
				route = func(ctx *moon.Context) error {
					return ctx.Write(http.StatusOK, "hello")
				}
			}

			h := tc.handler
			if h == nil {
				h = New()
			}

			app := moon.New()
			app.Use("/*", h.Handle)
			app.Map(http.MethodGet, "/timed", route)

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/timed", nil))
			require.Equal(t, tc.wantStatus, resp.StatusCode)

			value := resp.Header.Get(tc.header)
			if !tc.wantPresent {
				require.Empty(t, value)
				return
			}
			require.NotEmpty(t, value)
			duration, err := time.ParseDuration(value)
			require.NoError(t, err)
			require.GreaterOrEqual(t, duration, time.Duration(0))

			if tc.wantStatus == http.StatusOK {
				raw, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, "hello", string(raw))
			}
		})
	}
}
