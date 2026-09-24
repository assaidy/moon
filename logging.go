package moon

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// logRequest logs the handled request with every registered entry.
// Reserved entries (duration, client, method, path, status, error) are always
// present; custom entries are appended in registration order.
func (me *App) logRequest(ctx *Context) {
	me.requestLoggingEntriesMutex.RLock()
	entries := make([]RequestLoggingEntry, len(me.registeredRequestLoggingEntries))
	copy(entries, me.registeredRequestLoggingEntries)
	me.requestLoggingEntriesMutex.RUnlock()

	args := make([]any, 0, len(entries)*2)
	for _, e := range entries {
		args = append(args, e.Key, e.ValueFunc(ctx))
	}
	me.logger.Info("request handled", args...)
}

// RequestLoggingEntry is a key/value pair logged for every handled request.
// ValueFunc runs per request; keep it cheap and non-blocking.
type RequestLoggingEntry struct {
	Key       string
	ValueFunc RequestLoggingEntryValueFunc
}

// RequestLoggingEntryValueFunc renders a request logging entry value.
type RequestLoggingEntryValueFunc func(ctx *Context) string

// registerReservedRequestLoggingEntries installs the default entries every
// app starts with: duration, client, method, path, status and error.
func (me *App) registerReservedRequestLoggingEntries() {
	me.registeredRequestLoggingEntries = []RequestLoggingEntry{
		{"duration", func(ctx *Context) string { return time.Since(getRequestHandlingStartTimeLocal(ctx)).String() }},
		{"client", func(ctx *Context) string { return ctx.GetRemoteAddress() }},
		{"method", func(ctx *Context) string { return ctx.GetMethod() }},
		{"path", func(ctx *Context) string { return ctx.GetPath() }},
		{"status", func(ctx *Context) string { return strconv.Itoa(ctx.GetStatusCode()) }},
		{"error", func(ctx *Context) string { return fmt.Sprint(getRequestHandlingErrorLocal(ctx)) }},
	}
}

// RegisterRequestLoggingEntry appends a custom entry to this app only, so
// multiple apps can log differently. The key is trimmed; it panics on an
// empty key, a nil value func, or a key that is already registered on this
// app. Reserved keys (duration, client, method, path, status, error) cannot
// be overridden.
func (me *App) RegisterRequestLoggingEntry(entry RequestLoggingEntry) {
	entry.Key = strings.TrimSpace(entry.Key)
	Assert(entry.Key != "", "key cannot be empty or whitespace")
	Assert(entry.ValueFunc != nil, "value func cannot be nil")

	me.requestLoggingEntriesMutex.Lock()
	defer me.requestLoggingEntriesMutex.Unlock()

	Assert(
		slices.IndexFunc(me.registeredRequestLoggingEntries, func(e RequestLoggingEntry) bool { return e.Key == entry.Key }) == -1,
		"request logging entry key is already registered",
	)
	me.registeredRequestLoggingEntries = append(me.registeredRequestLoggingEntries, entry)
}

// RegisterRequestLoggingEntry registers the entry on the current request's
// app. See [App.RegisterRequestLoggingEntry].
//
// Its main use is letting middlewares register their own entries. Register
// once per middleware (e.g. with [sync.Once]): registering the same key
// twice panics.
func (me *Context) RegisterRequestLoggingEntry(entry RequestLoggingEntry) {
	me.app.RegisterRequestLoggingEntry(entry)
}

const requestHandlingStartTimeLocalKey = "moon.request_handling_start_time"

func setRequestHandlingStartTimeLocal(ctx *Context) {
	ctx.SetLocal(requestHandlingStartTimeLocalKey, time.Now())
}

func getRequestHandlingStartTimeLocal(ctx *Context) time.Time {
	return IgnoreSecond(ctx.GetLocal[time.Time](requestHandlingStartTimeLocalKey))
}

const requestHandlingErrorLocalKey = "moon.request_handling_error"

func setRequestHandlingErrorLocal(ctx *Context, err error) {
	if err != nil {
		ctx.SetLocal(requestHandlingErrorLocalKey, err)
	}
}

func getRequestHandlingErrorLocal(ctx *Context) error {
	return IgnoreSecond(ctx.GetLocal[error](requestHandlingErrorLocalKey))
}
