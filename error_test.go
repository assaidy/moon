package moon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestError_Error(t *testing.T) {
	t.Run("bare returns kind", func(t *testing.T) {
		require.Equal(t, "not_found", ErrNotFound.Error())
	})

	t.Run("with details includes details", func(t *testing.T) {
		err := ErrValidationFailed.WithDetails("field X")
		require.Equal(t, "validation_failed(field X)", err.Error())
	})
}

func TestError_Is(t *testing.T) {
	t.Run("same kind matches despite details", func(t *testing.T) {
		require.True(t, errors.Is(ErrNotFound, ErrNotFound))
		require.True(t, errors.Is(ErrNotFound.WithDetails("id 123"), ErrNotFound))
	})

	t.Run("different kind does not match", func(t *testing.T) {
		require.False(t, errors.Is(ErrNotFound, ErrBadRequest))
		require.False(t, errors.Is(errors.New("boom"), ErrNotFound))
	})

	t.Run("wrapped matches", func(t *testing.T) {
		wrapped := fmt.Errorf("handler failed: %w", ErrNotFound)
		require.True(t, errors.Is(wrapped, ErrNotFound))
		require.False(t, errors.Is(wrapped, ErrBadRequest))
	})
}

func TestError_WithDetailsCopy(t *testing.T) {
	orig := NewError(http.StatusNotFound, "not_found", "missing")
	with := orig.WithDetails("id 123")

	require.Nil(t, orig.Details)
	require.Equal(t, "id 123", with.Details)
}

func testErrorHandlerResponse(t *testing.T, handlerErr error) (int, map[string]any) {
	t.Helper()

	app := New()
	app.Handle(http.MethodGet, "/x", func(ctx *Context) error {
		return handlerErr
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	return resp.StatusCode, body
}

func TestDefaultErrorHandler(t *testing.T) {
	t.Run("non-internal preserves details", func(t *testing.T) {
		status, body := testErrorHandlerResponse(t, ErrNotFound.WithDetails("id 123"))

		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, "not_found", body["kind"])
		require.Equal(t, "id 123", body["details"])
	})

	t.Run("internal strips details", func(t *testing.T) {
		status, body := testErrorHandlerResponse(t, ErrInternalServerError.WithDetails("db password leak"))

		require.Equal(t, http.StatusInternalServerError, status)
		require.Equal(t, "internal_server_error", body["kind"])
		require.NotContains(t, body, "details")
	})

	t.Run("non-error falls back to generic 500", func(t *testing.T) {
		status, body := testErrorHandlerResponse(t, errors.New("boom"))

		require.Equal(t, http.StatusInternalServerError, status)
		require.Equal(t, "internal_server_error", body["kind"])
	})

	t.Run("wrapped error keeps status and details", func(t *testing.T) {
		status, body := testErrorHandlerResponse(t, fmt.Errorf("handler failed: %w", ErrForbidden.WithDetails("x")))

		require.Equal(t, http.StatusForbidden, status)
		require.Equal(t, "forbidden", body["kind"])
		require.Equal(t, "x", body["details"])
	})
}
