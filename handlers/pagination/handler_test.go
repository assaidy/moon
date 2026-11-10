package pagination

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

type testCursor struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// runHandler dispatches target through a handler registered as middleware and
// returns the response, the Info stored under localKey, and whether the route
// after the middleware ran.
func runHandler(t *testing.T, h moon.Handler, target, localKey string) (*http.Response, *Info, bool) {
	t.Helper()

	var info *Info
	nextRan := false

	app := moon.New()
	app.Use("/*", h)
	app.Map(http.MethodGet, "/items", func(ctx *moon.Context) error {
		nextRan = true
		if v, ok := ctx.GetLocal[*Info](localKey); ok {
			info = v
		}
		return ctx.Write(http.StatusOK, "ok")
	})

	resp := app.Test(httptest.NewRequest(http.MethodGet, target, nil))
	return resp, info, nextRan
}

// readError reads the JSON error body written by the default error handler.
func readError(t *testing.T, resp *http.Response) (string, any) {
	t.Helper()
	defer resp.Body.Close()

	var body struct {
		Kind    string `json:"kind"`
		Details any    `json:"details"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body.Kind, body.Details
}

func TestNewValidationPanics(t *testing.T) {
	testCases := []struct {
		name string
		new  func()
	}{
		{name: "default page is zero", new: func() { New(WithDefaultPage(0)) }},
		{name: "default page is negative", new: func() { New(WithDefaultPage(-1)) }},
		{name: "default limit is zero", new: func() { New(WithDefaultLimit(0)) }},
		{name: "min limit is zero", new: func() { New(WithMinLimit(0)) }},
		{name: "max limit is zero", new: func() { New(WithMaxLimit(0)) }},
		{name: "default offset is negative", new: func() { New(WithDefaultOffset(-1)) }},
		{name: "local key is empty", new: func() { New(WithLocalKey("")) }},
		{name: "allowed sort key is empty", new: func() { New(WithAllowedSortKeys([]string{" "})) }},
		{
			name: "default limit below min",
			new:  func() { New(WithMinLimit(10), WithDefaultLimit(5)) },
		},
		{
			name: "default limit above max",
			new:  func() { New(WithMaxLimit(5), WithDefaultLimit(10)) },
		},
		{
			name: "default sort key is empty",
			new:  func() { New(WithDefaultSort([]Sort{{Key: " "}})) },
		},
		{
			name: "default sort order is invalid",
			new:  func() { New(WithDefaultSort([]Sort{{Key: "name", Order: "sideways"}})) },
		},
		{
			name: "default sort key is not allowed",
			new:  func() { New(WithDefaultSort([]Sort{{Key: "name"}})) },
		},
		{
			name: "default sort key is missing from allowed keys",
			new: func() {
				New(
					WithDefaultSort([]Sort{{Key: "name"}}),
					WithAllowedSortKeys([]string{"id"}),
				)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Panics(t, tc.new)
		})
	}
}

func TestOptionsAreNormalized(t *testing.T) {
	h := New(
		WithPageQueryKey(" p "),
		WithDefaultPage(2),
		WithLimitQueryKey(" size "),
		WithDefaultLimit(20),
		WithMinLimit(5),
		WithMaxLimit(50),
		WithClampLimit(true),
		WithOffsetQueryKey(" start "),
		WithDefaultOffset(40),
		WithSortQueryKey(" order "),
		WithDefaultSort([]Sort{
			{Key: " name ", Order: " DESC "},
			{Key: "name", Order: "asc"},
			{Key: " id ", Order: ""},
		}),
		WithAllowedSortKeys([]string{" name ", "id "}),
		WithCursorQueryKey(" token "),
		WithLocalKey(" app.pagination "),
		WithSkip(func(ctx *moon.Context) bool { return false }),
	)

	resp, info, nextRan := runHandler(t, h, "/items?p=3&size=99&start=10", " app.pagination ")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, nextRan)
	require.Equal(t, &Info{
		page:   3,
		limit:  50,
		offset: 10,
		sort: []Sort{
			{Key: "name", Order: OrderAscending},
			{Key: "id", Order: OrderAscending},
		},
	}, info)

	resp, info, nextRan = runHandler(t, h, "/items?order=%20id%20", " app.pagination ")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, nextRan)
	require.Equal(t, []Sort{{Key: "id", Order: OrderAscending}}, info.GetSort())
}

func TestHandle(t *testing.T) {
	testCases := []struct {
		name      string
		handler   moon.Handler
		target    string
		localKey  string
		wantInfo  *Info
		wantNext  bool
		wantLocal bool
	}{
		{
			name:      "defaults are stored in the default local key",
			handler:   New(),
			target:    "/items",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 1, limit: 10, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "page limit and offset are parsed",
			handler:   New(),
			target:    "/items?page=3&limit=25&offset=50",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 3, limit: 25, offset: 50},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "custom local key is used",
			handler:   New(WithLocalKey("app.pagination")),
			target:    "/items?page=2",
			localKey:  "app.pagination",
			wantInfo:  &Info{page: 2, limit: 10, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name: "skipped request stores nothing",
			handler: New(WithSkip(func(ctx *moon.Context) bool {
				return true
			})),
			target:    "/items?page=2",
			localKey:  DefaultLocalKey,
			wantInfo:  nil,
			wantNext:  true,
			wantLocal: false,
		},
		{
			name: "non-skipped request is parsed",
			handler: New(WithSkip(func(ctx *moon.Context) bool {
				return false
			})),
			target:    "/items?page=2",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 2, limit: 10, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "limit below the minimum is clamped",
			handler:   New(WithClampLimit(true)),
			target:    "/items?limit=0",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 1, limit: 1, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "negative limit is clamped to the minimum",
			handler:   New(WithClampLimit(true)),
			target:    "/items?limit=-5",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 1, limit: 1, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "limit above the maximum is clamped",
			handler:   New(WithClampLimit(true)),
			target:    "/items?limit=101",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 1, limit: 100, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "limit below a custom minimum is clamped",
			handler:   New(WithClampLimit(true), WithMinLimit(5), WithMaxLimit(20)),
			target:    "/items?limit=4",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 1, limit: 5, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
		{
			name:      "limit above a custom maximum is clamped",
			handler:   New(WithClampLimit(true), WithMinLimit(5), WithMaxLimit(20)),
			target:    "/items?limit=99",
			localKey:  DefaultLocalKey,
			wantInfo:  &Info{page: 1, limit: 20, offset: 0},
			wantNext:  true,
			wantLocal: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, info, nextRan := runHandler(t, tc.handler, tc.target, tc.localKey)
			defer resp.Body.Close()

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, tc.wantNext, nextRan)
			if !tc.wantLocal {
				require.Nil(t, info)
				return
			}
			require.Equal(t, tc.wantInfo, info)
		})
	}
}

func TestHandleCustomQueryKeys(t *testing.T) {
	h := New(
		WithPageQueryKey("p"),
		WithLimitQueryKey("size"),
		WithOffsetQueryKey("start"),
	)

	resp, info, nextRan := runHandler(t, h, "/items?p=4&size=12&start=24", DefaultLocalKey)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, nextRan)
	require.Equal(t, &Info{page: 4, limit: 12, offset: 24}, info)
}

func TestDisabledQueryKeys(t *testing.T) {
	testCases := []struct {
		name    string
		handler moon.Handler
		target  string
		want    *Info
	}{
		{
			name: "all query keys disabled keeps every default",
			handler: New(
				WithPageQueryKey(""),
				WithLimitQueryKey(""),
				WithOffsetQueryKey(""),
				WithCursorQueryKey(""),
				WithDefaultPage(3),
				WithDefaultLimit(25),
				WithDefaultOffset(50),
			),
			target: "/items?page=99&limit=2&offset=7&cursor=bad&=x",
			want:   &Info{page: 3, limit: 25, offset: 50},
		},
		{
			name:    "disabled page keeps other parameters",
			handler: New(WithPageQueryKey("")),
			target:  "/items?page=99&limit=20&offset=5",
			want:    &Info{page: 1, limit: 20, offset: 5},
		},
		{
			name:    "disabled limit keeps other parameters",
			handler: New(WithLimitQueryKey("")),
			target:  "/items?page=2&limit=99&offset=5",
			want:    &Info{page: 2, limit: 10, offset: 5},
		},
		{
			name:    "disabled offset keeps other parameters",
			handler: New(WithOffsetQueryKey("")),
			target:  "/items?page=2&limit=20&offset=99",
			want:    &Info{page: 2, limit: 20, offset: 0},
		},
		{
			name:    "disabled cursor ignores a malformed value",
			handler: New(WithCursorQueryKey("")),
			target:  "/items?cursor=bad&page=2",
			want:    &Info{page: 2, limit: 10, offset: 0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, info, nextRan := runHandler(t, tc.handler, tc.target, DefaultLocalKey)
			defer resp.Body.Close()

			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.True(t, nextRan)
			require.Equal(t, tc.want, info)
		})
	}
}

func TestInvalidPaginationValues(t *testing.T) {
	testCases := []struct {
		name       string
		handler    moon.Handler
		target     string
		wantDetail string
	}{
		{
			name:       "page is not an integer",
			handler:    New(),
			target:     "/items?page=abc",
			wantDetail: "page must be a valid positive integer",
		},
		{
			name:       "page is zero",
			handler:    New(),
			target:     "/items?page=0",
			wantDetail: "page must be a valid positive integer",
		},
		{
			name:       "page is negative",
			handler:    New(),
			target:     "/items?page=-1",
			wantDetail: "page must be a valid positive integer",
		},
		{
			name:       "limit is not an integer",
			handler:    New(),
			target:     "/items?limit=abc",
			wantDetail: "limit must be a valid integer between 1 and 100",
		},
		{
			name:       "clamping does not accept non-integers",
			handler:    New(WithClampLimit(true)),
			target:     "/items?limit=abc",
			wantDetail: "limit must be a valid integer between 1 and 100",
		},
		{
			name:       "limit is below min",
			handler:    New(),
			target:     "/items?limit=0",
			wantDetail: "limit must be a valid integer between 1 and 100",
		},
		{
			name:       "limit is above max",
			handler:    New(),
			target:     "/items?limit=101",
			wantDetail: "limit must be a valid integer between 1 and 100",
		},
		{
			name:       "limit is negative",
			handler:    New(),
			target:     "/items?limit=-1",
			wantDetail: "limit must be a valid integer between 1 and 100",
		},
		{
			name:       "custom limit bounds are reported",
			handler:    New(WithMinLimit(5), WithMaxLimit(20)),
			target:     "/items?limit=4",
			wantDetail: "limit must be a valid integer between 5 and 20",
		},
		{
			name:       "offset is not an integer",
			handler:    New(),
			target:     "/items?offset=abc",
			wantDetail: "offset must be a valid non-negative integer",
		},
		{
			name:       "offset is negative",
			handler:    New(),
			target:     "/items?offset=-1",
			wantDetail: "offset must be a valid non-negative integer",
		},
		{
			name:       "offset is not an integer literal",
			handler:    New(),
			target:     "/items?offset=1.5",
			wantDetail: "offset must be a valid non-negative integer",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, info, nextRan := runHandler(t, tc.handler, tc.target, DefaultLocalKey)
			defer resp.Body.Close()

			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.False(t, nextRan)
			require.Nil(t, info)

			kind, details := readError(t, resp)
			require.Equal(t, moon.ErrBadRequest.Kind, kind)
			require.Equal(t, tc.wantDetail, details)
		})
	}
}

func TestSortParsing(t *testing.T) {
	sorted := New(
		WithSortQueryKey("sort"),
		WithAllowedSortKeys([]string{"name", "id", "created_at"}),
	)
	sortedWithDefault := New(
		WithSortQueryKey("sort"),
		WithAllowedSortKeys([]string{"name", "id"}),
		WithDefaultSort([]Sort{{Key: "name", Order: OrderDescending}}),
	)

	testCases := []struct {
		name       string
		handler    moon.Handler
		target     string
		wantStatus int
		wantSort   []Sort
		wantDetail string
	}{
		{
			name:       "no query keeps default sort",
			handler:    sortedWithDefault,
			target:     "/items",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "name", Order: OrderDescending}},
		},
		{
			name:       "no sign means ascending",
			handler:    sorted,
			target:     "/items?sort=id",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "id", Order: OrderAscending}},
		},
		{
			name:       "plus sign means ascending",
			handler:    sorted,
			target:     "/items?sort=%2Bid",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "id", Order: OrderAscending}},
		},
		{
			name:       "minus sign means descending",
			handler:    sorted,
			target:     "/items?sort=-id",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "id", Order: OrderDescending}},
		},
		{
			name:       "comma separated keys are all parsed",
			handler:    sorted,
			target:     "/items?sort=name,-created_at",
			wantStatus: http.StatusOK,
			wantSort: []Sort{
				{Key: "name", Order: OrderAscending},
				{Key: "created_at", Order: OrderDescending},
			},
		},
		{
			name:       "repeated query values are all parsed in order",
			handler:    sorted,
			target:     "/items?sort=id&sort=-name",
			wantStatus: http.StatusOK,
			wantSort: []Sort{
				{Key: "id", Order: OrderAscending},
				{Key: "name", Order: OrderDescending},
			},
		},
		{
			name:       "query overrides default sort",
			handler:    sortedWithDefault,
			target:     "/items?sort=id",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "id", Order: OrderAscending}},
		},
		{
			name:       "surrounding whitespace is trimmed",
			handler:    sorted,
			target:     "/items?sort=%20name%20",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "name", Order: OrderAscending}},
		},
		{
			name:       "custom sort query key",
			handler:    New(WithSortQueryKey("order"), WithAllowedSortKeys([]string{"name"})),
			target:     "/items?order=-name",
			wantStatus: http.StatusOK,
			wantSort:   []Sort{{Key: "name", Order: OrderDescending}},
		},
		{
			name:       "sorting disabled ignores the query",
			handler:    New(),
			target:     "/items?sort=anything",
			wantStatus: http.StatusOK,
			wantSort:   nil,
		},
		{
			name:       "empty sort value is rejected",
			handler:    sorted,
			target:     "/items?sort=",
			wantStatus: http.StatusBadRequest,
			wantDetail: "sort key cannot be empty",
		},
		{
			name:       "empty comma segment is rejected",
			handler:    sorted,
			target:     "/items?sort=name,,id",
			wantStatus: http.StatusBadRequest,
			wantDetail: "sort key cannot be empty",
		},
		{
			name:       "sign without a key is rejected",
			handler:    sorted,
			target:     "/items?sort=-",
			wantStatus: http.StatusBadRequest,
			wantDetail: "sort key cannot be empty",
		},
		{
			name:       "disallowed key is rejected",
			handler:    sorted,
			target:     "/items?sort=unknown",
			wantStatus: http.StatusBadRequest,
			wantDetail: `sort key "unknown" is not allowed`,
		},
		{
			name:       "disallowed signed key reports the key without its sign",
			handler:    sorted,
			target:     "/items?sort=-unknown",
			wantStatus: http.StatusBadRequest,
			wantDetail: `sort key "unknown" is not allowed`,
		},
		{
			name:       "sort keys are case sensitive",
			handler:    sorted,
			target:     "/items?sort=Name",
			wantStatus: http.StatusBadRequest,
			wantDetail: `sort key "Name" is not allowed`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, info, nextRan := runHandler(t, tc.handler, tc.target, DefaultLocalKey)
			defer resp.Body.Close()

			require.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantStatus == http.StatusBadRequest {
				require.False(t, nextRan)
				require.Nil(t, info)

				kind, details := readError(t, resp)
				require.Equal(t, moon.ErrBadRequest.Kind, kind)
				require.Equal(t, tc.wantDetail, details)
				return
			}

			require.True(t, nextRan)
			require.NotNil(t, info)
			require.Equal(t, tc.wantSort, info.GetSort())
		})
	}
}

func TestCursorParsing(t *testing.T) {
	encoded, err := EncodeCursor(testCursor{ID: 42, Name: "alice"})
	require.NoError(t, err)
	cursor := string(encoded)

	testCases := []struct {
		name       string
		handler    moon.Handler
		target     string
		wantStatus int
		wantCursor string
		wantValues *testCursor
		wantDetail string
	}{
		{
			name:       "valid cursor is stored raw and decodes into a struct",
			handler:    New(),
			target:     "/items?cursor=" + url.QueryEscape(cursor),
			wantStatus: http.StatusOK,
			wantCursor: cursor,
			wantValues: &testCursor{ID: 42, Name: "alice"},
		},
		{
			name:       "custom cursor query key",
			handler:    New(WithCursorQueryKey("token")),
			target:     "/items?token=" + url.QueryEscape(cursor),
			wantStatus: http.StatusOK,
			wantCursor: cursor,
			wantValues: &testCursor{ID: 42, Name: "alice"},
		},
		{
			name:       "missing cursor returns an empty raw value",
			handler:    New(),
			target:     "/items",
			wantStatus: http.StatusOK,
			wantCursor: "",
			wantValues: nil,
		},
		{
			name:       "invalid base64 is rejected",
			handler:    New(),
			target:     "/items?cursor=not-base64!",
			wantStatus: http.StatusBadRequest,
			wantDetail: "cursor must be a valid base64-encoded JSON value",
		},
		{
			name:       "invalid JSON is rejected",
			handler:    New(),
			target:     "/items?cursor=" + url.QueryEscape(base64.URLEncoding.EncodeToString([]byte("not json"))),
			wantStatus: http.StatusBadRequest,
			wantDetail: "cursor must be a valid base64-encoded JSON value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, info, nextRan := runHandler(t, tc.handler, tc.target, DefaultLocalKey)
			defer resp.Body.Close()

			require.Equal(t, tc.wantStatus, resp.StatusCode)
			if tc.wantStatus == http.StatusBadRequest {
				require.False(t, nextRan)
				require.Nil(t, info)

				kind, details := readError(t, resp)
				require.Equal(t, moon.ErrBadRequest.Kind, kind)
				require.Equal(t, tc.wantDetail, details)
				return
			}

			require.True(t, nextRan)
			require.NotNil(t, info)
			require.Equal(t, tc.wantCursor, info.GetCursor())
			if tc.wantValues == nil {
				return
			}

			var values testCursor
			require.NoError(t, DecodeCursor([]byte(info.GetCursor()), &values))
			require.Equal(t, *tc.wantValues, values)
		})
	}
}

func TestCursorParsingAcceptsAnyJsonValue(t *testing.T) {
	encoded, err := EncodeCursor([]int{1, 2, 3})
	require.NoError(t, err)

	resp, info, nextRan := runHandler(t, New(), "/items?cursor="+url.QueryEscape(string(encoded)), DefaultLocalKey)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, nextRan)
	require.NotNil(t, info)

	var values []int
	require.NoError(t, DecodeCursor([]byte(info.GetCursor()), &values))
	require.Equal(t, []int{1, 2, 3}, values)
}

func TestEncodeCursor(t *testing.T) {
	encoded, err := EncodeCursor(testCursor{ID: 7, Name: "alice"})
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	var values testCursor
	require.NoError(t, DecodeCursor(encoded, &values))
	require.Equal(t, testCursor{ID: 7, Name: "alice"}, values)

	_, err = EncodeCursor(map[string]any{"bad": func() {}})
	require.Error(t, err)
}

func TestDecodeCursor(t *testing.T) {
	t.Run("parses an array cursor into a slice", func(t *testing.T) {
		encoded, err := EncodeCursor([]int{1, 2, 3})
		require.NoError(t, err)

		var values []int
		require.NoError(t, DecodeCursor(encoded, &values))
		require.Equal(t, []int{1, 2, 3}, values)
	})

	t.Run("invalid base64 returns an error", func(t *testing.T) {
		var values testCursor
		require.Error(t, DecodeCursor([]byte("not-base64!"), &values))
	})

	t.Run("invalid JSON returns an error", func(t *testing.T) {
		raw := []byte(base64.URLEncoding.EncodeToString([]byte("not json")))
		var values testCursor
		require.Error(t, DecodeCursor(raw, &values))
	})

	t.Run("type mismatch returns an error", func(t *testing.T) {
		encoded, err := EncodeCursor(map[string]any{"id": 1})
		require.NoError(t, err)

		var values []int
		require.Error(t, DecodeCursor(encoded, &values))
	})
}

func TestDefaultSortIsNotSharedAcrossRequests(t *testing.T) {
	h := New(
		WithAllowedSortKeys([]string{"name"}),
		WithDefaultSort([]Sort{{Key: "name"}}),
	)

	var infos []*Info

	app := moon.New()
	app.Use("/*", h)
	app.Map(http.MethodGet, "/items", func(ctx *moon.Context) error {
		info, _ := ctx.GetLocal[*Info](DefaultLocalKey)
		infos = append(infos, info)
		return ctx.WriteStatus(http.StatusOK)
	})

	for range 2 {
		resp := app.Test(httptest.NewRequest(http.MethodGet, "/items", nil))
		_, err := io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	require.Len(t, infos, 2)
	infos[0].sort[0].Key = "mutated"
	require.Equal(t, "name", infos[1].sort[0].Key)
}
