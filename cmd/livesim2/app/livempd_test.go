// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/dash-mpd/xml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveMPD(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset     string
		mpdName   string
		nrMedia   string
		timeMedia string
		timescale int
	}{
		{
			asset:     "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:   "stream.mpd",
			nrMedia:   "1/$Number$.m4s",
			timeMedia: "1/$Time$.m4s",
			timescale: 12800,
		},
		{
			asset:     "testpic_2s",
			mpdName:   "Manifest.mpd",
			nrMedia:   "$RepresentationID$/$Number$.m4s",
			timeMedia: "$RepresentationID$/$Time$.m4s",
			timescale: 1,
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		assert.Equal(t, 8000, asset.LoopDurMS)
		cfg := NewResponseConfig()
		nowMS := 100_000
		// Number template
		liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, nowMS)
		assert.NoError(t, err)
		assert.Equal(t, "dynamic", *liveMPD.Type)
		assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), liveMPD.AvailabilityStartTime)
		for _, as := range liveMPD.Periods[0].AdaptationSets {
			stl := as.SegmentTemplate
			assert.Nil(t, stl.SegmentTimeline)
			assert.Equal(t, uint32(0), *stl.StartNumber)
			assert.Equal(t, tc.nrMedia, stl.Media)
			require.NotNil(t, stl.Duration)
			require.Equal(t, tc.timescale, int(stl.GetTimescale()))
			assert.Equal(t, 2, int(*stl.Duration)/int(stl.GetTimescale()))
		}
		// SegmentTimeline with $Time$
		cfg.SegTimelineFlag = true
		liveMPD, err = LiveMPD(asset, tc.mpdName, cfg, nowMS)
		assert.NoError(t, err)
		assert.Equal(t, "dynamic", *liveMPD.Type)
		assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), liveMPD.AvailabilityStartTime)
		for _, as := range liveMPD.Periods[0].AdaptationSets {
			stl := as.SegmentTemplate
			if as.ContentType == "video" {
				require.Greater(t, stl.SegmentTimeline.S[0].R, 0)
			}
			assert.Nil(t, stl.StartNumber)
			assert.Equal(t, tc.timeMedia, stl.Media)
		}
		assert.Equal(t, 1, len(liveMPD.UTCTimings))
	}
}

func TestLiveMPDWithTimeSubs(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset   string
		mpdName string
		nrMPD   string
	}{
		{
			asset:   "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName: "stream.mpd",
		},
		{
			asset:   "testpic_2s",
			mpdName: "Manifest.mpd",
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		assert.Equal(t, 8000, asset.LoopDurMS)
		cfg := NewResponseConfig()
		cfg.TimeSubsStpp = []string{"en", "sv"}
		nowMS := 100_000
		// Number template
		liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, nowMS)
		assert.NoError(t, err)
		assert.Equal(t, "dynamic", *liveMPD.Type)
		aSets := liveMPD.Periods[0].AdaptationSets
		nrSubsAS := 0
		var firstSubsAS *m.AdaptationSetType
		for _, as := range aSets {
			if as.ContentType == "text" {
				nrSubsAS++
				if firstSubsAS == nil {
					firstSubsAS = as
				}
			}
		}
		data, err := xml.MarshalIndent(firstSubsAS, " ", "")
		require.NoError(t, err)
		require.Equal(t, liveSubEn, string(data))
	}
}

var liveSubEn = "" +
	` <AdaptationSetType id="100" lang="en" contentType="text" segmentAlignment="true" mimeType="application/mp4" codecs="stpp">
 <Role schemeIdUri="urn:mpeg:dash:role:2011" value="subtitle"></Role>
 <SegmentTemplate media="$RepresentationID$/$Number$.m4s" initialization="$RepresentationID$/init.mp4" duration="2000" startNumber="0" timescale="1000"></SegmentTemplate>
 <Representation id="timestpp-en" bandwidth="8000" startWithSAP="1"></Representation>
 </AdaptationSetType>`

func TestSegmentTimes(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset      string
		mpdName    string
		startTimeS int
		endTimeS   int
	}{
		{
			asset:      "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:    "stream.mpd",
			startTimeS: 80,
			endTimeS:   88,
		},
		{
			asset:      "testpic_2s",
			mpdName:    "Manifest.mpd",
			startTimeS: 80,
			endTimeS:   88,
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		assert.Equal(t, 8000, asset.LoopDurMS)
		cfg := NewResponseConfig()
		cfg.SegTimelineFlag = true
		for nowS := tc.startTimeS; nowS < tc.endTimeS; nowS++ {
			nowMS := nowS * 1000
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, nowMS)
			assert.NoError(t, err)
			for _, as := range liveMPD.Periods[0].AdaptationSets {
				stl := as.SegmentTemplate
				nrSegs := 0
				for _, s := range stl.SegmentTimeline.S {
					nrSegs += s.R + 1
				}
				fmt.Println(nrSegs)
				assert.True(t, 29 <= nrSegs && nrSegs <= 32, "nr segments in interval 29 <= x <= 32")
			}
		}
	}
}

func TestLastAvailableSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)
	cases := []struct {
		desc                     string
		asset                    string
		mpdName                  string
		segTimelineTime          bool
		availabilityTimeOffset   float64
		availabilityTimeComplete bool
		nowMS                    int
		wantedSegNr              int
		wantedErr                string
	}{
		{
			desc:                     "Timeline with $Time$ 1hour+1s with chunkdur 0.5",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			segTimelineTime:          true,
			availabilityTimeOffset:   1.5,
			availabilityTimeComplete: false,
			nowMS:                    3_601_000,
			wantedSegNr:              1800,
		},
		{
			desc:                     "Timeline with $Time$ 1s after start. No segments",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			segTimelineTime:          true,
			availabilityTimeComplete: true,
			nowMS:                    0,
			wantedSegNr:              -1,
		},
		{
			desc:                     "Timeline with $Time$ 5s after start, two segment (0, 1)",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			segTimelineTime:          true,
			availabilityTimeComplete: true,
			nowMS:                    5000,
			wantedSegNr:              1,
		},
		{
			desc:                     "Timeline with $Time$ 1hour after segment generation",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			segTimelineTime:          true,
			availabilityTimeComplete: true,
			nowMS:                    3_600_000,
			wantedSegNr:              1799,
		},
		{
			desc:                     "Timeline with $Time$ 1hour+1s after segment generation",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			segTimelineTime:          true,
			nowMS:                    3_601_000,
			availabilityTimeComplete: true,
			wantedSegNr:              1799,
		},
		{
			desc:                     "Timeline with $Time$ and infinite ato => error",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			availabilityTimeOffset:   math.Inf(1),
			availabilityTimeComplete: true,
			segTimelineTime:          true,
			wantedErr:                ErrAtoInfTimeline.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			cfg := NewResponseConfig()
			if tc.segTimelineTime {
				cfg.SegTimelineFlag = true
			}
			cfg.AvailabilityTimeOffsetS = tc.availabilityTimeOffset
			if tc.availabilityTimeOffset > 0 && !tc.availabilityTimeComplete {
				cfg.ChunkDurS = Ptr(2 - tc.availabilityTimeOffset)
				cfg.AvailabilityTimeCompleteFlag = false
			}
			tsbd := m.Duration(60 * time.Second)
			wTimes := calcWrapTimes(asset, cfg, tc.nowMS, tsbd)
			mpd, err := asset.getVodMPD(tc.mpdName)
			require.NoError(t, err)
			as := mpd.Periods[0].AdaptationSets[0]
			lsi, err := adjustAdaptationSetForTimelineTime(cfg, asset, as, wTimes)
			if tc.wantedErr != "" {
				require.EqualError(t, err, tc.wantedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantedSegNr, lsi.nr)
			}
		})
	}
}

func TestPublishTime(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc                   string
		asset                  string
		mpdName                string
		segTimelineTime        bool
		availabilityTimeOffset float64
		nowMS                  int
		wantedPublishTime      string
	}{
		{
			desc:              "Timeline with $Time$ 1s after start. No segments",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			segTimelineTime:   true,
			nowMS:             0,
			wantedPublishTime: "1970-01-01T00:00:00Z",
		},
		{
			desc:                   "Timeline with $Time$ 3s, ato=1.5, 1 1/4 segments available",
			asset:                  "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                "stream.mpd",
			segTimelineTime:        true,
			availabilityTimeOffset: 1.5,
			nowMS:                  3000,
			wantedPublishTime:      "1970-01-01T00:00:02.5Z",
		},
		{
			desc:                   "Timeline with $Time$ 4.25s, ato=1.5, 2 segments available",
			asset:                  "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                "stream.mpd",
			segTimelineTime:        true,
			availabilityTimeOffset: 1.5,
			nowMS:                  4250,
			wantedPublishTime:      "1970-01-01T00:00:02.5Z",
		},
		{
			desc:                   "Timeline with $Time$ 4.5s, ato=1.5, 3 1/4 segments available",
			asset:                  "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                "stream.mpd",
			segTimelineTime:        true,
			availabilityTimeOffset: 1.5,
			nowMS:                  4500,
			wantedPublishTime:      "1970-01-01T00:00:04.5Z",
		},
		{
			desc:              "Timeline with $Time$ 3s after start, one segment",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			segTimelineTime:   true,
			nowMS:             3000,
			wantedPublishTime: "1970-01-01T00:00:02Z",
		},
		{
			desc:              "Timeline with $Time$ 1hour after segment generation",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			segTimelineTime:   true,
			nowMS:             3_600_000,
			wantedPublishTime: "1970-01-01T01:00:00Z",
		},
		{
			desc:              "Timeline with $Time$ 1hour+1s after segment generation",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			segTimelineTime:   true,
			nowMS:             3_601_000,
			wantedPublishTime: "1970-01-01T01:00:00Z",
		},
		{
			desc:              "SegmentTemplate with $Number$, some segments produced",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			segTimelineTime:   false,
			nowMS:             10000,
			wantedPublishTime: "1970-01-01T00:00:00Z",
		},
		{
			desc:              "SegmentTemplate with $Number$, at start",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			segTimelineTime:   false,
			nowMS:             0,
			wantedPublishTime: "1970-01-01T00:00:00Z",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			assert.Equal(t, 8000, asset.LoopDurMS)
			cfg := NewResponseConfig()
			if tc.segTimelineTime {
				cfg.SegTimelineFlag = true
			}
			if tc.availabilityTimeOffset > 0 {
				cfg.AvailabilityTimeOffsetS = tc.availabilityTimeOffset
				cfg.ChunkDurS = Ptr(2 - tc.availabilityTimeOffset)
				cfg.AvailabilityTimeCompleteFlag = false
			}
			err := verifyAndFillConfig(cfg)
			require.NoError(t, err)
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			assert.NoError(t, err)
			assert.Equal(t, m.DateTime(tc.wantedPublishTime), liveMPD.PublishTime)
		})
	}
}

