// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CC608Config configures on-the-fly injection of in-band CTA-608 (CEA-608) closed
// captions into the AVC/HEVC video elementary stream (livesim2 issue #321).
//
// The caption is generated frame-accurately from wall-clock time and carried as
// cc_data in user_data_registered_itu_t_t35 SEI NAL units spliced into the video
// samples. The first milestone renders the UTC time and segment number on CC1.
type CC608Config struct {
	// Channel is the CEA-608 channel. Only "CC1" is supported in the first milestone.
	Channel string `json:"Channel"`
	// Lang is the caption language as a three-letter ISO 639-2 code (eng, swe). It is
	// used for the CEA-608 Accessibility descriptor value, "<channel>=<lang>", whose
	// SCTE 214-1 scheme (urn:scte:dash:cc:cea-608:2015) is defined in terms of that
	// three-letter form — not DASH's RFC 5646 @lang, which this option never sets.
	Lang string `json:"Lang"`
	// Mode is the CTA-608 caption mode: paint-on (the default), pop-on or roll-up.
	// It decides how a caption reaches the screen, and with it how much a segment
	// depends on its neighbours — see the CC608Mode constants.
	Mode CC608Mode `json:"Mode,omitempty"`
	// RollUpRows is the roll-up window height in rows, 2 or 3, and is only set for
	// CC608RollUp. A cue writes two lines, so a 2-row window keeps no history and a
	// 3-row one keeps the previous cue's bottom line. The caption lines sit on rows
	// 2 and 3 (cc608Line1Row, cc608Line2Row), which makes row 3 the roll-up base row
	// and caps the window at 3: a 4-row window would need a row 0.
	RollUpRows int `json:"RollUpRows,omitempty"`
	// SelfContained keeps every caption inside the segment that carries it, and only
	// applies to CC608PopOn: a cue's pop-on build and its EOC flip both ride the cue's
	// own frames, so no segment depends on its neighbour. The flip then lands
	// ~build-pairs frames into the cue (0.6-0.75 s of a one-second cue), so the caption
	// lags the interval its text names. Unset, a pop-on caption flips on its cue's first
	// frame instead, which is frame-accurate but makes a caption span the segment
	// boundary. Paint-on and roll-up are self-contained by construction.
	SelfContained bool `json:"SelfContained,omitempty"`
}

// CC608Mode is the CTA-608 caption mode used to put a caption on screen. The three
// differ in what a receiver needs in order to render a segment, which is what makes the
// choice interesting for a test stream.
type CC608Mode string

const (
	// CC608PaintOn writes each cue straight onto the displayed screen, two characters
	// per frame, after clearing it. Every cue is self-contained in its own slice, so a
	// segment never depends on a neighbour and a client can start, seek or join
	// anywhere. The caption arrives progressively, complete only for the tail of its
	// cue. This is the default.
	CC608PaintOn CC608Mode = "paint"
	// CC608PopOn builds each cue in non-displayed memory and flips it on with an EOC.
	// The flip rides the cue's first frame, so the caption is displayed over exactly the
	// interval its clock names, at the cost of the build living in the previous segment
	// (see CC608Config.SelfContained for the self-contained variant).
	CC608PopOn CC608Mode = "pop"
	// CC608RollUp types each cue onto the base row of a scrolling window, so earlier
	// cues age upward. The window is reset at the start of every segment, which keeps
	// the segment self-contained in display as well as data.
	CC608RollUp CC608Mode = "roll"
)

// cc608SelfContainedToken is the modifier that asks a pop-on stream to keep every
// caption inside its own segment.
const cc608SelfContainedToken = "sc"

// cc608Modes maps the mode field of a timecc608 value onto a mode and, for roll-up, its
// window height. Roll-up is limited to 2 and 3 rows by the caption's own row placement
// (see CC608Config.RollUpRows).
var cc608Modes = map[string]struct {
	mode CC608Mode
	rows int
}{
	"paint": {CC608PaintOn, 0},
	"pop":   {CC608PopOn, 0},
	"roll2": {CC608RollUp, 2},
	"roll3": {CC608RollUp, 3},
}

// cc608ModeList names the supported mode tokens for an error message. Sorted, so the
// map stays the single source of truth: paint, pop, roll2, roll3.
func cc608ModeList() string {
	tokens := make([]string, 0, len(cc608Modes))
	for t := range cc608Modes {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)
	return "known modes: " + strings.Join(tokens, ", ")
}

