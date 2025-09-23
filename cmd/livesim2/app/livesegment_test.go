// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLiveSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	err := am.discoverAssets(logger)
	require.NoError(t, err)
	log := slog.Default()

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
	var drmCfg *drm.DrmConfig = nil
	for _, tc := range cases {
		t.Run(tc.asset, func(t *testing.T) {
			for _, mpdType := range []string{"Number", "TimelineTime"} {
				asset, ok := am.findAsset(tc.asset)
				require.True(t, ok)
				require.NoError(t, err)
				cfg := NewResponseConfig()
				switch mpdType {
				case "Number":
				case "TimelineTime":
					cfg.SegTimelineMode = SegTimelineModeTime
				case "TimelineNumber":
					cfg.SegTimelineMode = SegTimelineModeNr
				}
				nowMS := 100_000
				rr := httptest.NewRecorder()
				wroteInit, err := writeInitSegment(log, rr, cfg, drmCfg, asset, "2/init.mp4")
				require.False(t, wroteInit)
				require.NoError(t, err)
				rr = httptest.NewRecorder()
				wroteInit, err = writeInitSegment(log, rr, cfg, drmCfg, asset, tc.initialization)
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
				mediaTime := nr * 2 * mediaTimescale // This is exact even for audio for nr == 40
				switch mpdType {
				case "Number", "TimelineNumber":
					media = strings.ReplaceAll(media, "$NrOrTime$", fmt.Sprintf("%d", nr))
				default: // "TimelineTime":
					media = strings.ReplaceAll(media, "$NrOrTime$", fmt.Sprintf("%d", mediaTime))
				}
				so, err := genLiveSegment(log, vodFS, asset, cfg, media, nowMS, false /*isLast */)
				require.NoError(t, err)
				require.Equal(t, tc.segmentMimeType, so.meta.rep.SegmentType())
				seg := so.seg
				bdt := seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
				require.Equal(t, mediaTime, int(bdt))
			}
		})
	}
}

// TestAc3Timing checks that the generated segments end within one frame from nominal segment duration.
func TestAc3Timing(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	log := slog.Default()
	err := am.discoverAssets(log)
	require.NoError(t, err)

	asset, ok := am.findAsset("bbb_hevc_ac3_8s")
	require.True(t, ok)
	cfg := NewResponseConfig()
	nowMS := 20_000
	for sNr := 0; sNr <= 5; sNr++ {
		media := "audio_$NrOrTime$.m4s"
		media = strings.ReplaceAll(media, "$NrOrTime$", fmt.Sprintf("%d", sNr))
		so, err := genLiveSegment(log, vodFS, asset, cfg, media, nowMS, false /* isLast */)
		require.NoError(t, err)
		bmdt := int(so.seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime())
		overShoot := bmdt - (2 * sNr * 48000)
		require.Less(t, overShoot, 1536)
		require.GreaterOrEqual(t, overShoot, 0)
	}
}

