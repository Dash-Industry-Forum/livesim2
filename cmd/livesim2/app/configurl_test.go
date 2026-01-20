// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessURLCfg(t *testing.T) {
	cases := []struct {
		url         string
		nowMS       int
		contentPart string
		wantedCfg   *ResponseConfig
		err         string
	}{
		{
			url:         "/livesim2/utc_direct-ntp-sntp-httpxsdate-httpiso-head/asset/x.mpd",
			nowMS:       0,
			contentPart: "asset/x.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "utc_direct-ntp-sntp-httpxsdate-httpiso-head", "asset", "x.mpd"},
				URLContentIdx:                3,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				UTCTimingMethods:             []UTCTimingMethod{"direct", "ntp", "sntp", "httpxsdate", "httpiso", "head"},
			},
			err: "",
		},
		{
			url:         "/livesim2/utc_unknown/asset.mpd",
			nowMS:       0,
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
			url:         "/livesim2/utc_none/asset.mpd",
			nowMS:       0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "utc_none", "asset.mpd"},
				URLContentIdx:                3,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				UTCTimingMethods:             []UTCTimingMethod{"none"},
			},
		},
		{
			url:         "/livesim2/tsbd_1/drm_ezdrm1/asset.mpd",
			nowMS:       0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "tsbd_1", "drm_ezdrm1", "asset.mpd"},
				URLContentIdx:                4,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(1),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				DRM:                          "ezdrm1",
			},
			err: "",
		},
		{
			url:         "/livesim2/tsbd_1/tsb_asset/V300.cmfv",
			nowMS:       0,
			contentPart: "tsb_asset/V300.cmfv",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "tsbd_1", "tsb_asset", "V300.cmfv"},
				URLContentIdx:                3,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(1),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim2/tsbd_a/asset.mpd",
			nowMS:       0,
			contentPart: "",
			wantedCfg:   nil,
			err:         `key=tsbd, err=strconv.Atoi: parsing "a": invalid syntax`,
		},
		{
			url:         "/livesim2/tsbd_1",
			nowMS:       0,
			contentPart: "",
			wantedCfg:   nil,
			err:         "no content part",
		},
		{
			url:         "/livesim2/timesubsstpp_en,sv/asset.mpd",
			nowMS:       0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "timesubsstpp_en,sv", "asset.mpd"},
				URLContentIdx:                3,
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
			url:         "/livesim2/mup_0/asset.mpd",
			nowMS:       0,
			contentPart: "asset.mpd",
			wantedCfg:   nil,
			err:         "url config: minimumUpdatePeriod must be > 0",
		},
		{
			url:         "/livesim2/mup_1/asset.mpd",
			nowMS:       0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "mup_1", "asset.mpd"},
				URLContentIdx:                3,
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
			url:         "/livesim2/ltgt_2500/asset.mpd",
			nowMS:       1000,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "ltgt_2500", "asset.mpd"},
				URLContentIdx:                3,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				LatencyTargetMS:              Ptr(2500),
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim2/segtimeline_1/segtimelinenr_1/asset.mpd",
			nowMS:       0,
			contentPart: "",
			wantedCfg:   nil,
			err:         "url config: SegmentTimelineTime and SegmentTimelineNr cannot be used at same time",
		},
		{
			url:         "/livesim2/periods_60/asset.mpd",
			nowMS:       1000,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "periods_60", "asset.mpd"},
				URLContentIdx:                3,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				PeriodsPerHour:               Ptr(60),
			},
			err: "",
		},
		{
			url:         "/livesim2/periods_60/asset.mpd",
			nowMS:       1_000_000,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "periods_60", "asset.mpd"},
				URLContentIdx:                3,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				PeriodsPerHour:               Ptr(60),
			},
			err: "",
		},
		{
			url:         "/livesim2/periods_60/continuous_1/asset.mpd",
			nowMS:       1_000_000,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "periods_60", "continuous_1", "asset.mpd"},
				URLContentIdx:                4,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				PeriodsPerHour:               Ptr(60),
				ContMultiPeriodFlag:          true,
			},
			err: "",
		},
		{
			url:         "/livesim2/startrel_-20/stoprel_20/timeoffset_-1.5/asset.mpd",
			nowMS:       1_000_000,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "startrel_-20", "stoprel_20", "timeoffset_-1.5", "asset.mpd"},
				URLContentIdx:                5,
				StartTimeS:                   980,
				StopTimeS:                    Ptr(1020),
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AddLocationFlag:              true,
				AvailabilityTimeCompleteFlag: true,
				TimeOffsetS:                  Ptr(-1.5),
				TimeSubsDurMS:                defaultTimeSubsDurMS,
			},
			err: "",
		},
		{
			url:         "/livesim2/chunkdurssr_1,0.2/asset.mpd",
			nowMS:       0,
			contentPart: "",
			wantedCfg:   nil,
			err:         "url config: chunkDurSSR requires ssrAS to be configured",
		},
		{
			url:         "/livesim2/ssras_1,2/chunkdurssr_1,0.2/asset.mpd",
			nowMS:       0,
			contentPart: "asset.mpd",
			wantedCfg: &ResponseConfig{
				URLParts:                     []string{"", "livesim2", "ssras_1,2", "chunkdurssr_1,0.2", "asset.mpd"},
				URLContentIdx:                4,
				StartTimeS:                   0,
				TimeShiftBufferDepthS:        Ptr(60),
				StartNr:                      Ptr(0),
				AvailabilityTimeCompleteFlag: true,
				TimeSubsDurMS:                defaultTimeSubsDurMS,
				SSRFlag:                      true,
				SSRAS:                        "1,2",
				ChunkDurSSR:                  "1,0.2",
			},
			err: "",
		},
	}

	for _, c := range cases {
		cfg, err := processURLCfg(c.url, c.nowMS)
		if c.err != "" {
			require.Error(t, err, c.url)
			require.Equal(t, c.err, err.Error())
			continue
		}
		assert.NoError(t, err)
		gotContentPart := cfg.URLContentPart()
		require.Equal(t, c.contentPart, gotContentPart)
		if c.wantedCfg != nil {
			require.Equal(t, c.wantedCfg, cfg)
		}
	}
}

func TestParseLossItvls(t *testing.T) {
	cases := []struct {
		desc        string
		patter      string
		wantedItvls []LossItvls
	}{
		{"empty", "", nil},
		{"up 10s", "u10",
			[]LossItvls{
				{Itvls: []LossItvl{
					{10, lossNo}}},
			},
		},
		{"up20s down3s up12s", "u10d3u12",
			[]LossItvls{
				{Itvls: []LossItvl{
					{10, lossNo},
					{3, loss404},
					{12, lossNo}}},
			},
		},
		{"up 5s", "u5",
			[]LossItvls{
				{Itvls: []LossItvl{
					{5, lossNo}}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			gotItvls, err := CreateAllLossItvls(c.patter)
			require.NoError(t, err)
			require.Equal(t, c.wantedItvls, gotItvls)
		})
	}
}
