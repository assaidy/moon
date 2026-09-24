package moon

import (
	"crypto/rand"
	"encoding/base64"
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

// IgnoreFirst returns the second value, dropping the first.
func IgnoreFirst[T1, T2 any](_ T1, a2 T2) T2 {
	return a2
}

// IgnoreSecond returns the first value, dropping the second.
func IgnoreSecond[T1, T2 any](a1 T1, _ T2) T1 {
	return a1
}