func TestCheckAudioSegmentTimeAddressing(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	log := slog.Default()
	err := am.discoverAssets(log)
	require.NoError(t, err)

	cases := []struct {
		asset        string
		media        string
		refSegDur    uint64
		refTimescale uint64
		nowMS        int
		segNrStart   int
		segNrEnd     int
		nrSamplesMod []int
	}{
		{asset: "WAVE/vectors/cfhd_sets/14.985_29.97_59.94/t1/2022-10-17",
			media: "A48/$NrOrTime$.m4s", refSegDur: 2002, refTimescale: 1000, nowMS: 50_000, segNrStart: 3, segNrEnd: 4,
			nrSamplesMod: []int{94, 94, 94, 94, 94}},
		{asset: "testpic_6s", media: "A48/$NrOrTime$.m4s", refSegDur: 6000, refTimescale: 1000, nowMS: 50_000, segNrStart: 3, segNrEnd: 7,
			nrSamplesMod: []int{282, 281, 281, 281}},
	}

	for _, c := range cases {
		for _, mpdType := range []string{"TimelineTime", "Number"} {
			asset, ok := am.findAsset(c.asset)
			require.True(t, ok)
			cfg := NewResponseConfig()
			switch mpdType {
			case "Number":
			case "TimelineTime":
				cfg.SegTimelineMode = SegTimelineModeTime
			case "TimelineNumber":
				cfg.SegTimelineMode = SegTimelineModeNr
			}
			for nr := c.segNrStart; nr <= c.segNrEnd; nr++ {
				mediaTime := calcAudioTimeFromRef(uint64(nr)*c.refSegDur, c.refTimescale, 1024, 48000)
				var segMedia string
				switch mpdType {
				case "Number", "TimelineNumber":
					segMedia = strings.ReplaceAll(c.media, "$NrOrTime$", fmt.Sprintf("%d", nr))
				default:
					segMedia = strings.ReplaceAll(c.media, "$NrOrTime$", fmt.Sprintf("%d", mediaTime))
				}
				so, err := genLiveSegment(log, vodFS, asset, cfg, segMedia, c.nowMS, false /* isLast */)
				require.NoError(t, err)
				trun := so.seg.Fragments[0].Moof.Traf.Trun
				nrSamples := c.nrSamplesMod[nr%len(c.nrSamplesMod)]
				require.Equal(t, nrSamples, len(trun.Samples))
				t.Logf("nr %d segData: %s mpdType: %s mediaTime: %d\n", nr, so.meta.rep.SegmentType(), mpdType, mediaTime)
			}
		}
	}
}

func TestLiveThumbSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	log := slog.Default()
	err := am.discoverAssets(log)
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
			cfg := NewResponseConfig()
			switch mpdType {
			case "Number":
			case "TimelineTime":
				cfg.SegTimelineMode = SegTimelineModeTime
			case "TimelineNumber":
				cfg.SegTimelineMode = SegTimelineModeNr
			}
			nowMS := 100_000
			media := tc.media
			// Always number, even if MPD is timelinetime
			media = strings.ReplaceAll(media, "$NrOrTime$", fmt.Sprintf("%d", tc.reqNr))
			so, err := genLiveSegment(log, vodFS, asset, cfg, media, nowMS, false /* isLast */)
			require.NoError(t, err)
			origNr := tc.reqNr%tc.nrSegs + 1 // one-based
			require.Equal(t, tc.segmentMimeType, so.meta.rep.SegmentType())
			origSeg, err := os.ReadFile(path.Join(tc.origPath, fmt.Sprintf("%d.jpg", origNr)))
			require.NoError(t, err)
			require.Equal(t, origSeg, so.data)
		}
	}
}

func TestWriteChunkedSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	log := slog.Default()
	err := am.discoverAssets(log)
	require.NoError(t, err)
	cfg := NewResponseConfig()
	cfg.AvailabilityTimeCompleteFlag = false
	cfg.AvailabilityTimeOffsetS = 7.0
	chunkDur := 1.0
	cfg.ChunkDurS = &chunkDur
	err = logging.InitSlog("debug", "discard")
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
		err := writeChunkedSegment(context.Background(), log, rr, cfg, nil, vodFS, asset, segmentPart, nowMS, false /* isLast */)
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
			desc:       "infinite ato future",
			availTimeS: 140,
			nowS:       120,
			tsbd:       10,
			ato:        math.Inf(1),
			wantedErr:  "",
		},
		{
			desc:       "infinite ato past",
			availTimeS: 5,
			nowS:       120,
			tsbd:       10,
			ato:        math.Inf(1),
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

func TestStartNumber(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	log := slog.Default()
	err := am.discoverAssets(log)
	require.NoError(t, err)
	err = logging.InitSlog("debug", "discard")
	require.NoError(t, err)

	cases := []struct {
		asset              string
		media              string
		nowMS              int
		startNr            int
		requestNr          int
		expectedDecodeTime int
		expectedErr        string
	}{
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_000,
			startNr:            0,
			requestNr:          0,
			expectedDecodeTime: 0,
			expectedErr:        "",
		},
		{
			asset:       "testpic_2s",
			media:       "V300/$NrOrTime$.m4s",
			nowMS:       50_000,
			startNr:     5,
			requestNr:   0,
			expectedErr: "createOutSeg: not found",
		},
		{
			asset:              "testpic_2s",
			media:              "A48/$NrOrTime$.m4s",
			nowMS:              50_000,
			startNr:            5,
			requestNr:          5,
			expectedDecodeTime: 0,
			expectedErr:        "",
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		cfg := NewResponseConfig()
		cfg.StartNr = Ptr(tc.startNr)
		media := strings.Replace(tc.media, "$NrOrTime$", fmt.Sprintf("%d", (tc.requestNr)), 1)
		so, err := genLiveSegment(log, vodFS, asset, cfg, media, tc.nowMS, false /* isLast */)
		if tc.expectedErr != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
			continue
		}
		require.NoError(t, err)
		moof := so.seg.Fragments[0].Moof
		seqNr := moof.Mfhd.SequenceNumber
		require.Equal(t, tc.requestNr, int(seqNr), "response segment sequence number")
		decodeTime := moof.Traf.Tfdt.BaseMediaDecodeTime()
		require.Equal(t, tc.expectedDecodeTime, int(decodeTime), "response segment decode time")

	}
}

