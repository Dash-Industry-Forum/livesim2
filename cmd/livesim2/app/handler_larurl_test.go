package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLaURLBody(t *testing.T) {
	cases := []struct {
		name            string
		body            string
		expectedHexKeys []id16
	}{
		{
			name:            "dashif-example",
			body:            `{"kids":["nrQFDeRLSAKTLifXUIPiZg"],"type":"temporary"}`,
			expectedHexKeys: []id16{MustKey16FromHex("9eb4050de44b4802932e27d75083e266")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hexKIDs, err := parseLaURLBody([]byte(c.body))
			if err != nil {
				t.Error(err)
			}
			require.Equal(t, c.expectedHexKeys, hexKIDs)
		})
	}
}

func MustKey16FromHex(hexStr string) id16 {
	k, err := id16FromHex(hexStr)
	if err != nil {
		panic(err)
	}
	return k
}

func TestGenerateLAResponse(t *testing.T) {
	cases := []struct {
		name         string
		key          id16
		keyID        id16
		expectedResp LaURLResponse
	}{
		{
			name:  "dashif-example",
			key:   MustKey16FromHex("9eb4050de44b4802932e27d75083e266"),
			keyID: MustKey16FromHex("9eb4050de44b4802932e27d75083e266"),
			expectedResp: LaURLResponse{Type: "temporary",
				Keys: []CCPKey{
					{Kty: "oct",
						K:   "nrQFDeRLSAKTLifXUIPiZg",
						Kid: "nrQFDeRLSAKTLifXUIPiZg"},
				},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := generateLaURLResponse([]keyAndID{{key: c.key, id: c.keyID}})
			require.Equal(t, c.expectedResp, resp)
		})
	}
}
