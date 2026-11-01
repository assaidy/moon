// Package pagination provides a handler that parses pagination, sorting and
// cursor query parameters into a request-scoped [Info] stored in a context
// local. Register [Handler.Handle] as a middleware:
//
//	app.UseAll(pagination.New().Handle)
//
// Read the parsed values downstream with [moon.Context.GetLocal]:
//
//	app.MapGet("/users", func(ctx *moon.Context) error {
//		info, ok := ctx.GetLocal[*pagination.Info](pagination.DefaultLocalKey)
//		if !ok {
//			return moon.ErrInternalServerError
//		}
//		users, err := listUsers(ctx, info.GetOffset(), info.GetLimit(), info.GetSort())
//		if err != nil {
//			return err
//		}
//		return ctx.WriteJson(http.StatusOK, users)
//	})
//
// The handler fills every field of [Info] on every request. The application
// decides which pagination policy to use: page/limit, offset/limit,
// cursor/limit, or any combination. Parsing never queries anything.
//
// The default query keys are "page", "limit", "offset" and "cursor". Any query
// key can be disabled by setting it to an empty string: the corresponding
// default value is then used and the query parameter is ignored. Sorting is
// disabled until [WithSortQueryKey] is configured. Invalid values are rejected
// with [moon.ErrBadRequest] and a descriptive detail.
//
// Cursors are opaque values produced by [EncodeCursor] and parsed with
// [DecodeCursor]. [Info.GetCursor] returns the raw cursor query value.
package pagination

import (
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/assaidy/moon"
)

