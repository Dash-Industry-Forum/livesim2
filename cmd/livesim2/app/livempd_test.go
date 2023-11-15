// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"math"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/dash-mpd/xml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveMPDStart tests that start parameters are fine for Number and TimelineTime
func TestLiveMPDStart(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	tmpDir := t.TempDir()
	am := newAssetMgr(vodFS, tmpDir, false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset     string
		mpdName   string
		nrMedia   string
		timeMedia string
		timescale int
		startNr   int
	}{
		{
			asset:     "testpic_2s",
			mpdName:   "Manifest_thumbs.mpd",
			nrMedia:   "$RepresentationID$/$Number$.m4s",
			timeMedia: "$RepresentationID$/$Time$.m4s",
			timescale: 1,
			startNr:   2,
		},
		{
			asset:     "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:   "stream.mpd",
			nrMedia:   "1/$Number$.m4s",
			timeMedia: "1/$Time$.m4s",
			timescale: 12800,
			startNr:   0,
		},
		{
			asset:     "testpic_2s",
			mpdName:   "Manifest.mpd",
			nrMedia:   "$RepresentationID$/$Number$.m4s",
			timeMedia: "$RepresentationID$/$Time$.m4s",
			timescale: 1,
			startNr:   7,
		},
		{
			asset:     "testpic_2s",
			mpdName:   "Manifest_imsc1.mpd",
			nrMedia:   "$RepresentationID$/$Number$.m4s",
			timeMedia: "$RepresentationID$/$Time$.m4s",
			timescale: 1,
			startNr:   7,
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		assert.Equal(t, 8000, asset.LoopDurMS)
		cfg := NewResponseConfig()
		cfg.StartNr = Ptr(tc.startNr)
		nowMS := 100_000
		// Number template
		liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, nowMS)
		assert.NoError(t, err)
		assert.Equal(t, "dynamic", *liveMPD.Type)
		assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), liveMPD.AvailabilityStartTime)
		for _, as := range liveMPD.Periods[0].AdaptationSets {
			stl := as.SegmentTemplate
			assert.Nil(t, stl.SegmentTimeline)
			assert.Equal(t, uint32(tc.startNr), *stl.StartNumber)
			tcMedia := tc.nrMedia
			if as.ContentType == "image" {
				tcMedia = strings.Replace(tc.nrMedia, ".m4s", ".jpg", 1)
			}
			assert.Equal(t, tcMedia, stl.Media)
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
			switch as.ContentType {
			case "video":
				require.Greater(t, stl.SegmentTimeline.S[0].R, 0)
				fallthrough
			case "audio", "text":
				assert.Nil(t, stl.StartNumber)
				assert.Equal(t, tc.timeMedia, stl.Media)
			case "image":
				tcMedia := strings.Replace(tc.nrMedia, ".m4s", ".jpg", 1)
				assert.Equal(t, tcMedia, stl.Media)
				assert.Equal(t, tc.startNr, int(*stl.StartNumber))
			default:
				t.Errorf("unknown content type %s", as.ContentType)
			}
		}
		assert.Equal(t, 1, len(liveMPD.UTCTimings))
	}
}

func TestLiveMPDWithTimeSubs(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false)
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

// TestSegmentTimes checks that the right number of entries are in the SegmentTimeline
func TestSegmentTimes(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset      string
		mpdName    string
		useTime    bool
		startTimeS int
		endTimeS   int
	}{
		{
			asset:      "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:    "stream.mpd",
			useTime:    true,
			startTimeS: 80,
			endTimeS:   88,
		},
		{
			asset:      "testpic_2s",
			mpdName:    "Manifest.mpd",
			useTime:    true,
			startTimeS: 80,
			endTimeS:   88,
		},
		{
			asset:      "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:    "stream.mpd",
			useTime:    false,
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
		if tc.useTime {
			cfg.SegTimelineFlag = true
		} else {
			cfg.SegTimelineNrFlag = true
		}
		for nowS := tc.startTimeS; nowS < tc.endTimeS; nowS++ {
			nowMS := nowS * 1000
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, nowMS)
			wantedStartNr := (nowS - 62) / 2 // Sliding window of 60s + one segment
			assert.NoError(t, err)
			for _, as := range liveMPD.Periods[0].AdaptationSets {
				if !tc.useTime {
					assert.Equal(t, wantedStartNr, int(*as.SegmentTemplate.StartNumber))
				}
				stl := as.SegmentTemplate
				nrSegs := 0
				for _, s := range stl.SegmentTimeline.S {
					nrSegs += s.R + 1
				}
				assert.True(t, 29 <= nrSegs && nrSegs <= 32, "nr segments in interval 29 <= x <= 32")
			}
		}
	}
}

