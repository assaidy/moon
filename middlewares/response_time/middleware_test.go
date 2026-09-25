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

func TestNew(t *testing.T) {
	testCases := []struct {
		name        string
		optionFuncs []OptionFunc
		handler     moon.Handler
		header      string
		wantStatus  int
		wantPresent bool
	}{
		{
			name:        "default header",
			optionFuncs: nil,
			handler:     nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name:        "custom header",
			optionFuncs: []OptionFunc{WithHeader("X-Took")},
			handler:     nil,
			header:      "X-Took",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name: "skipped request omits header",
			optionFuncs: []OptionFunc{WithSkip(func(ctx *moon.Context) bool {
				return true
			})},
			handler:     nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: false,
		},
		{
			name: "non-skipped request keeps header",
			optionFuncs: []OptionFunc{WithSkip(func(ctx *moon.Context) bool {
				return false
			})},
			handler:     nil,
			header:      "X-Response-Time",
			wantStatus:  http.StatusOK,
			wantPresent: true,
		},
		{
			name:        "header set on error",
			optionFuncs: nil,
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

			app := moon.New()
			app.Use("/", New(tc.optionFuncs...))
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