func TestLLSegmentAvailability(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	log := slog.Default()
	err := am.discoverAssets(log)
	require.NoError(t, err)
	err = logging.InitSlog("error", "discard")
	require.NoError(t, err)

	cases := []struct {
		asset              string
		media              string
		nowMS              int
		startNr            int
		mpdType            string
		requestMedia       int
		expectedNr         int
		expectedDecodeTime int
		expectedErr        string
	}{
		{
			asset:        "testpic_2s",
			media:        "V300/$NrOrTime$.m4s",
			nowMS:        50_000,
			mpdType:      "Number",
			requestMedia: 25,
			expectedErr:  "too early",
		},
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_500,
			mpdType:            "Number",
			requestMedia:       25,
			expectedNr:         25,
			expectedDecodeTime: 50 * 90000,
			expectedErr:        "",
		},
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_500,
			mpdType:            "TimelineNumber",
			requestMedia:       25,
			expectedNr:         25,
			expectedDecodeTime: 50 * 90000,
			expectedErr:        "",
		},
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_500,
			mpdType:            "TimelineTime",
			requestMedia:       4_500_000,
			expectedNr:         25,
			expectedDecodeTime: 50 * 90000,
			expectedErr:        "",
		},
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_500,
			startNr:            1,
			mpdType:            "Number",
			requestMedia:       26,
			expectedNr:         26,
			expectedDecodeTime: 50 * 90000,
			expectedErr:        "",
		},
		{
			asset:        "testpic_2s",
			media:        "V300/$NrOrTime$.m4s",
			nowMS:        50_500,
			startNr:      1,
			mpdType:      "Number",
			requestMedia: 27,
			expectedErr:  "too early",
		},
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_500,
			mpdType:            "TimelineNumber",
			startNr:            1,
			requestMedia:       26,
			expectedNr:         26,
			expectedDecodeTime: 50 * 90000,
			expectedErr:        "",
		},
		{
			asset:              "testpic_2s",
			media:              "V300/$NrOrTime$.m4s",
			nowMS:              50_500,
			mpdType:            "TimelineTime",
			startNr:            1,
			requestMedia:       4_500_000,
			expectedNr:         26,
			expectedDecodeTime: 50 * 90000,
			expectedErr:        "",
		},
		{
			asset:              "WAVE/av",
			media:              "aac/$NrOrTime$.m4s",
			nowMS:              10_000, // Early time where segment 0 time=0, duration shortened
			mpdType:            "TimelineTime",
			requestMedia:       0,  // Client requests time=0 - this is NOT shifted for segment 0
			expectedNr:         0,  // Should return segment 0 (0-based sequence number)
			expectedDecodeTime: 0,  // Expected decode time
			expectedErr:        "", // Should work - no mapping needed for segment 0
		},
		{
			asset:              "WAVE/av",
			media:              "aac/$NrOrTime$.m4s",
			nowMS:              70_000, // Later time where segment time is shifted
			mpdType:            "TimelineTime",
			requestMedia:       3069952, // Client requests adjusted time 64*48000-2048
			expectedNr:         32,      // Should map to correct segment number
			expectedDecodeTime: 3072000, // Original unadjusted decode time
			expectedErr:        "",      // Should work when implemented
		},
	}
	for _, tc := range cases {
		asset, ok := am.findAsset(tc.asset)
		require.True(t, ok)
		require.NoError(t, err)
		cfg := NewResponseConfig()
		cfg.AvailabilityTimeCompleteFlag = false
		cfg.AvailabilityTimeOffsetS = 1.5
		switch tc.mpdType {
		case "TimelineTime":
			cfg.SegTimelineMode = SegTimelineModeTime
		case "TimelineNumber":
			cfg.SegTimelineMode = SegTimelineModeNr
		case "Number":
			// Nothing
		default:
			require.Fail(t, "unknown mpdType")
		}
		if tc.startNr != 0 {
			cfg.StartNr = Ptr(tc.startNr)
		}
		media := strings.Replace(tc.media, "$NrOrTime$", fmt.Sprintf("%d", (tc.requestMedia)), 1)
		so, err := genLiveSegment(log, vodFS, asset, cfg, media, tc.nowMS, false /* isLast */)
		if tc.expectedErr != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
			continue
		}
		require.NoError(t, err)
		moof := so.seg.Fragments[0].Moof
		seqNr := moof.Mfhd.SequenceNumber
		require.Equal(t, tc.expectedNr, int(seqNr), "response segment sequence number")
		decodeTime := moof.Traf.Tfdt.BaseMediaDecodeTime()
		require.Equal(t, tc.expectedDecodeTime, int(decodeTime), "response segment decode time")
	}
}