// TestLastAvailableSegment tests that the last available segment is correct including low latency.
func TestLastAvailableSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	tmpDir := t.TempDir()
	am := newAssetMgr(vodFS, tmpDir, true)
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
			nowMS:                    3_600_501,
			wantedSegNr:              1800,
		},
		{
			desc:                     "Timeline with $Time$ 1hour+1s with chunkdur 0.5",
			asset:                    "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                  "stream.mpd",
			segTimelineTime:          false,
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
			} else {
				cfg.SegTimelineNrFlag = true
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
			for _, as := range mpd.Periods[0].AdaptationSets {
				atoMS, err := setOffsetInAdaptationSet(cfg, asset, as)
				if tc.wantedErr != "" {
					require.EqualError(t, err, tc.wantedErr)
				} else {
					require.NoError(t, err)
					r := as.Representations[0] // Assume that any representation will be fine inside AS
					se := asset.generateTimelineEntries(r.Id, wTimes, atoMS)
					assert.Equal(t, tc.wantedSegNr, se.lsi.nr)
				}
			}
		})
	}
}

func TestPublishTime(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	tmpDir := t.TempDir()
	am := newAssetMgr(vodFS, tmpDir, false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc                   string
		asset                  string
		mpdName                string
		segTimelineTime        bool
		availabilityStartTime  int
		availabilityTimeOffset float64
		nowMS                  int
		wantedPublishTime      string
	}{
		{
			desc:                  "Timeline with $Time$ 1s after start. No segments",
			asset:                 "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:               "stream.mpd",
			segTimelineTime:       true,
			availabilityStartTime: 0,
			nowMS:                 0,
			wantedPublishTime:     "1970-01-01T00:00:00Z",
		},
		{
			desc:                  "Timeline with $Time$ 1s after start. No segments",
			asset:                 "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:               "stream.mpd",
			segTimelineTime:       true,
			availabilityStartTime: 1682341800,
			nowMS:                 1682341801_000,
			wantedPublishTime:     "2023-04-24T13:10:00Z",
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
			desc:                   "Timeline with $Time$ 3s, ato=1.5, 1 1/4 segments available",
			asset:                  "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName:                "stream.mpd",
			segTimelineTime:        true,
			availabilityTimeOffset: 1.5,
			availabilityStartTime:  1682341800,
			nowMS:                  1682341803_000,
			wantedPublishTime:      "2023-04-24T13:10:02.5Z",
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
			cfg.StartTimeS = tc.availabilityStartTime
			if tc.segTimelineTime {
				cfg.SegTimelineFlag = true
			}
			if tc.availabilityTimeOffset > 0 {
				cfg.AvailabilityTimeOffsetS = tc.availabilityTimeOffset
				cfg.ChunkDurS = Ptr(2 - tc.availabilityTimeOffset)
				cfg.AvailabilityTimeCompleteFlag = false
			}
			err := verifyAndFillConfig(cfg, tc.nowMS)
			require.NoError(t, err)
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			assert.NoError(t, err)
			assert.Equal(t, m.ConvertToDateTimeS(int64(tc.availabilityStartTime)), liveMPD.AvailabilityStartTime)
			assert.Equal(t, m.DateTime(tc.wantedPublishTime), liveMPD.PublishTime)
		})
	}
}

func TestNormalAvailabilityTimeOffset(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc                  string
		asset                 string
		mpdName               string
		ato                   string
		nowMS                 int
		availabilityStartTime int
		segTimelineTime       bool
		wantedAtoVal          float64
		wantedErr             string
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
			wantedErr:       "infinite availabilityTimeOffset for SegmentTimeline",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			cfg := NewResponseConfig()
			cfg.StartTimeS = tc.availabilityStartTime
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
	am := newAssetMgr(vodFS, "", false)
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
			err := verifyAndFillConfig(cfg, tc.nowMS)
			require.NoError(t, err)
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			assert.NoError(t, err)
			assert.Equal(t, m.DateTime(tc.wantedPublishTime), liveMPD.PublishTime)
			assert.Equal(t, tc.wantedUTCTimings, len(liveMPD.UTCTimings))
		})
	}
}

type segTiming struct {
	t, d int
}

