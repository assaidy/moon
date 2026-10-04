package request_id

import (
	"bufio"
	"context"
	"log/slog"
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
		handler      *Handler
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
			handler:      New(NewOptions().WithGenerator(func() string { return "  xyz  " })),
			header:       "X-Request-ID",
			wantResponse: "xyz",
		},
		{
			name:         "custom generator",
			handler:      New(NewOptions().WithGenerator(func() string { return "fixed-id" })),
			header:       "X-Request-ID",
			wantResponse: "fixed-id",
		},
		{
			name:       "falls back after invalid generator",
			handler:    New(NewOptions().WithGenerator(func() string { return "\x01" })),
			header:     "X-Request-ID",
			wantGenLen: 43,
		},
		{
			name:         "custom header",
			handler:      New(NewOptions().WithHeader("X-Correlation-ID")),
			incoming:     "corr-1",
			sendHeader:   true,
			header:       "X-Correlation-ID",
			wantResponse: "corr-1",
		},
		{
			name: "skipped request sets nothing",
			handler: New(NewOptions().WithSkip(func(ctx *moon.Context) bool {
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
			app.Use("/*", h.Handle)
			app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
				fromCtx = GetFromContext(ctx)
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

// The handler does not trim the incoming header itself: the HTTP server
// strips edge whitespace while parsing. Pinned here so the premise in [New]
// stays true.
func TestIncomingHeaderArrivesTrimmed(t *testing.T) {
	raw := "GET /resource HTTP/1.1\r\nHost: example.com\r\nX-Request-ID:   abc  \r\n\r\n"
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	require.NoError(t, err)
	require.Equal(t, "abc", req.Header.Get("X-Request-ID"))
}

type captureLogHandler struct {
	records []slog.Record
}

func (h *captureLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureLogHandler) WithGroup(string) slog.Handler      { return h }

func TestGetRequestLoggingEntry(t *testing.T) {
	entry := New().GetRequestLoggingEntry()
	require.Equal(t, "request_id", entry.Key)
	require.NotNil(t, entry.Value)

	custom := New(NewOptions().WithRequestLoggingEntryKey("correlation_id").
		WithRequestLoggingEntryValueFunc(func(ctx *moon.Context, err error) any { return "v" })).
		GetRequestLoggingEntry()
	require.Equal(t, "correlation_id", custom.Key)
	require.NotNil(t, custom.Value)
}

func TestLogsRequestIdEntry(t *testing.T) {
	logs := &captureLogHandler{}
	app := moon.New(moon.NewAppOptions().WithLogger(slog.New(logs)).WithRequestLogging(true))
	h := New()
	app.AddRequestLoggingEntry(h.GetRequestLoggingEntry())
	app.Use("/*", h.Handle)
	app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
		return ctx.Write(http.StatusOK, "hello")
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/resource", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	id := resp.Header.Get("X-Request-ID")
	require.NotEmpty(t, id)

	require.Len(t, logs.records, 1)
	var logged string
	found := false
	logs.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "request_id" {
			logged, found = a.Value.Any().(string), true
			return false
		}
		return true
	})
	require.True(t, found, "expected request id log entry")
	require.Equal(t, id, logged)
}

func TestLogsCustomRequestIdEntry(t *testing.T) {
	logs := &captureLogHandler{}
	app := moon.New(moon.NewAppOptions().WithLogger(slog.New(logs)).WithRequestLogging(true))
	h := New(NewOptions().
		WithRequestLoggingEntryKey("correlation_id").
		WithRequestLoggingEntryValueFunc(func(ctx *moon.Context, err error) any {
			return "custom-" + GetFromContext(ctx)
		}))
	app.AddRequestLoggingEntry(h.GetRequestLoggingEntry())
	app.Use("/*", h.Handle)
	app.Map(http.MethodGet, "/resource", func(ctx *moon.Context) error {
		return ctx.Write(http.StatusOK, "hello")
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/resource", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	id := resp.Header.Get("X-Request-ID")
	require.NotEmpty(t, id)

	require.Len(t, logs.records, 1)
	var logged any
	found := false
	logs.records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "correlation_id" {
			logged, found = a.Value.Any(), true
			return false
		}
		return true
	})
	require.True(t, found, "expected correlation id log entry")
	require.Equal(t, "custom-"+id, logged)
}
