package moon

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// RequestLoggingEntry is one key/value pair logged for every handled request
// when request logging is enabled with [AppOptions.WithRequestLogging].
//
// Key is the attribute name Value is logged under. Value runs once per
// request after the response was written; keep it cheap and non-blocking.
// Before, when non-nil, runs before the handler chain so the entry can
// capture request-scoped state (see [RleDuration]).
//
// Every app starts with [DefaultRequestLoggingEntries]; replace that list
// with [AppOptions.WithRequestLoggingEntries] and append to it with
// [App.AddRequestLoggingEntry].
type RequestLoggingEntry struct {
	Before RequestLoggingEntryBefore
	Key    string
	Value  RequestLoggingEntryValue
}

// RequestLoggingEntryBefore prepares a [RequestLoggingEntry] before the
// handler chain runs, e.g. by storing a start time in a [Context] local.
// It runs only when request logging is enabled, once per request and per
// entry present when the request starts, and may be nil.
type RequestLoggingEntryBefore func(*Context)

// RequestLoggingEntryValue renders the value logged for a
// [RequestLoggingEntry] after the handler chain finished. err is the error
// the app's [ErrorHandler] returned, nil when the chain succeeded, so it
// matches the error the response carries. Keep it cheap and non-blocking.
type RequestLoggingEntryValue func(*Context, error) any

// DefaultRequestLoggingEntries is the entry list every app starts with:
// [RleDuration], [RleRemote], [RleMethod], [RlePath], [RleStatus] and
// [RleError], in that order. Pass it to
// [AppOptions.WithRequestLoggingEntries] to restore the defaults after
// resetting the list.
//
// The slice is shared by every app, and apps never write into it:
// [App.AddRequestLoggingEntry] appends to the list owned by the app.
var DefaultRequestLoggingEntries = []RequestLoggingEntry{
	RleDuration,
	RleRemote,
	RleMethod,
	RlePath,
	RleStatus,
	RleError,
}

const rleDurationStartTimeLocalKey = "moon.rle_duration_start_time_local_key"

var (
	// RleDuration logs how long handling the request took, measured from
	// when the handler chain is about to run, via its Before hook, until
	// the entry is rendered. The value is a [time.Duration].
	RleDuration = RequestLoggingEntry{
		Before: func(ctx *Context) {
			ctx.SetLocal(rleDurationStartTimeLocalKey, time.Now())
		},
		Key: "duration",
		Value: func(ctx *Context, err error) any {
			t, _ := ctx.GetLocal[time.Time](rleDurationStartTimeLocalKey)
			return time.Since(t)
		},
	}

	// RleRemote logs the client address from [Context.GetRemoteAddress].
	RleRemote = RequestLoggingEntry{
		Key: "remote",
		Value: func(ctx *Context, err error) any {
			return ctx.GetRemoteAddress()
		},
	}

	// RleMethod logs the request method, e.g. "GET".
	// See [Context.GetMethod].
	RleMethod = RequestLoggingEntry{
		Key: "method",
		Value: func(ctx *Context, err error) any {
			return ctx.GetMethod()
		},
	}

	// RlePath logs the request path, e.g. "/users/42".
	// See [Context.GetPath].
	RlePath = RequestLoggingEntry{
		Key: "path",
		Value: func(ctx *Context, err error) any {
			return ctx.GetPath()
		},
	}

	// RleStatus logs the response status code as an int, defaulting to 200
	// when the handler chain wrote no status. See [Context.GetStatusCode].
	RleStatus = RequestLoggingEntry{
		Key: "status",
		Value: func(ctx *Context, err error) any {
			return ctx.GetStatusCode()
		},
	}

	// RleError logs the error the app's [ErrorHandler] returned, or nil when
	// the handler chain succeeded.
	RleError = RequestLoggingEntry{
		Key: "error",
		Value: func(ctx *Context, err error) any {
			return err
		},
	}
)

// AddRequestLoggingEntry appends an entry to the request logging list of
// this app only, so multiple apps can log differently. The app starts from
// [DefaultRequestLoggingEntries], or from
// [AppOptions.WithRequestLoggingEntries] when it was given, and logs the new
// entry after the ones already in the list.
//
// The key is trimmed. It panics:
//
//   - once the app started, so entries belong to setup, before [App.Start];
//   - on an empty or whitespace-only key, or a nil value;
//   - when the key is already in this app's list.
//
// The call is meant to run sequentially; serializing calls made from
// multiple goroutines is the caller's responsibility.
func (me *App) AddRequestLoggingEntry(entry RequestLoggingEntry) {
	Assert(!me.started, "cannot add request logging entries after the app started")

	entry.Key = strings.TrimSpace(entry.Key)
	Assert(entry.Key != "", "key cannot be empty or whitespace")
	Assert(entry.Value != nil, "value func cannot be nil")

	duplicateIndex := slices.IndexFunc(
		me.options.requestLoggingEntries,
		func(e RequestLoggingEntry) bool { return e.Key == entry.Key },
	)
	Assert(
		duplicateIndex == -1,
		fmt.Sprintf("request logging entry key %q is already registered at index %d", entry.Key, duplicateIndex),
	)

	me.options.requestLoggingEntries = append(me.options.requestLoggingEntries, entry)
}

// logRequest logs the handled request with one attribute per entry in this
// app's request logging list. err is what the app's [ErrorHandler] returned,
// nil when the handler chain succeeded.
func (me *App) logRequest(ctx *Context, err error) {
	entries := me.options.requestLoggingEntries

	args := make([]any, 0, len(entries)*2)
	for _, e := range entries {
		args = append(args, e.Key, e.Value(ctx, err))
	}

	me.options.logger.Info("request handled", args...)
}