func TestSegmentStatusCodeResponse(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	err := am.discoverAssets(logger)
	require.NoError(t, err)

	cases := []struct {
		desc     string
		asset    string
		media    string
		mpdType  string
		nrOrTime int
		ss       []SegStatusCodes
		nowMS    int
		expCode  int
	}{
		{
			desc:     "hit cyclic 404",
			asset:    "testpic_2s",
			media:    "V300/$NrOrTime$.m4s",
			mpdType:  "Number",
			nrOrTime: 30,
			nowMS:    90_000,
			ss: []SegStatusCodes{
				{Cycle: 30, Code: 404, Rsq: 0},
			},
			expCode: 404,
		},
		{
			desc:     "miss cyclic 404",
			asset:    "testpic_2s",
			media:    "V300/$NrOrTime$.m4s",
			mpdType:  "Number",
			nrOrTime: 31,
			nowMS:    90_000,
			ss: []SegStatusCodes{
				{Cycle: 30, Code: 404, Rsq: 0},
			},
			expCode: 0,
		},
		{
			desc:     "hit cyclic 404 for timeline time",
			asset:    "testpic_2s",
			media:    "V300/$NrOrTime$.m4s",
			mpdType:  "TimelineTime",
			nrOrTime: 90000 * 30,
			nowMS:    90_000,
			ss: []SegStatusCodes{
				{Cycle: 30, Code: 404},
			},
			expCode: 404,
		},
		{
			desc:     "miss cyclic 404 for timeline time",
			asset:    "testpic_2s",
			media:    "V300/$NrOrTime$.m4s",
			mpdType:  "TimelineTime",
			nrOrTime: 90000 * 32,
			nowMS:    90_000,
			ss: []SegStatusCodes{
				{Cycle: 30, Code: 404},
			},
			expCode: 0,
		},
		{
			desc:     "hit cyclic 404 with specific rep",
			asset:    "testpic_2s",
			media:    "A48/$NrOrTime$.m4s",
			mpdType:  "Number",
			nrOrTime: 30,
			nowMS:    90_000,
			ss: []SegStatusCodes{
				{Cycle: 30, Code: 404, Reps: []string{"A48"}},
			},
			expCode: 404,
		},
		{
			desc:     "hit cyclic 404 but not specific rep",
			asset:    "testpic_2s",
			media:    "A48/$NrOrTime$.m4s",
			mpdType:  "Number",
			nrOrTime: 30,
			nowMS:    90_000,
			ss: []SegStatusCodes{
				{Cycle: 30, Code: 404, Reps: []string{"V300"}},
			},
			expCode: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)
			require.NoError(t, err)
			cfg := NewResponseConfig()
			switch tc.mpdType {
			case "Number":
			case "TimelineTime":
				cfg.SegTimelineMode = SegTimelineModeTime
			case "TimelineNumber":
				cfg.SegTimelineMode = SegTimelineModeNr
			}
			cfg.SegStatusCodes = tc.ss
			media := strings.ReplaceAll(tc.media, "$NrOrTime$", fmt.Sprintf("%d", tc.nrOrTime))
			rr := httptest.NewRecorder()
			code, err := writeSegment(context.TODO(), rr, slog.Default(), cfg, nil,
				vodFS, asset, media, tc.nowMS, nil, false /* isLast */)
			require.NoError(t, err)
			require.Equal(t, tc.expCode, code)
		})
	}
}