// cc608LangRegexp matches a three-letter ISO 639-2 code, the language form the CEA-608
// accessibility descriptor uses (see CC608Config.Lang). Not a check against the ISO 639-2
// register, just the shape, which is what keeps the descriptor value well-formed.
var cc608LangRegexp = regexp.MustCompile(`^[A-Za-z]{3}$`)

// CreateCC608Config parses the value of a "timecc608" URL option.
//
// Grammar: <channel>-<lang>[-<mode>[-<modifier>]], e.g. CC1-eng, CC1-eng-pop-sc or
// CC1-eng-roll3. The fields are positional, so each one is exactly one hyphen-separated
// token:
//   - channel:  CEA-608 channel; only CC1 is supported in the first milestone.
//   - lang:     three-letter ISO 639-2 code for the descriptor value (see
//     CC608Config.Lang). This is the SCTE 214-1 language form and not DASH's RFC 5646
//     @lang, so eng and swe are codes here while en, en-US and zh-Hans are not.
//   - mode:     optional caption mode; paint (the default), pop, roll2 or roll3.
//   - modifier: optional; only "sc" after "pop", which keeps each pop-on caption inside
//     its own segment. Paint-on and roll-up are self-contained anyway.
//
// Because the language is a single token, an unknown mode or modifier is reported rather
// than swallowed as part of it: "CC1-eng-roll4" and "CC1-eng-xx" are errors, not requests
// for a language "eng-roll4". The one value worth a special word is a lone mode token,
// "CC1-pop", where the language is missing rather than named "pop".
func CreateCC608Config(val string) (*CC608Config, error) {
	channel, rest, ok := strings.Cut(val, "-")
	if !ok {
		return nil, fmt.Errorf("timecc608 must be <channel>-<lang>[-<mode>[-<modifier>]], got %q", val)
	}
	if channel != "CC1" {
		return nil, fmt.Errorf("timecc608 channel %q not supported (only CC1)", channel)
	}
	fields := strings.Split(rest, "-")
	if len(fields) > 3 {
		return nil, fmt.Errorf("timecc608 %q has %d fields after the channel, at most 3 "+
			"(<lang>[-<mode>[-<modifier>]])", val, len(fields))
	}
	lang := fields[0]
	// A value that is nothing but a mode token has lost its language rather than named
	// one. Checked before the shape, since "pop" is itself three letters.
	if _, isMode := cc608Modes[lang]; isMode && len(fields) == 1 {
		return nil, fmt.Errorf("timecc608 %q: the language comes before the mode, e.g. %s-eng-%s",
			val, channel, lang)
	}
	if !cc608LangRegexp.MatchString(lang) {
		return nil, fmt.Errorf("timecc608 language %q is not a three-letter ISO 639-2 code", lang)
	}
	mode, rows := CC608PaintOn, 0
	if len(fields) >= 2 {
		m, isMode := cc608Modes[fields[1]]
		if !isMode {
			if fields[1] == cc608SelfContainedToken {
				// A bare "sc" was the self-contained field before modes existed, so name the
				// mode it now modifies rather than just listing the modes.
				return nil, fmt.Errorf("timecc608 %q: %q must follow a mode, e.g. %s-%s-pop-%s",
					val, cc608SelfContainedToken, channel, lang, cc608SelfContainedToken)
			}
			return nil, fmt.Errorf("timecc608 mode %q not supported (%s)", fields[1], cc608ModeList())
		}
		mode, rows = m.mode, m.rows
	}
	selfContained := false
	if len(fields) == 3 {
		if fields[2] != cc608SelfContainedToken {
			return nil, fmt.Errorf("timecc608 modifier %q not supported (only %q)",
				fields[2], cc608SelfContainedToken)
		}
		if mode != CC608PopOn {
			return nil, fmt.Errorf("timecc608 modifier %q applies to mode pop only; %s is self-contained already",
				cc608SelfContainedToken, mode)
		}
		selfContained = true
	}
	return &CC608Config{
		Channel:       channel,
		Lang:          lang,
		Mode:          mode,
		RollUpRows:    rows,
		SelfContained: selfContained,
	}, nil
}

// ParseCC608Config parses a "timecc608" option value, accumulating any error.
func (s *strConvAccErr) ParseCC608Config(key, val string) *CC608Config {
	if s.err != nil {
		return nil
	}
	cfg, err := CreateCC608Config(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return cfg
}