// Handler parses pagination, sorting and cursor query parameters. Use [New] to
// construct it, passing [Option]s to configure it, then register
// [Handler.Handle] in the chain.
type Handler struct {
	options options
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register [Handler.Handle] in the chain:
//
//	app.UseAll(pagination.New().Handle)
//	app.UseAll(pagination.New(
//		pagination.WithDefaultLimit(20),
//		pagination.WithMaxLimit(200),
//	).Handle)
//
// Defaults: page 1, limit 10 (between 1 and 100), offset 0, query keys "page",
// "limit", "offset" and "cursor", sorting disabled, no default sort, limit
// clamping disabled, and [DefaultLocalKey] as the context local key.
//
// Passing an empty key to [WithPageQueryKey], [WithLimitQueryKey],
// [WithOffsetQueryKey], [WithSortQueryKey] or [WithCursorQueryKey] disables that
// query parameter; the configured default value is still used.
//
// It panics when the default limit is outside the min/max range, or when a
// key in the default sort is not listed in [WithAllowedSortKeys].
func New(opts ...Option) *Handler {
	options := options{
		pageQueryKey:   "page",
		defaultPage:    1,
		limitQueryKey:  "limit",
		defaultLimit:   10,
		maxLimit:       100,
		minLimit:       1,
		offsetQueryKey: "offset",
		defaultOffset:  0,
		cursorQueryKey: "cursor",
		localKey:       DefaultLocalKey,
	}
	for _, opt := range opts {
		opt(&options)
	}

	moon.Assert(
		options.minLimit <= options.defaultLimit && options.defaultLimit <= options.maxLimit,
		"default limit must be between min limit and max limit",
	)
	for _, s := range options.defaultSort {
		moon.Assert(
			slices.Contains(options.allowedSortKeys, s.Key),
			fmt.Sprintf("default sort key %q must be listed in allowed sort keys", s.Key),
		)
	}

	return &Handler{options: options}
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	skip            func(*moon.Context) bool
	pageQueryKey    string
	defaultPage     int
	limitQueryKey   string
	defaultLimit    int
	maxLimit        int
	minLimit        int
	clampLimit      bool
	offsetQueryKey  string
	defaultOffset   int
	sortQueryKey    string
	defaultSort     []Sort
	allowedSortKeys []string
	cursorQueryKey  string
	localKey        string
}

// Sort is one sort criterion: a key and an order. The query syntax uses a
// leading "-" for [OrderDescending] and an optional leading "+" for
// [OrderAscending].
type Sort struct {
	Key   string
	Order string
}

const (
	// OrderAscending sorts a key from lowest to highest.
	OrderAscending = "asc"
	// OrderDescending sorts a key from highest to lowest.
	OrderDescending = "desc"
)

// DefaultLocalKey is the context local key used when [WithLocalKey] is not
// given. Read the parsed values with [moon.Context.GetLocal] using this key.
const DefaultLocalKey = "moon.handlers.pagination.local_key"

// Option configures a [Handler]. Pass them to [New]:
//
//	app.UseAll(pagination.New(
//		pagination.WithDefaultLimit(20),
//		pagination.WithSortQueryKey("sort"),
//		pagination.WithAllowedSortKeys([]string{"created_at", "name"}),
//	).Handle)
type Option func(*options)

// WithSkip bypasses parsing for requests where f returns true. The chain still
// runs, but no [Info] is stored, so downstream reads return false. Pass it to
// [New].
//
// Default: nil (nothing is skipped)
func WithSkip(f func(*moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// WithPageQueryKey sets the query key carrying the page number. Surrounding
// whitespace is trimmed. An empty key disables page parsing, so [Info.GetPage]
// always returns the default page. Pass it to [New].
//
// Default: "page"
func WithPageQueryKey(s string) Option {
	s = strings.TrimSpace(s)
	return func(o *options) {
		o.pageQueryKey = s
	}
}

// WithDefaultPage sets the page used when the page query parameter is absent.
// It panics on a non-positive page. Pass it to [New].
//
// Default: 1
func WithDefaultPage(i int) Option {
	moon.Assert(i > 0, "page must be positive")
	return func(o *options) {
		o.defaultPage = i
	}
}

// WithLimitQueryKey sets the query key carrying the limit. Surrounding
// whitespace is trimmed. An empty key disables limit parsing, so [Info.GetLimit]
// always returns the default limit. Pass it to [New].
//
// Default: "limit"
func WithLimitQueryKey(s string) Option {
	s = strings.TrimSpace(s)
	return func(o *options) {
		o.limitQueryKey = s
	}
}

// WithDefaultLimit sets the limit used when the limit query parameter is
// absent. It panics on a non-positive limit. [New] panics when the default is
// outside the min/max range. Pass it to [New].
//
// Default: 10
func WithDefaultLimit(i int) Option {
	moon.Assert(i > 0, "limit must be positive")
	return func(o *options) {
		o.defaultLimit = i
	}
}

// WithMaxLimit sets the largest accepted limit. It panics on a non-positive
// limit. [New] panics when the default limit is greater than this value. Pass
// it to [New].
//
// Default: 100
func WithMaxLimit(i int) Option {
	moon.Assert(i > 0, "limit must be positive")
	return func(o *options) {
		o.maxLimit = i
	}
}

// WithMinLimit sets the smallest accepted limit. It panics on a non-positive
// limit: a zero minimum would allow requests that do unnecessary database work
// and invites abuse. [New] panics when the default limit is smaller than this
// value. Pass it to [New].
//
// Default: 1
func WithMinLimit(i int) Option {
	moon.Assert(i > 0, "limit must be positive")
	return func(o *options) {
		o.minLimit = i
	}
}

// WithClampLimit controls how out-of-range limits are handled. When enabled,
// limits below [WithMinLimit] are raised to the minimum and limits above
// [WithMaxLimit] are lowered to the maximum instead of being rejected. Values
// that are not integers are still rejected. Disabled by default, so out-of-range
// limits return [moon.ErrBadRequest]. Pass it to [New].
//
// Default: false
func WithClampLimit(b bool) Option {
	return func(o *options) {
		o.clampLimit = b
	}
}

// WithOffsetQueryKey sets the query key carrying the offset. Surrounding
// whitespace is trimmed. An empty key disables offset parsing, so
// [Info.GetOffset] always returns the default offset. Pass it to [New].
//
// Default: "offset"
func WithOffsetQueryKey(s string) Option {
	s = strings.TrimSpace(s)
	return func(o *options) {
		o.offsetQueryKey = s
	}
}

// WithDefaultOffset sets the offset used when the offset query parameter is
// absent. It panics on a negative offset. Pass it to [New].
//
// Default: 0
func WithDefaultOffset(i int) Option {
	moon.Assert(i >= 0, "offset cannot be negative")
	return func(o *options) {
		o.defaultOffset = i
	}
}

// WithSortQueryKey enables sorting and sets the query key carrying sort
// criteria. Surrounding whitespace is trimmed. An empty key disables sorting,
// which is the default. Pass it to [New].
//
// Default: "" (sorting disabled)
func WithSortQueryKey(s string) Option {
	s = strings.TrimSpace(s)
	return func(o *options) {
		o.sortQueryKey = s
	}
}

// WithDefaultSort sets the sort criteria used when the sort query parameter is
// absent. Keys and orders are trimmed; an empty order becomes
// [OrderAscending]. When a key appears more than once, the last order wins and
// the first position is kept. It panics on an empty key or on an order other
// than [OrderAscending] and [OrderDescending].
//
// Every default key must also be listed in [WithAllowedSortKeys], otherwise
// [New] panics. Keys are case sensitive. Pass it to [New].
//
// Default: nil (no default sort)
func WithDefaultSort(s []Sort) Option {
	normalized := make([]Sort, 0, len(s))
	for _, sort := range s {
		sort.Key = strings.TrimSpace(sort.Key)
		moon.Assert(sort.Key != "", "key cannot be empty")
		sort.Order = strings.ToLower(strings.TrimSpace(sort.Order))
		if sort.Order == "" {
			sort.Order = OrderAscending
		}
		moon.Assert(
			sort.Order == OrderAscending || sort.Order == OrderDescending,
			fmt.Sprintf("invalid sort order; sort order can only be %q or %q", OrderAscending, OrderDescending),
		)
		index := slices.IndexFunc(normalized, func(s Sort) bool { return s.Key == sort.Key })
		if index != -1 {
			normalized[index].Order = sort.Order
			continue
		}
		normalized = append(normalized, sort)
	}

	return func(o *options) {
		o.defaultSort = normalized
	}
}

// WithAllowedSortKeys sets the keys accepted in the sort query parameter.
// Surrounding whitespace is trimmed and empty keys panic. Keys are case
// sensitive. When the list is empty, any sort query is rejected. Pass it to
// [New].
//
// Default: nil (no sort key is allowed)
func WithAllowedSortKeys(keys []string) Option {
	normalized := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		moon.Assert(key != "", "key cannot be empty")
		normalized = append(normalized, key)
	}

	return func(o *options) {
		o.allowedSortKeys = normalized
	}
}

// WithCursorQueryKey sets the query key carrying the cursor. Surrounding
// whitespace is trimmed. An empty key disables cursor parsing, so
// [Info.GetCursor] always returns "". Pass it to [New].
//
// Default: "cursor"
func WithCursorQueryKey(s string) Option {
	s = strings.TrimSpace(s)
	return func(o *options) {
		o.cursorQueryKey = s
	}
}

// WithLocalKey sets the context local key under which the parsed [Info] is
// stored. The key is used as-is, including surrounding whitespace. It panics on
// an empty key. Read the value with [moon.Context.GetLocal] using the same key.
// Pass it to [New].
//
// Default: [DefaultLocalKey].
func WithLocalKey(s string) Option {
	moon.Assert(s != "", "local key cannot be empty")
	return func(o *options) {
		o.localKey = s
	}
}

// Handle parses the request query parameters and stores the resulting [Info]
// in the configured context local, then runs the chain. Requests skipped by
// [WithSkip] run the chain without parsing or storing anything.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	info, err := me.getInfo(ctx)
	if err != nil {
		return err
	}

	ctx.SetLocal(me.options.localKey, info)

	return ctx.Next()
}

// getInfo parses the request query parameters into an [Info] seeded with the
// configured defaults. All pagination styles are parsed independently: page,
// offset and cursor may be present together, and the application chooses which
// values to use.
func (me *Handler) getInfo(ctx *moon.Context) (*Info, error) {
	info := &Info{
		page:   me.options.defaultPage,
		limit:  me.options.defaultLimit,
		offset: me.options.defaultOffset,
		sort:   slices.Clone(me.options.defaultSort),
	}

	if me.options.cursorQueryKey != "" {
		if cursorQuery := ctx.GetQuery(me.options.cursorQueryKey); cursorQuery != "" {
			var value any
			if err := DecodeCursor([]byte(cursorQuery), &value); err != nil {
				return nil, moon.ErrBadRequest.WithDetails("cursor must be a valid base64-encoded JSON value")
			}
			info.cursor = cursorQuery
		}
	}
	if me.options.pageQueryKey != "" {
		if pageQuery := ctx.GetQuery(me.options.pageQueryKey); pageQuery != "" {
			if i, err := strconv.Atoi(pageQuery); err != nil || i < 1 {
				return nil, moon.ErrBadRequest.WithDetails("page must be a valid positive integer")
			} else {
				info.page = i
			}
		}
	}
	if me.options.limitQueryKey != "" {
		if limitQuery := ctx.GetQuery(me.options.limitQueryKey); limitQuery != "" {
			i, err := strconv.Atoi(limitQuery)
			if err != nil {
				return nil, moon.ErrBadRequest.WithDetails(
					fmt.Sprintf("limit must be a valid integer between %d and %d", me.options.minLimit, me.options.maxLimit),
				)
			}
			if me.options.clampLimit {
				info.limit = max(me.options.minLimit, min(i, me.options.maxLimit))
			} else if i < me.options.minLimit || i > me.options.maxLimit {
				return nil, moon.ErrBadRequest.WithDetails(
					fmt.Sprintf("limit must be a valid integer between %d and %d", me.options.minLimit, me.options.maxLimit),
				)
			} else {
				info.limit = i
			}
		}
	}
	if me.options.offsetQueryKey != "" {
		if offsetQuery := ctx.GetQuery(me.options.offsetQueryKey); offsetQuery != "" {
			if i, err := strconv.Atoi(offsetQuery); err != nil || i < 0 {
				return nil, moon.ErrBadRequest.WithDetails("offset must be a valid non-negative integer")
			} else {
				info.offset = i
			}
		}
	}
	if me.options.sortQueryKey != "" {
		if sortQueries := ctx.GetUrl().Query()[me.options.sortQueryKey]; len(sortQueries) > 0 {
			info.sort = nil // reset it as query must have at least one valid key.
			for _, query := range sortQueries {
				for rawKey := range strings.SplitSeq(query, ",") {
					rawKey = strings.TrimSpace(rawKey)
					if rawKey == "" {
						return nil, moon.ErrBadRequest.WithDetails("sort key cannot be empty")
					}
					order := OrderAscending
					key := rawKey
					if sign := key[0]; sign == '+' || sign == '-' {
						if sign == '-' {
							order = OrderDescending
						}
						key = key[1:]
					}
					if key == "" {
						return nil, moon.ErrBadRequest.WithDetails("sort key cannot be empty")
					}
					if !slices.Contains(me.options.allowedSortKeys, key) {
						return nil, moon.ErrBadRequest.WithDetails(fmt.Sprintf("sort key %q is not allowed", key))
					}
					info.sort = append(info.sort, Sort{Key: key, Order: order})
				}
			}
		}
	}

	return info, nil
}

// Info holds the pagination, sorting and cursor values parsed for one request.
// Use the getters below; every field is filled on every request, and the
// application decides which pagination policy to apply.
type Info struct {
	page   int
	limit  int
	offset int
	sort   []Sort
	cursor string
}

// GetPage returns the requested page, or the configured default page when the
// page query parameter is absent.
func (me *Info) GetPage() int {
	return me.page
}

// GetLimit returns the requested limit, or the configured default limit when
// the limit query parameter is absent.
func (me *Info) GetLimit() int {
	return me.limit
}

// GetOffset returns the requested offset, or the configured default offset
// when the offset query parameter is absent.
func (me *Info) GetOffset() int {
	return me.offset
}

// GetSort returns the requested sort criteria, or a copy of the configured
// default sort when the sort query parameter is absent.
func (me *Info) GetSort() []Sort {
	return me.sort
}

// GetCursor returns the cursor query value exactly as sent by the client, or
// "" when no cursor was given. Decode it with [DecodeCursor].
func (me *Info) GetCursor() string {
	return me.cursor
}

// EncodeCursor encodes v as an opaque cursor: JSON followed by URL-safe base64.
// Use the returned bytes as the cursor query value, or convert them to string
// when writing them to a JSON response:
//
//	nextCursor, err := pagination.EncodeCursor(last)
//	if err != nil {
//		return err
//	}
//	return ctx.WriteJson(http.StatusOK, moon.Object{
//		"next_cursor": string(nextCursor),
//	})
//
// It returns an error when v cannot be encoded as JSON.
func EncodeCursor(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to encode cursor: %w", err)
	}
	return []byte(base64.URLEncoding.EncodeToString(b)), nil
}

// DecodeCursor decodes a cursor produced by [EncodeCursor] into v, which must
// be a non-nil pointer. It returns an error when raw is not valid URL-safe
// base64 or does not contain valid JSON.
//
// Decode the value returned by [Info.GetCursor]:
//
//	type userCursor struct {
//		ID int `json:"id"`
//	}
//
//	var cursor userCursor
//	if err := pagination.DecodeCursor([]byte(info.GetCursor()), &cursor); err != nil {
//		return err
//	}
func DecodeCursor(raw []byte, v any) error {
	decoded, err := base64.URLEncoding.DecodeString(string(raw))
	if err != nil {
		return fmt.Errorf("failed to decode base64: %w", err)
	}
	if err := json.Unmarshal(decoded, v); err != nil {
		return fmt.Errorf("failed to decode json: %w", err)
	}
	return nil
}
