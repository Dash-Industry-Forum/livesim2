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
		asset   string
		mpdName string
	}{
		{
			asset:   "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName: "stream.mpd",
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
		stl := liveMPD.Periods[0].AdaptationSets[0].SegmentTemplate
		assert.Nil(t, stl.SegmentTimeline)
		assert.Equal(t, uint32(0), *stl.StartNumber)
		assert.Equal(t, "1/$Number$.m4s", stl.Media)
		require.NotNil(t, stl.Duration)
		require.NotNil(t, stl.Timescale)
		assert.Equal(t, 2, int(*stl.Duration)/int(*stl.Timescale))
		// SegmentTimeline with $Time$
		cfg.SegTimelineFlag = true
		liveMPD, err = LiveMPD(asset, tc.mpdName, cfg, nowMS)
		assert.NoError(t, err)
		assert.Equal(t, "dynamic", *liveMPD.Type)
		assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), liveMPD.AvailabilityStartTime)
		stl = liveMPD.Periods[0].AdaptationSets[0].SegmentTemplate
		assert.Equal(t, 30, stl.SegmentTimeline.S[0].R)
		assert.Nil(t, stl.StartNumber)
		assert.Equal(t, "1/$Time$.m4s", stl.Media)
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
	}{
		{
			asset:   "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			mpdName: "stream.mpd",
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
		assert.Equal(t, 3, len(aSets))
		data, err := xml.MarshalIndent(aSets[1], " ", "")
		require.NoError(t, err)
		require.Equal(t, liveSubEn, string(data))
	}
}

var liveSubEn = "" +
	` <AdaptationSetType id="100" lang="en" contentType="text" segmentAlignment="true" mimeType="application/mp4" codecs="stpp">
 <Role schemeIdUri="urn:mpeg:dash:role:2011" value="subtitle"></Role>
 <SegmentTemplate media="$RepresentationID$/$Number$.m4s" initialization="$RepresentationID$/init.mp4" duration="2000" startNumber="0" timescale="1000"></SegmentTemplate>
 <Representation id="timesubs-en" bandwidth="5000" startWithSAP="1"></Representation>
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
