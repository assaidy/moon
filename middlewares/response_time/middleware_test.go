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
		middleware  *Middleware
		handler     moon.Handler
		header      string
		wantStatus  int
		wantPresent bool
	}{
		{
			name:        "default header",
			middleware:  nil,
			handler:     nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name:        "custom header",
			middleware:  New().WithHeader("X-Took"),
			handler:     nil,
			header:      "X-Took",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name: "skipped request omits header",
			middleware: New().WithSkip(func(ctx *moon.Context) bool {
				return true
			}),
			handler:     nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: false,
		},
		{
			name: "non-skipped request keeps header",
			middleware: New().WithSkip(func(ctx *moon.Context) bool {
				return false
			}),
			handler:     nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name:       "header set on error",
			middleware: nil,
			handler: func(ctx *moon.Context) error {
				return errors.New("boom")
			},
			header:      "X-Response-Time",
			wantStatus:  http.StatusInternalServerError,
			wantPresent: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := tc.handler
			if handler == nil {
				handler = func(ctx *moon.Context) error {
					return ctx.Write(http.StatusOK, "hello")
				}
			}

			mw := tc.middleware
			if mw == nil {
				mw = New()
			}

			app := moon.New()
			app.Use("/", mw.Handle)
			app.Map(http.MethodGet, "/timed", handler)

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
