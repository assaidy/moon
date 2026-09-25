package skip

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	errWrapped := errors.New("boom")

	testCases := []struct {
		name        string
		skip        bool
		wantWrapped bool // whether the wrapped handler ran
		wantErr     error
	}{
		{
			name:        "predicate false runs wrapped handler",
			skip:        false,
			wantWrapped: true,
		},
		{
			name:        "predicate true bypasses wrapped handler",
			skip:        true,
			wantWrapped: false,
		},
		{
			name:        "wrapped handler error propagates",
			skip:        false,
			wantWrapped: true,
			wantErr:     errWrapped,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := moon.New()
			predicateRan := false
			wrappedRan := false

			wrapped := New(
				func(ctx *moon.Context) error {
					wrappedRan = true
					if tc.wantErr != nil {
						return tc.wantErr
					}
					return ctx.Write(http.StatusTeapot, "wrapped")
				},
				func(*moon.Context) bool {
					predicateRan = true
					return tc.skip
				},
			)

			app.Use("/", wrapped)
			app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
				return ctx.Write(http.StatusOK, "downstream")
			})

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/resource", nil))
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			require.True(t, predicateRan)
			require.Equal(t, tc.wantWrapped, wrappedRan)
			if tc.wantErr != nil {
				require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
				return
			}
			if tc.wantWrapped {
				require.Equal(t, http.StatusTeapot, resp.StatusCode)
				require.Equal(t, "wrapped", string(body))
			} else {
				// chain continued past the wrapped handler
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "downstream", string(body))
			}
		})
	}
}

func TestNew_NilHandlerPanics(t *testing.T) {
	require.Panics(t, func() {
		New(nil, func(ctx *moon.Context) bool { return false })
	})
}

func TestNew_NilPredicatePanics(t *testing.T) {
	require.Panics(t, func() {
		New(func(ctx *moon.Context) error { return nil }, nil)
	})
}