func TestNormalAvailabilityTimeOffset(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc            string
		asset           string
		mpdName         string
		ato             string
		nowMS           int
		segTimelineTime bool
		wantedAtoVal    float64
		wantedErr       string
	}{
		{
			desc:            "number with ato=10",
			asset:           "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:         "stream.mpd",
			ato:             "10",
			nowMS:           100_000,
			segTimelineTime: false,
			wantedAtoVal:    10,
		},
		{
			desc:            "timelines with ato=10",
			asset:           "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:         "stream.mpd",
			ato:             "10",
			nowMS:           100_000,
			segTimelineTime: true,
			wantedAtoVal:    10,
		},
		{
			desc:            "number with ato=inf",
			asset:           "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:         "stream.mpd",
			ato:             "inf",
			nowMS:           100_000,
			segTimelineTime: false,
			wantedAtoVal:    math.Inf(+1),
		},
		{
			desc:            "timelines with ato=inf",
			asset:           "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:         "stream.mpd",
			ato:             "inf",
			nowMS:           100_000,
			segTimelineTime: true,
			wantedErr:       "adjustASForTimelineTime: infinite availabilityTimeOffset for SegmentTimeline",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			cfg := NewResponseConfig()
			cfg.AvailabilityTimeCompleteFlag = true
			cfg.SegTimelineFlag = tc.segTimelineTime
			sc := strConvAccErr{}
			cfg.AvailabilityTimeOffsetS = sc.AtofInf("ato", tc.ato)
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			if tc.wantedErr != "" {
				assert.EqualError(t, err, tc.wantedErr)
				return
			}
			assert.NoError(t, err)
			p := liveMPD.Periods[0]
			for _, as := range p.AdaptationSets {
				segTemplateATO := float64(as.SegmentTemplate.AvailabilityTimeOffset)
				require.Equal(t, tc.wantedAtoVal, segTemplateATO)
			}
		})
	}
}

func TestUTCTiming(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc              string
		asset             string
		mpdName           string
		nowMS             int
		utcTimings        []string
		wantedPublishTime string
		wantedUTCTimings  int
	}{
		{
			desc:              "Default with no UTCTiming",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			nowMS:             50000,
			utcTimings:        nil,
			wantedPublishTime: "1970-01-01T00:00:50Z",
			wantedUTCTimings:  1,
		},
		{
			desc:              "Default with no UTCTiming",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			nowMS:             50000,
			utcTimings:        []string{"none"},
			wantedPublishTime: "1970-01-01T00:00:50Z",
			wantedUTCTimings:  0,
		},
		{
			desc:              "Default with no UTCTiming",
			asset:             "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:           "stream.mpd",
			nowMS:             50000,
			utcTimings:        []string{"httpiso", "ntp", "sntp"},
			wantedPublishTime: "1970-01-01T00:00:50Z",
			wantedUTCTimings:  3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			assert.Equal(t, 8000, asset.LoopDurMS)
			cfg := NewResponseConfig()
			cfg.SegTimelineFlag = true
			for _, ut := range tc.utcTimings {
				cfg.UTCTimingMethods = append(cfg.UTCTimingMethods, UTCTimingMethod(ut))
			}
			err := verifyAndFillConfig(cfg)
			require.NoError(t, err)
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			assert.NoError(t, err)
			assert.Equal(t, m.DateTime(tc.wantedPublishTime), liveMPD.PublishTime)
			assert.Equal(t, tc.wantedUTCTimings, len(liveMPD.UTCTimings))
		})
	}
}
