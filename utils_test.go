package moon

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
