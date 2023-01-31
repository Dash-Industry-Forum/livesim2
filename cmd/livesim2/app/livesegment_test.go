package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset          string
		initialization string
		media          string
		mediaTimescale int
	}{
		{
			asset:          "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			initialization: "1/init.mp4",
			media:          "1/$NrOrTime$.m4s",
			mediaTimescale: 12800,
		},
		{
			asset:          "testpic_2s",
			initialization: "V300/init.mp4",
			media:          "V300/$NrOrTime$.m4s",
			mediaTimescale: 90000,
		},
		{
			asset:          "testpic_2s",
			initialization: "A48/init.mp4",
			media:          "A48/$NrOrTime$.m4s",
			mediaTimescale: 48000,
		},
	}
	for _, tc := range cases {
		for _, mpdType := range []string{"Number", "TimelineTime"} {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			require.NoError(t, err)
			cfg := NewResponseConfig()
			switch mpdType {
			case "Number":
			case "TimelineTime":
				cfg.SegTimelineFlag = true
			case "TimelineNumber":
				cfg.SegTimelineNrFlag = true
			}
			nowMS := 100_000
			rr := httptest.NewRecorder()
			wroteInit, err := writeInitSegment(rr, vodFS, asset, "2/init.mp4")
			require.False(t, wroteInit)
			require.NoError(t, err)
			rr = httptest.NewRecorder()
			wroteInit, err = writeInitSegment(rr, vodFS, asset, tc.initialization)
			require.True(t, wroteInit)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rr.Code)
			seg := rr.Body.Bytes()
			sr := bits.NewFixedSliceReader(seg)
			mp4d, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			mediaTimescale := int(mp4d.Moov.Trak.Mdia.Mdhd.Timescale)
			assert.Equal(t, tc.mediaTimescale, mediaTimescale)
			media := tc.media
			nr := 40
			mediaTime := nr * 2 * mediaTimescale
			switch mpdType {
			case "Number", "TimelineNumber":
				media = strings.Replace(media, "$NrOrTime$", fmt.Sprintf("%d", nr), -1)
			default: // "TimelineTime":
				media = strings.Replace(media, "$NrOrTime$", fmt.Sprintf("%d", mediaTime), -1)
			}
			seg, err = LiveSegment(vodFS, asset, cfg, media, nowMS)
			require.NoError(t, err)
			sr = bits.NewFixedSliceReader(seg)
			mp4d, err = mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			bdt := mp4d.Segments[0].Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			require.Equal(t, mediaTime, int(bdt))
		}
	}
}

func TestWriteChunkedSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)
	cfg := NewResponseConfig()
	cfg.AvailabilityTimeCompleteFlag = false
	cfg.AvailabilityTimeOffsetS = Ptr(7.0)
	log, err := logging.InitZerolog("debug", "discard")
	require.NoError(t, err)

	cases := []struct {
		asset          string
		initialization string
		media          string
		mediaTimescale int
	}{
		{
			asset:          "testpic_8s",
			media:          "V300/$NrOrTime$.m4s",
			mediaTimescale: 15360,
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		nowMS := 86_000
		rr := httptest.NewRecorder()
		segmentPart := strings.Replace(tc.media, "$NrOrTime$", "10", 1)
		mediaTime := 80 * tc.mediaTimescale
		err := writeChunkedSegment(context.Background(), rr, log, cfg, vodFS, asset, segmentPart, nowMS)
		require.NoError(t, err)
		seg := rr.Body.Bytes()
		sr := bits.NewFixedSliceReader(seg)
		mp4d, err := mp4.DecodeFileSR(sr)
		require.NoError(t, err)
		bdt := mp4d.Segments[0].Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
		require.Equal(t, mediaTime, int(bdt))
		require.Equal(t, 8, len(mp4d.Segments[0].Fragments))
	}

}