// TestMpeghAssets tests MPEG-H assets with 1.6s segment duration
func TestMpeghAssets(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	err := am.discoverAssets(logger)
	require.NoError(t, err)

	testCases := []struct {
		asset                  string
		mpdName                string
		audioRepId             string
		videoRepId             string
		expectedAudioTimescale int
		expectedVideoTimescale int
		expectedSegmentDurMs   int
		expectedAudioCodec     string
		expectedVideoCodec     string
	}{
		{
			asset:                  "mpegh_BL_1_6",
			mpdName:                "BL_1_6.mpd",
			audioRepId:             "mhm1_64kbps_per_signal",
			videoRepId:             "hvc1_10000kbps",
			expectedAudioTimescale: 48000,
			expectedVideoTimescale: 50,
			expectedSegmentDurMs:   1600, // 1.6 seconds
			expectedAudioCodec:     "mhm1.0x10",
			expectedVideoCodec:     "hvc1.1.6.L123.90",
		},
		{
			asset:                  "mpegh_LC_1_6",
			mpdName:                "LC_1_6.mpd",
			audioRepId:             "mhm1_64kbps_per_signal",
			videoRepId:             "hvc1_10000kbps",
			expectedAudioTimescale: 48000,
			expectedVideoTimescale: 50,
			expectedSegmentDurMs:   1600, // 1.6 seconds
			expectedAudioCodec:     "mhm1.0x0B",
			expectedVideoCodec:     "hvc1.1.6.L123.90",
		},
	}

	var drmCfg *drm.DrmConfig = nil
	for _, tc := range testCases {
		t.Run(tc.asset, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok, "Asset %s not found", tc.asset)

			cfg := NewResponseConfig()
			nowMS := 10_000 // 10 seconds into stream

			// Test MPD generation
			liveMPD, err := LiveMPD(asset, tc.mpdName, cfg, nil, nowMS)
			require.NoError(t, err, "Failed to generate live MPD")
			require.NotNil(t, liveMPD, "Live MPD is nil")
			require.Len(t, liveMPD.Periods, 1, "Expected exactly one period")
			require.Len(t, liveMPD.Periods[0].AdaptationSets, 2, "Expected exactly two adaptation sets")

			// Test audio and video adaptation sets
			var audioAS, videoAS *m.AdaptationSetType
			for _, as := range liveMPD.Periods[0].AdaptationSets {
				switch as.ContentType {
				case "audio":
					audioAS = as
				case "video":
					videoAS = as
				}
			}
			require.NotNil(t, audioAS, "Audio adaptation set not found")
			require.NotNil(t, videoAS, "Video adaptation set not found")

			// Verify audio adaptation set
			require.Equal(t, tc.expectedAudioCodec, audioAS.Codecs, "Audio codec mismatch")
			require.Equal(t, "audio/mp4", audioAS.MimeType, "Audio mime type mismatch")
			require.Equal(t, tc.expectedAudioTimescale, int(*audioAS.SegmentTemplate.Timescale), "Audio timescale mismatch")
			expectedAudioDuration := tc.expectedSegmentDurMs * tc.expectedAudioTimescale / 1000
			require.Equal(t, expectedAudioDuration, int(*audioAS.SegmentTemplate.Duration), "Audio duration mismatch")

			// Verify video adaptation set
			require.Equal(t, tc.expectedVideoCodec, videoAS.Codecs, "Video codec mismatch")
			require.Equal(t, "video/mp4", videoAS.MimeType, "Video mime type mismatch")
			require.Equal(t, tc.expectedVideoTimescale, int(*videoAS.SegmentTemplate.Timescale), "Video timescale mismatch")
			expectedVideoDuration := tc.expectedSegmentDurMs * tc.expectedVideoTimescale / 1000
			require.Equal(t, expectedVideoDuration, int(*videoAS.SegmentTemplate.Duration), "Video duration mismatch")

			// Test audio init segment
			audioInitPath := fmt.Sprintf("%s_init.mp4", tc.audioRepId)
			rr := httptest.NewRecorder()
			wroteInit, err := writeInitSegment(logger, rr, cfg, drmCfg, asset, audioInitPath)
			require.True(t, wroteInit, "Failed to write audio init segment")
			require.NoError(t, err, "Error writing audio init segment")
			require.Equal(t, http.StatusOK, rr.Code, "Audio init segment returned wrong status code")

			// Verify audio init segment content
			initSeg := rr.Body.Bytes()
			require.Greater(t, len(initSeg), 0, "Audio init segment is empty")
			sr := bits.NewFixedSliceReader(initSeg)
			mp4File, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err, "Failed to parse audio init segment")
			require.NotNil(t, mp4File.Init, "Init segment has no init part")
			audioTimescale := int(mp4File.Moov.Trak.Mdia.Mdhd.Timescale)
			require.Equal(t, tc.expectedAudioTimescale, audioTimescale, "Audio init segment timescale mismatch")

			// Test audio media segment (segment 0)
			audioMediaPath := fmt.Sprintf("%s_0.m4s", tc.audioRepId)
			so, err := genLiveSegment(logger, vodFS, asset, cfg, audioMediaPath, nowMS, false)
			require.NoError(t, err, "Failed to generate audio segment")
			require.NotNil(t, so, "Audio segment output is nil")
			require.Equal(t, "audio/mp4", so.meta.rep.SegmentType(), "Audio segment wrong type")

			// Verify audio segment timing (1.6s duration = 76800 at 48kHz)
			seg := so.seg
			require.Len(t, seg.Fragments, 1, "Expected exactly one fragment in audio segment")
			baseDecodeTime := seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			require.Equal(t, 0, int(baseDecodeTime), "First audio segment should start at time 0")

			// Test video init segment
			videoInitPath := fmt.Sprintf("%s_init.mp4", tc.videoRepId)
			rr = httptest.NewRecorder()
			wroteInit, err = writeInitSegment(logger, rr, cfg, drmCfg, asset, videoInitPath)
			require.True(t, wroteInit, "Failed to write video init segment")
			require.NoError(t, err, "Error writing video init segment")
			require.Equal(t, http.StatusOK, rr.Code, "Video init segment returned wrong status code")

			// Verify video init segment content
			initSeg = rr.Body.Bytes()
			require.Greater(t, len(initSeg), 0, "Video init segment is empty")
			sr = bits.NewFixedSliceReader(initSeg)
			mp4File, err = mp4.DecodeFileSR(sr)
			require.NoError(t, err, "Failed to parse video init segment")
			require.NotNil(t, mp4File.Init, "Init segment has no init part")
			videoTimescale := int(mp4File.Moov.Trak.Mdia.Mdhd.Timescale)
			require.Equal(t, tc.expectedVideoTimescale, videoTimescale, "Video init segment timescale mismatch")

			// Test video media segment (segment 0)
			videoMediaPath := fmt.Sprintf("%s_0.m4s", tc.videoRepId)
			so, err = genLiveSegment(logger, vodFS, asset, cfg, videoMediaPath, nowMS, false)
			require.NoError(t, err, "Failed to generate video segment")
			require.NotNil(t, so, "Video segment output is nil")
			require.Equal(t, "video/mp4", so.meta.rep.SegmentType(), "Video segment wrong type")

			// Verify video segment timing (1.6s duration = 80 at 50Hz)
			seg = so.seg
			require.Len(t, seg.Fragments, 1, "Expected exactly one fragment in video segment")
			baseDecodeTime = seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			require.Equal(t, 0, int(baseDecodeTime), "First video segment should start at time 0")

			// Test segment 1 to verify timing progression
			audioMediaPath = fmt.Sprintf("%s_1.m4s", tc.audioRepId)
			so, err = genLiveSegment(logger, vodFS, asset, cfg, audioMediaPath, nowMS, false)
			require.NoError(t, err, "Failed to generate audio segment 1")
			baseDecodeTime = so.seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			expectedAudioTime := 76800 // 1.6s * 48000
			require.Equal(t, expectedAudioTime, int(baseDecodeTime), "Second audio segment timing incorrect")

			videoMediaPath = fmt.Sprintf("%s_1.m4s", tc.videoRepId)
			so, err = genLiveSegment(logger, vodFS, asset, cfg, videoMediaPath, nowMS, false)
			require.NoError(t, err, "Failed to generate video segment 1")
			baseDecodeTime = so.seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			expectedVideoTime := 80 // 1.6s * 50
			require.Equal(t, expectedVideoTime, int(baseDecodeTime), "Second video segment timing incorrect")

			t.Logf("Successfully tested MPEG-H asset %s with 1.6s segments", tc.asset)
		})
	}
}