func segTimingsFromS(ss []*m.S) []segTiming {
	res := make([]segTiming, 0, len(ss))
	t := int(*ss[0].T)
	for _, s := range ss {
		d := int(s.D)
		for i := 0; i <= int(s.R); i++ {
			res = append(res, segTiming{t, d})
			t += d
		}
	}
	return res
}

func TestAudioSegmentTimeFollowsVideo(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc                  string
		asset                 string
		mpdName               string
		nowMS                 int
		timeShiftBufferDepthS int
		mpdStlType            string
		wantedVideoTimescale  int
		wantedAudioTimescale  int
		wantedVideoSegTimings []segTiming
		wantedAudioSegTimings []segTiming
		wantedErr             string
	}{
		{
			desc:                  "1-min periods with timelineTime",
			asset:                 "testpic_2s",
			mpdName:               "Manifest.mpd",
			nowMS:                 1001_000,
			timeShiftBufferDepthS: 8,
			mpdStlType:            "timelineTime",
			wantedVideoTimescale:  90000,
			wantedAudioTimescale:  48000,
			wantedVideoSegTimings: []segTiming{{t: 89100000, d: 180000}, {89280000, 180000}, {89460000, 180000}, {89640000, 180000}, {89820000, 180000}},
			wantedAudioSegTimings: []segTiming{{t: 47520768, d: 95232}, {47616000, 96256}, {47712256, 96256}, {47808512, 96256}, {47904768, 95232}},
			wantedErr:             "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			cfg := NewResponseConfig()
			cfg.TimeShiftBufferDepthS = Ptr(tc.timeShiftBufferDepthS)
			switch tc.mpdStlType {
			case "timelineTime":
				cfg.SegTimelineFlag = true
			case "timelineNumber":
				cfg.SegTimelineNrFlag = true
			default: // $Number$
				// no flag
			}
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			if tc.wantedErr != "" {
				assert.EqualError(t, err, tc.wantedErr)
				return
			}
			assert.NoError(t, err)
			adaptationSets := orderAdaptationSetsByContentType(liveMPD.Periods[0].AdaptationSets)
			for _, as := range adaptationSets {
				assert.NotNil(t, as.SegmentTemplate, "segment template")
				stl := as.SegmentTemplate.SegmentTimeline
				assert.NotNil(t, stl, "segment timeline")
				gotSegTimings := segTimingsFromS(stl.S)
				switch as.ContentType {
				case "video":
					require.Equal(t, tc.wantedVideoTimescale, int(*as.SegmentTemplate.Timescale), "video timescale")
					require.Equal(t, tc.wantedVideoSegTimings, gotSegTimings, "video segment timings")
				case "audio":
					require.Equal(t, tc.wantedAudioTimescale, int(*as.SegmentTemplate.Timescale), "audio timescale")
					require.Equal(t, tc.wantedAudioSegTimings, gotSegTimings, "audio segment timings")
				default:
					t.Errorf("unexpected content type %q", as.ContentType)
				}
			}
			_, _ = liveMPD.Write(os.Stdout, "", false)
		})
	}
}

