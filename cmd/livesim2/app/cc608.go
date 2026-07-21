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
}

// cc608LangRegexp is a light RFC-5646 check: a 2-3 letter primary subtag followed by
// optional hyphen-separated subtags (e.g. eng, swe, en-US, zh-Hans).
var cc608LangRegexp = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// CreateCC608Config parses the value of a "timecc608" URL option.
//
// Grammar: <channel>-<lang>, e.g. CC1-eng.
//   - channel: CEA-608 channel; only CC1 is supported in the first milestone.
//   - lang:    RFC-5646 language code (caption locale + MPD Accessibility value).
func CreateCC608Config(val string) (*CC608Config, error) {
	channel, lang, ok := strings.Cut(val, "-")
	if !ok {
		return nil, fmt.Errorf("timecc608 must be <channel>-<lang>, got %q", val)
	}
	if channel != "CC1" {
		return nil, fmt.Errorf("timecc608 channel %q not supported (only CC1)", channel)
	}
	if !cc608LangRegexp.MatchString(lang) {
		return nil, fmt.Errorf("timecc608 language %q is not a valid RFC-5646 code", lang)
	}
	return &CC608Config{Channel: channel, Lang: lang}, nil
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
