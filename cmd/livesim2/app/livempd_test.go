package app

import (
	"fmt"
	"os"
	"testing"

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
