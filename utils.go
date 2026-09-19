package moon

import (
	"reflect"
)

type Map map[string]any

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
