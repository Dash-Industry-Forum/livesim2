// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessURLCfg(t *testing.T) {
	cases := []struct {
		url         string
		nowS        int
		contentPart string
		cfgJSON     string
		err         string
	}{
		{
			url:         "/livesim/tsbd_1/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			cfgJSON: `{
				"StartTimeS": 0,
				"TimeShiftBufferDepthS": 1,
				"StartNr": 0,
				"AvailabilityTimeCompleteFlag": true,
				"TimeSubsDurMS": 900
				}`,
			err: "",
		},
		{
			url:         "/livesim/tsbd_1/tsb_asset/V300.cmfv",
			nowS:        0,
			contentPart: "tsb_asset/V300.cmfv",
			cfgJSON: `{
				"StartTimeS": 0,
				"TimeShiftBufferDepthS": 1,
				"StartNr": 0,
				"AvailabilityTimeCompleteFlag": true,
				"TimeSubsDurMS": 900
				}`,
			err: "",
		},
		{
			url:         "/livesim/tsbd_a/asset.mpd",
			nowS:        0,
			contentPart: "",
			cfgJSON:     "",
			err:         `key=tsbd, err=strconv.Atoi: parsing "a": invalid syntax`,
		},
		{
			url:         "/livesim/tsbd_1",
			nowS:        0,
			contentPart: "",
			cfgJSON:     "",
			err:         "no content part",
		},
		{
			url:         "/livesim/timesubsstpp_en,sv/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			cfgJSON: `{
				"StartTimeS": 0,
				"TimeShiftBufferDepthS": 60,
				"StartNr": 0,
				"AvailabilityTimeCompleteFlag": true,
				"TimeSubsStppLanguages": [
				"en",
				"sv"
				],
				"TimeSubsDurMS": 900
			}`,
			err: "",
		},
		{
			url:         "/livesim/segtimeline_1/timesubsstpp_en,sv/asset.mpd",
			nowS:        0,
			contentPart: "",
			cfgJSON:     "",
			err:         "url config: combination of SegTimeline and generated stpp subtitles not yet supported",
		},
		{
			url:         "/livesim/segtimelinenr_1/asset.mpd",
			nowS:        0,
			contentPart: "",
			cfgJSON:     "",
			err:         "url config: mpd type SegmentTimeline with Number not yet supported",
		},
	}

	for _, c := range cases {
		urlParts := strings.Split(c.url, "/")
		cfg, idx, err := processURLCfg(urlParts, c.nowS)
		if c.err != "" {
			require.Error(t, err, c.url)
			require.Equal(t, c.err, err.Error())
			continue
		}
		assert.NoError(t, err)
		gotContentPart := strings.Join(urlParts[idx:], "/")
		require.Equal(t, c.contentPart, gotContentPart)
		jsonBytes, err := json.MarshalIndent(cfg, "", "")
		assert.NoError(t, err)
		jsonStr := string(jsonBytes)
		wantedJSON := dedent(c.cfgJSON)
		require.Equal(t, wantedJSON, jsonStr)
	}
}

var whitespaceOnly = regexp.MustCompile("\n[ \t]+")

// dendent removes spaces and tabs right after a newline
func dedent(str string) string {
	return whitespaceOnly.ReplaceAllString(str, "\n")
}
