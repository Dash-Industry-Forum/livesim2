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
		// No mode field means paint-on, the mode whose segments are independent.
		{"simple eng", "CC1-eng", &CC608Config{Channel: "CC1", Lang: "eng", Mode: CC608PaintOn}, ""},
		{"swe", "CC1-swe", &CC608Config{Channel: "CC1", Lang: "swe", Mode: CC608PaintOn}, ""},
		{"explicit paint", "CC1-eng-paint", &CC608Config{Channel: "CC1", Lang: "eng", Mode: CC608PaintOn}, ""},
		{"pop-on", "CC1-eng-pop", &CC608Config{Channel: "CC1", Lang: "eng", Mode: CC608PopOn}, ""},
		{"pop-on self-contained", "CC1-eng-pop-sc",
			&CC608Config{Channel: "CC1", Lang: "eng", Mode: CC608PopOn, SelfContained: true}, ""},
		{"roll-up 2 rows", "CC1-eng-roll2",
			&CC608Config{Channel: "CC1", Lang: "eng", Mode: CC608RollUp, RollUpRows: 2}, ""},
		{"roll-up 3 rows", "CC1-eng-roll3",
			&CC608Config{Channel: "CC1", Lang: "eng", Mode: CC608RollUp, RollUpRows: 3}, ""},
		// The language is a single token, so it never swallows the fields after it.
		{"pop-on lang pop", "CC1-pop-pop", &CC608Config{Channel: "CC1", Lang: "pop", Mode: CC608PopOn}, ""},
		{"missing hyphen", "CC1", nil, `timecc608 must be <channel>-<lang>[-<mode>[-<modifier>]], got "CC1"`},
		{"empty", "", nil, `timecc608 must be <channel>-<lang>[-<mode>[-<modifier>]], got ""`},
		{"channel CC2", "CC2-eng", nil, `timecc608 channel "CC2" not supported (only CC1)`},
		{"channel word", "SERVICE1-eng", nil, `timecc608 channel "SERVICE1" not supported (only CC1)`},
		// The descriptor value takes the SCTE 214-1 three-letter language form, so DASH's
		// RFC 5646 tags — a two-letter code, a region or script subtag — are not codes here.
		{"empty lang", "CC1-", nil, `timecc608 language "" is not a three-letter ISO 639-2 code`},
		{"two-letter lang", "CC1-sv", nil, `timecc608 language "sv" is not a three-letter ISO 639-2 code`},
		{"lang too short", "CC1-e", nil, `timecc608 language "e" is not a three-letter ISO 639-2 code`},
		{"lang digits", "CC1-123", nil, `timecc608 language "123" is not a three-letter ISO 639-2 code`},
		{"region subtag", "CC1-en-US", nil, `timecc608 language "en" is not a three-letter ISO 639-2 code`},
		{"script subtag", "CC1-zh-Hans", nil, `timecc608 language "zh" is not a three-letter ISO 639-2 code`},
		{"mode without lang", "CC1-pop", nil,
			`timecc608 "CC1-pop": the language comes before the mode, e.g. CC1-eng-pop`},
		{"too many fields", "CC1-eng-pop-sc-sc", nil,
			`timecc608 "CC1-eng-pop-sc-sc" has 4 fields after the channel, at most 3 (<lang>[-<mode>[-<modifier>]])`},
		{"unknown mode", "CC1-eng-xx", nil,
			`timecc608 mode "xx" not supported (known modes: paint, pop, roll2, roll3)`},
		// A bare "sc" was the self-contained field before modes existed; it now needs the
		// mode it modifies rather than becoming a language subtag.
		{"bare sc", "CC1-eng-sc", nil,
			`timecc608 "CC1-eng-sc": "sc" must follow a mode, e.g. CC1-eng-pop-sc`},
		{"unsupported modifier", "CC1-eng-roll3-c", nil, `timecc608 modifier "c" not supported (only "sc")`},
		{"sc on paint", "CC1-eng-paint-sc", nil,
			`timecc608 modifier "sc" applies to mode pop only; paint is self-contained already`},
		{"sc on roll-up", "CC1-eng-roll3-sc", nil,
			`timecc608 modifier "sc" applies to mode pop only; roll is self-contained already`},
		// A roll-up window taller than the caption's base row (3) has no room, and a
		// mode-shaped token is reported rather than read as a language subtag.
		{"roll-up 4 rows", "CC1-eng-roll4", nil,
			`timecc608 mode "roll4" not supported (known modes: paint, pop, roll2, roll3)`},
		{"roll-up no rows", "CC1-eng-roll", nil,
			`timecc608 mode "roll" not supported (known modes: paint, pop, roll2, roll3)`},
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