// TestMultiPeriod tests that period splitting works as expected
func TestMultiPeriod(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		desc                          string
		asset                         string
		mpdName                       string
		nowMS                         int
		nrPeriodsPerHour              int
		mpdStlType                    string
		wantedNrPeriods               int
		wantedStartNrs                []*int  // When applicable. Same for all adaptation sets
		wantedPresentationTimeOffsets [][]int // For each period and adaptation set
		wantedErr                     string
	}{
		{
			desc:                          "1-min periods with timelineTime",
			asset:                         "testpic_2s",
			mpdName:                       "Manifest.mpd",
			nowMS:                         1001_000,
			nrPeriodsPerHour:              60,
			mpdStlType:                    "timelineTime",
			wantedNrPeriods:               2,
			wantedStartNrs:                []*int{nil, nil},
			wantedPresentationTimeOffsets: [][]int{{43200000, 81000000}, {46080000, 86400000}},
			wantedErr:                     "",
		},
		{
			desc:                          "1-min periods with timelineNumber",
			asset:                         "testpic_2s",
			mpdName:                       "Manifest.mpd",
			nowMS:                         1001_000,
			nrPeriodsPerHour:              60,
			mpdStlType:                    "timelineNumber",
			wantedNrPeriods:               2,
			wantedStartNrs:                []*int{Ptr(469), Ptr(480)},
			wantedPresentationTimeOffsets: [][]int{{43200000, 81000000}, {46080000, 86400000}},
			wantedErr:                     "",
		},
		{
			desc:                          "1-min periods with $Number$",
			asset:                         "testpic_2s",
			mpdName:                       "Manifest.mpd",
			nowMS:                         1001_000,
			nrPeriodsPerHour:              60,
			mpdStlType:                    "$Number$",
			wantedNrPeriods:               2,
			wantedStartNrs:                []*int{Ptr(450), Ptr(480)},
			wantedPresentationTimeOffsets: [][]int{{900, 900}, {960, 960}},
			wantedErr:                     "",
		},
		{
			desc:             "1-min periods is not compatible with 8s segments",
			asset:            "testpic_8s",
			mpdName:          "Manifest.mpd",
			nowMS:            1001_000,
			nrPeriodsPerHour: 60,
			mpdStlType:       "$Number$",
			wantedErr:        "splitPeriods: period duration 60s not a multiple of segment duration 8000ms",
		},
		{
			desc:                          "2-min periods with 8s segments",
			asset:                         "testpic_8s",
			mpdName:                       "Manifest.mpd",
			nowMS:                         1001_000,
			nrPeriodsPerHour:              30,
			mpdStlType:                    "$Number$",
			wantedNrPeriods:               2,
			wantedStartNrs:                []*int{Ptr(105), Ptr(120)},
			wantedPresentationTimeOffsets: [][]int{{40320000, 12902400}, {46080000, 14745600}},
			wantedErr:                     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			cfg := NewResponseConfig()
			cfg.PeriodsPerHour = Ptr(tc.nrPeriodsPerHour)
			switch tc.mpdStlType {
			case "timelineTime":
				cfg.SegTimelineFlag = true
			case "timelineNumber":
				cfg.SegTimelineNrFlag = true
			default: // $Number$
				// no flag
			}
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, tc.nowMS)
			if tc.wantedErr != "" {
				assert.EqualError(t, err, tc.wantedErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.wantedNrPeriods, len(liveMPD.Periods))
			for pNr, p := range liveMPD.Periods {
				for asNr, as := range p.AdaptationSets {
					stl := as.SegmentTemplate
					if tc.wantedStartNrs[pNr] == nil {
						assert.Nil(t, stl.StartNumber)
					} else {
						assert.Equal(t, *tc.wantedStartNrs[pNr], int(*stl.StartNumber), "startNumber in period %d, AS %d", pNr, asNr)
					}
					assert.Equal(t, tc.wantedPresentationTimeOffsets[pNr][asNr], int(*stl.PresentationTimeOffset))
				}
			}
		})
	}
}

func TestRelStartStopTimeIntoLocation(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		url            string
		nowMS          int
		wantedLocation string
		scheme         string
		host           string
	}{
		{
			url:            "/livesim2/startrel_-20/mup_3/stoprel_20/testpic_2s/Manifest.mpd",
			nowMS:          1_000_000,
			wantedLocation: "http://localhost:8888/livesim2/start_980/mup_3/stop_1020/testpic_2s/Manifest.mpd",
			host:           "http://localhost:8888",
		},
	}

	for _, c := range cases {
		cfg, err := processURLCfg(c.url, c.nowMS)
		require.NoError(t, err)
		cfg.SetHost(c.host, nil)
		contentPart := cfg.URLContentPart()
		asset, ok := am.findAsset(contentPart)
		require.True(t, ok)
		_, mpdName := path.Split(contentPart)
		liveMPD, err := LiveMPD(asset, mpdName, cfg, c.nowMS)
		require.NoError(t, err)
		require.Equal(t, c.wantedLocation, string(liveMPD.Location[0]), "the right location element is not inserted")
	}
}

func TestFractionalFramerateMPDs(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	tmpDir := t.TempDir()
	am := newAssetMgr(vodFS, tmpDir, false)
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
			asset:     "WAVE/vectors/cfhd_sets/14.985_29.97_59.94/t1/2022-10-17",
			mpdName:   "stream_w_beeps.mpd",
			nrMedia:   "$RepresentationID$/$Number$.m4s",
			timeMedia: "$RepresentationID$/$Time$.m4s",
			timescale: 1,
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		assert.Equal(t, 8008, asset.LoopDurMS)
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
			require.Equal(t, 0, int(*stl.StartNumber))
			switch as.ContentType {
			case "video":
				require.Equal(t, 30000, int(*stl.Timescale))
				require.Equal(t, 60060, int(*stl.Duration))
			case "audio":
				require.Equal(t, 48000, int(*stl.Timescale))
				require.Equal(t, 96096, int(*stl.Duration))
			default:
				t.Errorf("unexpected content type %q", as.ContentType)
			}
		}
	}
}
