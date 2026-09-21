package moon

import (
	"reflect"
)

// Map is shorthand for building response bodies (see [Context.WriteAs]).
//
// Example:
//
//	return ctx.WriteAs(http.StatusOK, CodecJson, Map{"hello": "world"})
type Map map[string]any

// Assert panics when condition is false, with message when given.
func Assert(condition bool, message ...string) {
	if !condition {
		if len(message) > 0 {
			panic(message[0])
		}
		panic("assertion failed")
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Map,
		reflect.Pointer,
		reflect.UnsafePointer,
		reflect.Interface,
		reflect.Slice:
		return v.IsNil()
	}

	return false
}
