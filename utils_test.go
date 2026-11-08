package moon

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssert(t *testing.T) {
	t.Run("true does not panic", func(t *testing.T) {
		require.NotPanics(t, func() { Assert(true) })
		require.NotPanics(t, func() { Assert(true, "msg") })
	})

	t.Run("false panics with default message", func(t *testing.T) {
		require.PanicsWithValue(t, "assertion failed", func() { Assert(false) })
	})

	t.Run("false panics with custom message", func(t *testing.T) {
		require.PanicsWithValue(t, "boom", func() { Assert(false, "boom") })
	})
}

func TestIsNilValue(t *testing.T) {
	t.Run("nil interface", func(t *testing.T) {
		var v any
		require.True(t, IsNilValue(v))
		require.True(t, IsNilValue(nil))
	})

	t.Run("typed nils", func(t *testing.T) {
		var ch chan int
		var fn func()
		var m map[string]int
		var p *int
		var s []int
		require.True(t, IsNilValue(ch))
		require.True(t, IsNilValue(fn))
		require.True(t, IsNilValue(m))
		require.True(t, IsNilValue(p))
		require.True(t, IsNilValue(s))
		require.True(t, IsNilValue(any(ch)))
	})

	t.Run("non-nil nillable", func(t *testing.T) {
		require.False(t, IsNilValue(make(chan int)))
		require.False(t, IsNilValue(func() {}))
		require.False(t, IsNilValue(map[string]int{}))
		require.False(t, IsNilValue(new(int)))
		require.False(t, IsNilValue([]int{}))
	})

	t.Run("non-nillable", func(t *testing.T) {
		require.False(t, IsNilValue(0))
		require.False(t, IsNilValue(""))
		require.False(t, IsNilValue(false))
		require.False(t, IsNilValue(struct{}{}))
	})
}

func TestGenerateSecureToken(t *testing.T) {
	testCases := []struct {
		name           string
		length         []int
		wantEncodedLen int
		wantPanic      bool
	}{
		{name: "default", length: nil, wantEncodedLen: 43},
		{name: "explicit default", length: []int{32}, wantEncodedLen: 43},
		{name: "custom unpadded", length: []int{24}, wantEncodedLen: 32},
		{name: "custom padded dropped", length: []int{16}, wantEncodedLen: 22},
		{name: "multiple lengths panic", length: []int{1, 2}, wantPanic: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantPanic {
				require.Panics(t, func() { GenerateSecureToken(tc.length...) })
				return
			}

			token := GenerateSecureToken(tc.length...)
			require.Len(t, token, tc.wantEncodedLen)
			require.NotContains(t, token, "=")
			require.True(t, !strings.ContainsAny(token, "+/"), "token must be URL-safe")

			raw, err := base64.RawURLEncoding.DecodeString(token)
			require.NoError(t, err)
			n := 32
			if len(tc.length) > 0 {
				n = tc.length[0]
			}
			require.Len(t, raw, n)

			require.NotEqual(t, token, GenerateSecureToken(tc.length...))
		})
	}
}

func TestGetEnv(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("MOON_TEST_ENV", "value")

		value, ok := GetEnv("MOON_TEST_ENV")
		require.True(t, ok)
		require.Equal(t, "value", value)
	})

	t.Run("set to empty", func(t *testing.T) {
		t.Setenv("MOON_TEST_ENV", "")

		value, ok := GetEnv("MOON_TEST_ENV")
		require.True(t, ok)
		require.Empty(t, value)
	})

	t.Run("set ignores fallback", func(t *testing.T) {
		t.Setenv("MOON_TEST_ENV", "value")

		value, ok := GetEnv("MOON_TEST_ENV", "fallback")
		require.True(t, ok)
		require.Equal(t, "value", value)
	})

	t.Run("missing without fallback", func(t *testing.T) {
		value, ok := GetEnv("MOON_TEST_ENV_MISSING")
		require.False(t, ok)
		require.Empty(t, value)
	})

	t.Run("missing with fallback", func(t *testing.T) {
		value, ok := GetEnv("MOON_TEST_ENV_MISSING", "fallback")
		require.False(t, ok)
		require.Equal(t, "fallback", value)
	})

	t.Run("multiple fallbacks panic", func(t *testing.T) {
		require.PanicsWithValue(t, "at most one fallback may be given", func() {
			GetEnv("MOON_TEST_ENV_MISSING", "a", "b")
		})
	})
}

func TestMustGetEnv(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("MOON_TEST_ENV", "value")

		require.Equal(t, "value", MustGetEnv("MOON_TEST_ENV"))
	})

	t.Run("set to empty", func(t *testing.T) {
		t.Setenv("MOON_TEST_ENV", "")

		require.Empty(t, MustGetEnv("MOON_TEST_ENV"))
	})

	t.Run("missing panics", func(t *testing.T) {
		require.PanicsWithValue(t, "missing env var: MOON_TEST_ENV_MISSING", func() {
			MustGetEnv("MOON_TEST_ENV_MISSING")
		})
	})
}