func TestMehdBoxRemovedFromInitSegment(t *testing.T) {
	var drmCfg *drm.DrmConfig = nil
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	err := am.discoverAssets(logger)
	require.NoError(t, err)
	asset, ok := am.findAsset("testpic_8s")
	require.True(t, ok)
	cfg := NewResponseConfig()
	initV300 := "V300/init.mp4"
	match, err := matchInit(initV300, cfg, drmCfg, asset)
	require.NoError(t, err)
	sr := bits.NewFixedSliceReader(match.init)
	mp4File, err := mp4.DecodeFileSR(sr)
	require.NoError(t, err)
	initSeg := mp4File.Init
	require.NotNil(t, initSeg)
	require.Nil(t, initSeg.Moov.Mvex.Mehd)
}

func TestWriteSubSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	err := logging.InitSlog("debug", "discard")
	require.NoError(t, err)
	log := slog.Default()
	err = am.discoverAssets(log)
	require.NoError(t, err)

	cases := []struct {
		desc                    string
		asset                   string
		media                   string
		subSegmentPart          string
		nowMS                   int
		expSeqNr                uint32
		expErr                  string
		shouldPanic             bool
	}{
		{
			desc:                    "first video sub-segment (8s)",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "0",
			nowMS:                   88_000,
			expSeqNr:                10,
		},
		{
			desc:                    "last video sub-segment (8s)",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "7",
			nowMS:                   88_000,
			expSeqNr:                10,
		},
		{
			desc:                    "valid sub-segment (8s segment)",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "1",
			nowMS:                   88_000,
			expSeqNr:                10,
		},
		{
			desc:                    "too early",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "1",
			nowMS:                   79_000,
			expErr:                  "createOutSeg: too early by",
		},
		{
			desc:                    "gone",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "1",
			nowMS:                   400_000,
			expErr:                  "createOutSeg: gone",
		},
		{
			desc:                    "invalid sub-segment part (not a number)",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "abc",
			nowMS:                   86_000,
			expErr:                  "bad chunk index: strconv.Atoi: parsing \"abc\": invalid syntax",
		},
		{
			desc:                    "invalid sub-segment index (out of bounds)",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "9",
			nowMS:                   88_000,
			expErr:                  "chunk 9 not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)

			cfg := NewResponseConfig()
			cfg.SSRAS = "1,2"
			cfg.ChunkDurSSR = "1,1.0"
			cfg.ChunkDurS = Ptr(1.0)

			rr := httptest.NewRecorder()
			ctx := context.Background()

			if tc.shouldPanic {
				require.Panics(t, func() {
					// This call is expected to panic
					_ = writeSubSegment(ctx, log, rr, cfg, nil, vodFS, asset, tc.media, tc.subSegmentPart, tc.nowMS, false /* isLast */)
				})

				return
			}

			err := writeSubSegment(ctx, log, rr, cfg, nil, vodFS, asset, tc.media, tc.subSegmentPart, tc.nowMS, false /* isLast */)

			if tc.expErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expErr)
				return
			}
			require.NoError(t, err)

			seg := rr.Body.Bytes()

			// A subsegment is a (styp+)moof+mdat which is a valid file
			sr := bits.NewFixedSliceReader(seg)
			mp4d, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			require.Equal(t, 1, len(mp4d.Segments[0].Fragments))

			moof := mp4d.Segments[0].Fragments[0].Moof
			require.Equal(t, tc.expSeqNr, moof.Mfhd.SequenceNumber)
		})
	}
}

