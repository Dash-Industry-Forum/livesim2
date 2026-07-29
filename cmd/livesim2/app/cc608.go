// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"regexp"
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
	// Lang is the RFC-5646 language code used both for the caption content locale
	// and for the value of the MPD Accessibility descriptor.
	Lang string `json:"Lang"`
	// SelfContained keeps every caption inside the segment that carries it: a cue's
	// pop-on build and its EOC flip both ride the cue's own frames, so no segment
	// depends on its neighbour. The flip then lands ~build-pairs frames into the cue
	// (0.6-0.75 s of a one-second cue), so the caption lags the interval its text
	// names. The default (false) flips each caption on its cue's first frame instead,
	// which is frame-accurate but makes a caption span the segment boundary.
	SelfContained bool `json:"SelfContained,omitempty"`
}

// cc608SelfContainedToken is the optional trailing field of the timecc608 value that
// asks for self-contained segments.
const cc608SelfContainedToken = "sc"

// cc608LangRegexp is a light RFC-5646 check: a 2-3 letter primary subtag followed by
// optional hyphen-separated subtags (e.g. eng, swe, en-US, zh-Hans).
var cc608LangRegexp = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// CreateCC608Config parses the value of a "timecc608" URL option.
//
// Grammar: <channel>-<lang>[-sc], e.g. CC1-eng or CC1-eng-sc.
//   - channel: CEA-608 channel; only CC1 is supported in the first milestone.
//   - lang:    RFC-5646 language code (caption locale + MPD Accessibility value).
//   - sc:      optional; keep captions self-contained per segment (see
//     CC608Config.SelfContained). Omitted, captions flip on their cue's first frame.
//
// Since a language tag may itself carry hyphenated subtags (en-US, zh-Hans), the "sc"
// field is only recognized as such when the value has three or more fields; the lang is
// everything between the channel and it. "CC1-sc" is therefore the language "sc"
// (Sardinian), and "CC1-sc-sc" is that language with self-contained segments. The one
// tag this cannot express is a language whose final subtag is literally "sc".
func CreateCC608Config(val string) (*CC608Config, error) {
	channel, rest, ok := strings.Cut(val, "-")
	if !ok {
		return nil, fmt.Errorf("timecc608 must be <channel>-<lang>[-sc], got %q", val)
	}
	if channel != "CC1" {
		return nil, fmt.Errorf("timecc608 channel %q not supported (only CC1)", channel)
	}
	lang := rest
	selfContained := false
	if pre, last, hasMore := cutLast(rest, "-"); hasMore && last == cc608SelfContainedToken {
		lang, selfContained = pre, true
	}
	if !cc608LangRegexp.MatchString(lang) {
		return nil, fmt.Errorf("timecc608 language %q is not a valid RFC-5646 code", lang)
	}
	return &CC608Config{Channel: channel, Lang: lang, SelfContained: selfContained}, nil
}

// cutLast splits s around the last instance of sep, like strings.Cut around the first.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
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
