// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
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
		wantedCfg   *ResponseConfig
		err         string
	}{
		{
			url:         "/livesim/utc_direct-ntp-sntp-httpxsdate-httpiso/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				UTCTimingMethods:             []UTCTimingMethod{"direct", "ntp", "sntp", "httpxsdate", "httpiso"},
			},
			err: "",
		},
		{
			url:         "/livesim/utc_unknown/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: `key="utc", val="unknown" is not a valid UTC timing method`,
		},
		{
			url:         "/livesim/utc_head/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: `key="utc", val="head", UTC timing method "head" not supported`,
		},
		{
			url:         "/livesim/utc_none/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				UTCTimingMethods:             []UTCTimingMethod{"none"},
			},
		},
		{
			url:         "/livesim/tsbd_1/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(1),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim/tsbd_1/tsb_asset/V300.cmfv",
			nowS:        0,
			contentPart: "tsb_asset/V300.cmfv",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(1),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim/tsbd_a/asset.mpd",
			nowS:        0,
			contentPart: "",
			wantedCfg:   nil,
			err:         `key=tsbd, err=strconv.Atoi: parsing "a": invalid syntax`,
		},
		{
			url:         "/livesim/tsbd_1",
			nowS:        0,
			contentPart: "",
			wantedCfg:   nil,
			err:         "no content part",
		},
		{
			url:         "/livesim/timesubsstpp_en,sv/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsStpp:                 []string{"en", "sv"},
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim/segtimeline_1/timesubsstpp_en,sv/asset.mpd",
			nowS:        0,
			contentPart: "",
			wantedCfg:   nil,
			err:         "url config: combination of SegTimeline and generated stpp subtitles not yet supported",
		},
		{
			url:         "/livesim/segtimelinenr_1/asset.mpd",
			nowS:        0,
			contentPart: "",
			wantedCfg:   nil,
			err:         "url config: mpd type SegmentTimeline with Number not yet supported",
		},
		{
			url:         "/livesim/mup_0/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg:   nil,
			err:         "url config: minimumUpdatePeriod must be > 0",
		},
		{
			url:         "/livesim/mup_1/asset.mpd",
			nowS:        0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				MinimumUpdatePeriodS:         Ptr(1),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim/ltgt_2500/asset.mpd",
			nowS:        1000,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				LatencyTargetMS:              Ptr(2500),
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
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
		if c.wantedCfg != nil {
			require.Equal(t, c.wantedCfg, cfg)
		}
	}
}