func TestWriteSubSegmentWithChunkDuration(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	err := logging.InitSlog("debug", "discard")
	require.NoError(t, err)
	log := slog.Default()
	err = am.discoverAssets(log)
	require.NoError(t, err)

	cases := []struct {
		desc                    string
		asset                   string
		media                   string
		subSegmentPart          string
		nowMS                   int
		chunkDurS               *float64
		availabilityTimeOffsetS float64
		expSeqNr                uint32
		expErr                  string
	}{
		{
			desc:                    "chunk 5 with explicit chunkDurS=0.2s and minimal availabilityTimeOffset",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "5",
			nowMS:                   88_000,
			chunkDurS:               Ptr(0.2),
			availabilityTimeOffsetS: 0.1,
			expSeqNr:                10,
		},
		{
			desc:                    "chunk 10 with explicit chunkDurS=0.2s and minimal availabilityTimeOffset",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "10",
			nowMS:                   88_000,
			chunkDurS:               Ptr(0.2),
			availabilityTimeOffsetS: 0.1,
			expSeqNr:                10,
		},
		{
			desc:                    "chunk 5 with explicit chunkDurS=0.2s and availabilityTimeOffset=1.8s",
			asset:                   "testpic_8s",
			media:                   "V300/10.m4s",
			subSegmentPart:          "5",
			nowMS:                   88_000,
			chunkDurS:               Ptr(0.2),
			availabilityTimeOffsetS: 1.8,
			expSeqNr:                10,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			asset, ok := am.findAsset(tc.asset)
			require.True(t, ok)

			cfg := NewResponseConfig()
			cfg.AvailabilityTimeCompleteFlag = false
			cfg.SSRAS = "1,2"
			cfg.AvailabilityTimeOffsetS = tc.availabilityTimeOffsetS
			cfg.ChunkDurS = tc.chunkDurS

			rr := httptest.NewRecorder()
			ctx := context.Background()

			err := writeSubSegment(ctx, slog.Default(), rr, cfg, nil, vodFS, asset, tc.media, tc.subSegmentPart, tc.nowMS, false)

			if tc.expErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expErr)
				return
			}
			require.NoError(t, err)

			seg := rr.Body.Bytes()

			// A subsegment is a (styp+)moof+mdat which is a valid file
			sr := bits.NewFixedSliceReader(seg)
			mp4d, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			require.Equal(t, 1, len(mp4d.Segments[0].Fragments))

			moof := mp4d.Segments[0].Fragments[0].Moof
			require.Equal(t, tc.expSeqNr, moof.Mfhd.SequenceNumber)
		})
	}
}
