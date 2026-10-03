package moon

import (
	"crypto/rand"
	"encoding/base64"
	"reflect"
)

// Object is shorthand for building response bodies (see [Context.WriteAs]).
//
// Example:
//
//	return ctx.WriteAs(http.StatusOK, CodecJson, Object{"hello": "world"})
type Object map[string]any

// Assert panics when condition is false, with message when given.
func Assert(condition bool, message ...string) {
	if !condition {
		if len(message) > 0 {
			panic(message[0])
		}
		panic("assertion failed")
	}
}

// IsNilValue reports whether value is nil, including typed nils.
// It returns true for a nil interface and for nil chans, funcs, maps,
// pointers, unsafe pointers, interfaces and slices. All other values,
// including zero values like 0 or "", return false.
func IsNilValue(value any) bool {
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

// GenerateSecureToken returns a cryptographically random token encoded with
// [base64.RawURLEncoding] (URL-safe, unpadded), suitable for session IDs,
// CSRF tokens and similar secrets.
//
// Called with no arguments it uses 32 random bytes (43 encoded characters).
// An optional length overrides the random byte count; at most one may be
// given. The default length takes a stack-allocated fast path.
func GenerateSecureToken(length ...int) string {
	const defaultLength = 32
	// 32 bytes encode to 44 base64 chars minus 1 pad char dropped by RawURLEncoding.
	const fastPathEncodedLength = 43

	n := defaultLength
	if len(length) > 0 {
		Assert(len(length) == 1, "at most one length may be given")
		n = length[0]
	}

	// fast path without heap allocation
	if n == defaultLength {
		var buffer [defaultLength]byte
		src := buffer[:]
		rand.Read(src)

		var encoded [fastPathEncodedLength]byte
		base64.RawURLEncoding.Encode(encoded[:], src)
		return string(encoded[:])
	}

	buffer := make([]byte, n)
	rand.Read(buffer)
	return base64.RawURLEncoding.EncodeToString(buffer)
}

// IsValidHttpMethod reports whether method is a supported HTTP method.
// It is case sensitive and expects the canonical all-caps form
// (e.g. "GET", not "get"). See the Method* constants and [App.Map],
// which normalizes its input with [strings.ToUpper] and [strings.TrimSpace]
// before validating.
func IsValidHttpMethod(method string) bool {
	switch method {
	case MethodGet,
		MethodHead,
		MethodPost,
		MethodPut,
		MethodPatch,
		MethodDelete,
		MethodConnect,
		MethodOptions,
		MethodTrace,
		MethodQuery:
		return true
	}
	return false
}

// IsValidRoutePattern reports whether pattern is a valid [App.Map] route
// pattern. See [App.Map] for the grammar.
func IsValidRoutePattern(pattern string) bool {
	return routePatternRegex.MatchString(pattern)
}

// IsValidMiddlewarePattern reports whether pattern is a valid [App.Use]
// middleware pattern. See [App.Use] for the grammar.
func IsValidMiddlewarePattern(pattern string) bool {
	return middlewarePatternRegex.MatchString(pattern)
}

// AreRouteParamNamesUnique reports whether all ":param" names in a route
// pattern are distinct. [App.Map] panics on duplicates.
func AreRouteParamNamesUnique(pattern string) bool {
	paramNames := routeParamRegex.FindAllStringSubmatch(pattern, -1)
	seen := make(map[string]struct{}, len(paramNames))
	for _, match := range paramNames {
		if _, ok := seen[match[1]]; ok {
			return false
		}
		seen[match[1]] = struct{}{}
	}
	return true
}
