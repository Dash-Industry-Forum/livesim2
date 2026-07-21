// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateCC608Config(t *testing.T) {
	cases := []struct {
		desc    string
		val     string
		wanted  *CC608Config
		wantErr string
	}{
		{"simple eng", "CC1-eng", &CC608Config{Channel: "CC1", Lang: "eng"}, ""},
		{"two-letter", "CC1-sv", &CC608Config{Channel: "CC1", Lang: "sv"}, ""},
		{"region subtag", "CC1-en-US", &CC608Config{Channel: "CC1", Lang: "en-US"}, ""},
		{"script subtag", "CC1-zh-Hans", &CC608Config{Channel: "CC1", Lang: "zh-Hans"}, ""},
		{"missing hyphen", "CC1", nil, `timecc608 must be <channel>-<lang>, got "CC1"`},
		{"empty", "", nil, `timecc608 must be <channel>-<lang>, got ""`},
		{"channel CC2", "CC2-eng", nil, `timecc608 channel "CC2" not supported (only CC1)`},
		{"channel word", "SERVICE1-eng", nil, `timecc608 channel "SERVICE1" not supported (only CC1)`},
		{"empty lang", "CC1-", nil, `timecc608 language "" is not a valid RFC-5646 code`},
		{"lang too short", "CC1-e", nil, `timecc608 language "e" is not a valid RFC-5646 code`},
		{"lang digits", "CC1-123", nil, `timecc608 language "123" is not a valid RFC-5646 code`},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got, err := CreateCC608Config(c.val)
			if c.wantErr != "" {
				require.Error(t, err)
				require.Equal(t, c.wantErr, err.Error())
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wanted, got)
		})
	}
}
