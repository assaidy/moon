package request_id

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

func TestHandle(t *testing.T) {
	testCases := []struct {
		name         string
		handler      moon.Handler
		incoming     string
		sendHeader   bool
		header       string
		wantResponse string
		wantGenLen   int
		wantSkipped  bool
	}{
		{
			name:         "reuses valid incoming",
			handler:      nil,
			incoming:     "abc-123",
			sendHeader:   true,
			header:       "X-Request-ID",
			wantResponse: "abc-123",
		},
		{
			name:         "accepts inside spaces",
			handler:      nil,
			incoming:     "ab cd",
			sendHeader:   true,
			header:       "X-Request-ID",
			wantResponse: "ab cd",
		},
		{
			name:       "generates when missing",
			header:     "X-Request-ID",
			wantGenLen: 43,
		},
		{
			name:       "generates when incoming empty",
			sendHeader: true,
			header:     "X-Request-ID",
			wantGenLen: 43,
		},
		{
			name:       "generates when incoming invalid",
			handler:    nil,
			incoming:   "a\x7fb",
			sendHeader: true,
			header:     "X-Request-ID",
			wantGenLen: 43,
		},
		{
			name:         "trims generated value",
			handler:      New(WithGenerator(func() string { return "  xyz  " })),
			header:       "X-Request-ID",
			wantResponse: "xyz",
		},
		{
			name:         "custom generator",
			handler:      New(WithGenerator(func() string { return "fixed-id" })),
			header:       "X-Request-ID",
			wantResponse: "fixed-id",
		},
		{
			name:       "falls back after invalid generator",
			handler:    New(WithGenerator(func() string { return "\x01" })),
			header:     "X-Request-ID",
			wantGenLen: 43,
		},
		{
			name:         "custom header",
			handler:      New(WithHeader("X-Correlation-ID")),
			incoming:     "corr-1",
			sendHeader:   true,
			header:       "X-Correlation-ID",
			wantResponse: "corr-1",
		},
		{
			name: "skipped request sets nothing",
			handler: New(WithSkip(func(ctx *moon.Context) bool {
				return true
			})),
			incoming:    "abc-123",
			sendHeader:  true,
			header:      "X-Request-ID",
			wantSkipped: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var fromCtx string

			h := tc.handler
			if h == nil {
				h = New()
			}

			app := moon.New()
			app.Use("/*", h)
			app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
				fromCtx, _ = ctx.GetLocal[string](DefaultLocalKey)
				return ctx.Write(http.StatusOK, "hello")
			})

			req := httptest.NewRequest(http.MethodGet, "/resource", nil)
			if tc.sendHeader {
				req.Header.Set(tc.header, tc.incoming)
			}
			resp := app.Test(req)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			if tc.wantSkipped {
				require.Empty(t, resp.Header.Get(tc.header))
				require.Empty(t, fromCtx)
				return
			}

			got := resp.Header.Get(tc.header)
			if tc.wantGenLen > 0 {
				require.Len(t, got, tc.wantGenLen)
				if tc.sendHeader {
					require.NotEqual(t, tc.incoming, got)
				}
			} else {
				require.Equal(t, tc.wantResponse, got)
			}
			require.Equal(t, got, fromCtx)
		})
	}
}

func TestOptionValidation(t *testing.T) {
	require.Panics(t, func() { WithHeader(" ") })
	require.Panics(t, func() { WithGenerator(nil) })
	require.Panics(t, func() { WithLocalKey("") })
}

func TestWithHeaderCanonicalizes(t *testing.T) {
	app := moon.New()
	app.Use("/*", New(WithHeader(" x-correlation-id ")))
	app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
		return ctx.WriteStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set("x-correlation-id", "abc")
	resp := app.Test(req)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "abc", resp.Header.Get("X-Correlation-Id"))
}

func TestWithLocalKey(t *testing.T) {
	var customId string

	app := moon.New()
	h := New(WithLocalKey("  custom.request_id  "))
	app.Use("/*", h)
	app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
		id, ok := ctx.GetLocal[string]("  custom.request_id  ")
		require.True(t, ok)
		require.NotEmpty(t, id)
		customId = id
		return ctx.Write(http.StatusOK, "hello")
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/resource", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEmpty(t, customId)
	require.Equal(t, customId, resp.Header.Get("X-Request-ID"))
	require.Panics(t, func() { WithLocalKey("") })
}

// The handler does not trim the incoming header itself: the HTTP server
// strips edge whitespace while parsing. Pinned here so the premise in [New]
// stays true.
func TestIncomingHeaderArrivesTrimmed(t *testing.T) {
	raw := "GET /resource HTTP/1.1\r\nHost: example.com\r\nX-Request-ID:   abc  \r\n\r\n"
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	require.NoError(t, err)
	require.Equal(t, "abc", req.Header.Get("X-Request-ID"))
}
