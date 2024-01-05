package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyToKid(t *testing.T) {
	cases := []struct {
		name      string
		key       id16
		wantedKid id16
	}{
		{
			name: "first",
			key: id16{0x28, 0x46, 0x3e, 0x03, 0x04, 0x05, 0x06, 0x07,
				0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
			wantedKid: id16{0x28, 0x80, 0xfe, 0x03, 0x04, 0x05, 0x06,
				0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kid := keyToKid(c.key)
			require.Equal(t, c.wantedKid, kid)
		})
	}
}

func TestKidtoKey(t *testing.T) {
	cases := []struct {
		name      string
		kid       id16
		wantedKey id16
	}{
		{
			name: "first",
			kid: id16{0x28, 0x80, 0xfe, 0x03, 0x04, 0x05, 0x06, 0x07,
				0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
			wantedKey: id16{0x28, 0x46, 0x3e, 0x03, 0x04, 0x05, 0x06,
				0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := kidToKey(c.kid)
			require.Equal(t, c.wantedKey, key)
		})
	}
}

func TestKidFromString(t *testing.T) {
	cases := []struct {
		name      string
		s         string
		wantedKid id16
	}{
		{
			name:      "first",
			s:         "test",
			wantedKid: id16{0x28, 0x80, 0xfe, 0x36, 0xe4, 0x4b, 0xf9, 0xbf, 0x79, 0xd2, 0x75, 0x2e, 0x23, 0x48, 0x18, 0xa5},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kid := kidFromString(c.s)
			require.Equal(t, c.wantedKid, kid)
		})
	}
}

func TestBase64Transform(t *testing.T) {
	cases := []struct {
		name         string
		keyID        string
		wantedBase64 string
	}{
		{
			name:         "IOP5",
			keyID:        "9eb4050d-e44b-4802-932e-27d75083e266",
			wantedBase64: "nrQFDeRLSAKTLifXUIPiZg",
		},
		{
			name:         "KIDstart",
			keyID:        "2880fe36-e44b-f9bf-79d2-752e234818a5",
			wantedBase64: "KID-NuRL-b950nUuI0gYpQ",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hexID := strings.Replace(c.keyID, "-", "", -1)
			kid, err := id16FromHex(hexID)
			require.NoError(t, err)
			b64 := kid.PackBase64()
			require.Equal(t, c.wantedBase64, b64)
		})
	}
}
