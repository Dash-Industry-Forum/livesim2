// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
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
		asset           string
		initialization  string
		media           string
		segmentMimeType string
		mediaTimescale  int
	}{
		{
			asset:           "WAVE/vectors/cfhd_sets/12.5_25_50/t3/2022-10-17",
			initialization:  "1/init.mp4",
			media:           "1/$NrOrTime$.m4s",
			segmentMimeType: "video/mp4",
			mediaTimescale:  12800,
		},
		{
			asset:           "testpic_2s",
			initialization:  "V300/init.mp4",
			media:           "V300/$NrOrTime$.m4s",
			segmentMimeType: "video/mp4",
			mediaTimescale:  90000,
		},
		{
			asset:           "testpic_2s",
			initialization:  "A48/init.mp4",
			media:           "A48/$NrOrTime$.m4s",
			segmentMimeType: "audio/mp4",
			mediaTimescale:  48000,
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
			wroteInit, err := writeInitSegment(rr, cfg, vodFS, asset, "2/init.mp4")
			require.False(t, wroteInit)
			require.NoError(t, err)
			rr = httptest.NewRecorder()
			wroteInit, err = writeInitSegment(rr, cfg, vodFS, asset, tc.initialization)
			require.True(t, wroteInit)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rr.Code)
			initSeg := rr.Body.Bytes()
			sr := bits.NewFixedSliceReader(initSeg)
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
			segData, err := adjustLiveSegment(vodFS, asset, cfg, media, nowMS)
			require.NoError(t, err)
			require.Equal(t, tc.segmentMimeType, segData.contentType)
			sr = bits.NewFixedSliceReader(segData.data)
			mp4d, err = mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			require.Equal(t, 1, len(mp4d.Segments), "nr segments should be 1")
			bdt := mp4d.Segments[0].Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			require.Equal(t, mediaTime, int(bdt))
		}
	}
}

func TestLiveThumbSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)

	cases := []struct {
		asset           string
		media           string
		segmentMimeType string
		mediaTimescale  int
		origPath        string
		reqNr           int
		nrSegs          int
	}{
		{
			asset:           "testpic_2s",
			media:           "thumbs/$NrOrTime$.jpg",
			segmentMimeType: "image/jpeg",
			mediaTimescale:  1,
			origPath:        "testdata/assets/testpic_2s/thumbs",
			reqNr:           43,
			nrSegs:          4,
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
			media := tc.media
			// Always number, even if MPD is timelinetime
			media = strings.Replace(media, "$NrOrTime$", fmt.Sprintf("%d", tc.reqNr), -1)
			segData, err := adjustLiveSegment(vodFS, asset, cfg, media, nowMS)
			require.NoError(t, err)
			origNr := tc.reqNr%tc.nrSegs + 1 // one-based
			require.Equal(t, tc.segmentMimeType, segData.contentType)
			origSeg, err := os.ReadFile(path.Join(tc.origPath, fmt.Sprintf("%d.jpg", origNr)))
			require.NoError(t, err)
			require.Equal(t, origSeg, segData.data)
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
	cfg.AvailabilityTimeOffsetS = 7.0
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

func TestAvailabilityTime(t *testing.T) {
	testCases := []struct {
		desc       string
		availTimeS float64
		nowS       float64
		tsbd       float64
		ato        float64
		wantedErr  string
	}{
		{
			desc:       "too early",
			availTimeS: 4,
			nowS:       2,
			tsbd:       10,
			ato:        0,
			wantedErr:  "too early by 2000ms",
		},
		{
			desc:       "ato > 0",
			availTimeS: 14,
			nowS:       12,
			tsbd:       10,
			ato:        2,
			wantedErr:  "",
		},
		{
			desc:       "fine",
			availTimeS: 14,
			nowS:       15,
			tsbd:       10,
			ato:        0,
			wantedErr:  "",
		},
		{
			desc:       "too late",
			availTimeS: 140,
			nowS:       120,
			tsbd:       10,
			ato:        0,
			wantedErr:  "too late",
		},
		{
			desc:       "infinite tsbd",
			availTimeS: 140,
			nowS:       120,
			tsbd:       10,
			ato:        -1,
			wantedErr:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			err := CheckTimeValidity(tc.availTimeS, tc.nowS, tc.tsbd, tc.ato)
			if tc.wantedErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err, tc.wantedErr)
			}
		})
	}
}

func TestTTMLTimeShifts(t *testing.T) {
	cases := []struct {
		desc       string
		ttml       string
		offsetMS   uint64
		wantedTTML string
	}{
		{
			desc:       "timestamps with fraction",
			ttml:       `begin="00:00:00.000" end="00:00:00.500"`,
			offsetMS:   3600000500,
			wantedTTML: `begin="1000:00:00.500" end="1000:00:01.000"`,
		},
		{
			desc:       "no fraction",
			ttml:       `begin="00:00:00" end="00:00:01"`,
			offsetMS:   500,
			wantedTTML: `begin="00:00:00.500" end="00:00:01.500"`,
		},
	}

	for _, c := range cases {
		gotTTMLBytes, err := shiftTTMLTimestamps([]byte(c.ttml), c.offsetMS)
		require.NoError(t, err)
		gotTTML := string(gotTTMLBytes)
		assert.Equal(t, c.wantedTTML, gotTTML)
	}

}
