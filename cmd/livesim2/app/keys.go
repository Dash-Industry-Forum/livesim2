package app

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Use the following to get nice starts of kids and keys:
var kidStart = []byte{0x28, 0x80, 0xfe}
var keyStart = []byte{0x28, 0x46, 0x3e}

// id16 is a 128-bit value typically used as key or key ID
type id16 [16]byte

func (k id16) String() string {
	s := hex.EncodeToString(k[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[:8], s[8:12], s[12:16], s[16:20], s[20:])
}

// PackBase64 returns a URL-safe base64-encoded string with trailing padding removed
func (k id16) PackBase64() string {
	a := base64.StdEncoding.EncodeToString(k[:])
	a = strings.ReplaceAll(a, "=", "")
	a = strings.ReplaceAll(a, "+", "-")
	a = strings.ReplaceAll(a, "/", "_")
	return a
}

func unpackBase64(b64 string) string {
	b64 = strings.ReplaceAll(b64, "-", "+")
	b64 = strings.ReplaceAll(b64, "_", "/")
	missing := 4 - len(b64)%4
	if missing != 4 {
		for range missing {
			b64 += "="
		}
	}
	return b64
}

// sliceToId16 converts a slice with 16 bytes to id16
func sliceToId16(in []byte) id16 {
	var i16 id16
	copy(i16[:], in)
	return i16
}

/*
// id16ToSlice converts id16 to slice with 16 bytes
func id16ToSlice(in id16) []byte {
	i16 := make([]byte, 16)
	copy(i16, in[:])
	return i16
}

*/

// id16FromTruncatedBase64 returns a Key16 from a base64-encoded string after unpacking
func id16FromTruncatedBase64(b64 string) (id16, error) {
	b64 = unpackBase64(b64)
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return id16{}, err
	}
	if len(b) != 16 {
		return id16{}, fmt.Errorf("decoded key is not 16 bytes")
	}
	return id16(b), err
}

func id16FromBase64(b64 string) (id16, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return id16{}, err
	}
	if len(b) != 16 {
		return id16{}, fmt.Errorf("decoded key is not 16 bytes")
	}
	return id16(b), err
}

func id16FromHex(hexStr string) (id16, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return id16{}, err
	}
	if len(b) != 16 {
		return id16{}, fmt.Errorf("decoded key is not 16 bytes")
	}
	return id16(b), err
}

func keyToKid(key id16) (kid id16) {
	copy(kid[:], key[:])
	for i := range 3 {
		if key[i] != keyStart[i] {
			panic("key does not start with 3 key bytes")
		}
		kid[i] = kidStart[i]
	}
	return kid
}

func kidToKey(kid id16) (key id16) {
	copy(key[:], kid[:])
	for i := range 3 {
		if kid[i] != kidStart[i] {
			panic("keyID does not start with 3 k i d bytes")
		}
		key[i] = keyStart[i]
	}
	return key
}

func kidFromString(s string) id16 {
	c := md5.New()
	c.Sum([]byte(s))
	o := make([]byte, 16)
	n, err := c.Write(o)
	if err != nil {
		panic(err)
	}
	if n != 16 {
		panic("md5 did not write 16 bytes")
	}
	copy(o[:], c.Sum(nil))
	for i := range 3 {
		o[i] = kidStart[i]
	}
	k := id16(o)
	return k
}
