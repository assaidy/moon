package moon

import (
	"fmt"
	"os"
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
// Every app starts with the default list; replace it with
// [AppOptions.WithRequestLoggingEntries] and append to it with
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

// defaultRequestLoggingEntries is the entry list every app starts with.
// [New] clones it, so no app ever writes into it.
var defaultRequestLoggingEntries = []RequestLoggingEntry{
	RleDuration,
	RleRemote,
	RleMethod,
	RlePath,
	RleStatus,
	RleError,
}

const (
	rleTimeLocalKey              = "moon.rle_time"
	rleDurationStartTimeLocalKey = "moon.rle_duration_start_time"
)

var (
	// RleTime logs the time the request arrived, captured by its Before hook
	// before the handler chain runs. The value is a [time.Time].
	RleTime = RequestLoggingEntry{
		Before: func(ctx *Context) {
			ctx.SetLocal(rleTimeLocalKey, time.Now())
		},
		Key: "time",
		Value: func(ctx *Context, err error) any {
			t, _ := ctx.GetLocal[time.Time](rleTimeLocalKey)
			return t
		},
	}

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

	// RleUrl logs the request URL as it was received, path and query, e.g.
	// "/users/42?page=2". A server request carries no scheme or host, so the
	// value is [RlePath] plus the query string, which is absent when the
	// request has none. See [Context.GetUrl].
	RleUrl = RequestLoggingEntry{
		Key: "url",
		Value: func(ctx *Context, err error) any {
			return ctx.GetUrl().String()
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

	// RlePattern logs the route pattern that matched the request, e.g.
	// "/users/:id", or "" when no route matched. See [Context.GetPattern].
	RlePattern = RequestLoggingEntry{
		Key: "pattern",
		Value: func(ctx *Context, err error) any {
			return ctx.GetPattern()
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

	// RlePid logs the process id of the process that handled the request.
	// Under prefork every child logs its own pid.
	RlePid = RequestLoggingEntry{
		Key: "pid",
		Value: func(ctx *Context, err error) any {
			return os.Getpid()
		},
	}

	// RleUserAgent logs the User-Agent request header, or "" when the request
	// carries none. See [Context.GetHeader].
	RleUserAgent = RequestLoggingEntry{
		Key: "user_agent",
		Value: func(ctx *Context, err error) any {
			return ctx.GetHeader("User-Agent")
		},
	}
)

// AddRequestLoggingEntry appends an entry to the request logging list of
// this app only, so multiple apps can log differently. The app starts from
// the defaults, [RleDuration], [RleRemote], [RleMethod], [RlePath],
// [RleStatus] and [RleError] in that order, or from the list given to
// [AppOptions.WithRequestLoggingEntries], and logs the new entry after the
// ones already in the list.
//
// The key is trimmed. It panics:
//
//   - on an empty or whitespace-only key, or a nil value;
//   - when the key is already in this app's list.
//
// A request logs the entries present when it starts: an entry added while a
// request is in flight, from a handler or from a [RequestLoggingEntryBefore]
// hook, is logged starting with the next request.
func (me *App) AddRequestLoggingEntry(entry RequestLoggingEntry) {
	entry.Key = strings.TrimSpace(entry.Key)
	Assert(entry.Key != "", "key cannot be empty or whitespace")
	Assert(entry.Value != nil, "value func cannot be nil")

	me.rleMutex.Lock()
	defer me.rleMutex.Unlock()

	duplicateIndex := slices.IndexFunc(
		me.options.rle,
		func(e RequestLoggingEntry) bool { return e.Key == entry.Key },
	)
	Assert(
		duplicateIndex == -1,
		fmt.Sprintf("request logging entry key %q is already registered at index %d", entry.Key, duplicateIndex),
	)

	me.options.rle = append(me.options.rle, entry)
}

// logRequest logs the handled request with one attribute per entry in rle,
// the list captured when the request started. err is what the app's
// [ErrorHandler] returned, nil when the handler chain succeeded.
func (me *App) logRequest(ctx *Context, err error, rle []RequestLoggingEntry) {
	args := make([]any, 0, len(rle)*2)
	for _, e := range rle {
		args = append(args, e.Key, e.Value(ctx, err))
	}

	me.options.logger.Info("request handled", args...)
}
