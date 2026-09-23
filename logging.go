package moon

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TEST: test request logging

func (me *App) logRequest(ctx *Context) {
	entries := make([]any, 0, len(registeredRequestLoggingEntries)*2)
	for _, e := range registeredRequestLoggingEntries {
		entries = append(entries, e.key, e.valueFunc(ctx))
	}
	me.logger.Info("request handled", entries...)
}

type requestLoggingEntry struct {
	key       string
	valueFunc requestLoggingEntryValueFunc
}

type requestLoggingEntryValueFunc func(ctx *Context) string

var registeredRequestLoggingEntries = []requestLoggingEntry{
	{"duration", func(ctx *Context) string { return time.Since(getRequestHandlingStartTimeLocal(ctx)).String() }},
	{"client", func(ctx *Context) string { return ctx.GetRemoteAddress() }},
	{"method", func(ctx *Context) string { return ctx.GetMethod() }},
	{"path", func(ctx *Context) string { return ctx.GetPath() }},
	{"status", func(ctx *Context) string { return strconv.Itoa(ctx.GetStatusCode()) }},
	{"error", func(ctx *Context) string { return fmt.Sprint(getRequestHandlingErrorLocal(ctx)) }},
}

var requestLoggingEntriesMutex sync.RWMutex

func RegisterRequestLoggingEntry(key string, valueFunc requestLoggingEntryValueFunc) {
	key = strings.TrimSpace(key)
	Assert(key != "", "key cannot be empty or whitespace")
	Assert(
		slices.IndexFunc(registeredRequestLoggingEntries, func(entry requestLoggingEntry) bool { return entry.key == key }) == -1,
		"request logging entry key is already registered",
	)

	requestLoggingEntriesMutex.RLock()
	registeredRequestLoggingEntries = append(registeredRequestLoggingEntries, requestLoggingEntry{key, valueFunc})
	requestLoggingEntriesMutex.RUnlock()
}

const requestHandlingStartTimeLocalKey = "moon.request_handling_start_time"

func setRequestHandlingStartTimeLocal(ctx *Context) {
	ctx.SetLocal(requestHandlingStartTimeLocalKey, time.Now())
}

func getRequestHandlingStartTimeLocal(ctx *Context) time.Time {
	t, _ := ctx.GetLocal[time.Time](requestHandlingStartTimeLocalKey)
	return t
}

const requestHandlingErrorLocalKey = "moon.request_handling_error"

func setRequestHandlingErrorLocal(ctx *Context, err error) {
	if err != nil {
		ctx.SetLocal(requestHandlingErrorLocalKey, err)
	}
}

func getRequestHandlingErrorLocal(ctx *Context) error {
	err, _ := ctx.GetLocal[error](requestHandlingErrorLocalKey)
	return err
}
