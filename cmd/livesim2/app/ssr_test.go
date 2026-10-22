// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLiveServer serves the test assets, and returns the server and a function that GETs a path at nowMS.
func testLiveServer(t *testing.T) (*Server, func(path string, nowMS int) (int, []byte)) {
	t.Helper()
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewTestServer(t, server.Router)
	client := ts.Client()
	return server, func(path string, nowMS int) (int, []byte) {
		resp, err := client.Get(fmt.Sprintf("%s%s?nowMS=%d", ts.URL, path, nowMS))
		if err != nil {
			return 0, nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}
}

// partialTiming returns the start time and duration in seconds of the fragment in a Partial Segment.
func partialTiming(t *testing.T, body []byte, init *mp4.InitSegment) (startS, durS float64) {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(body))
	require.NoError(t, err)
	require.Len(t, f.Segments, 1)
	require.Len(t, f.Segments[0].Fragments, 1)
	frag := f.Segments[0].Fragments[0]
	samples, err := frag.GetFullSamples(init.Moov.Mvex.Trex)
	require.NoError(t, err)
	var dur uint64
	for _, s := range samples {
		dur += uint64(s.Dur)
	}
	timescale := float64(init.Moov.Trak.Mdia.Mdhd.Timescale)
	return float64(frag.Moof.Traf.Tfdt.BaseMediaDecodeTime()) / timescale, float64(dur) / timescale
}

func ssrTestInit(t *testing.T) *mp4.InitSegment {
	t.Helper()
	data, err := os.ReadFile("testdata/assets/testpic_2s_low_delay/LD_360/init.mp4")
	require.NoError(t, err)
	f, err := mp4.DecodeFile(bytes.NewReader(data))
	require.NoError(t, err)
	return f.Init
}

// ssrPartialPath is the path of Partial Segment subNr of segment 51 of the low-delay
// Representation LD_360, with the chunkdurssr_ duration chunkDur. The asset loops 4 segments
// whose times start at 2 s, so segment 51 covers 102-104 s.
func ssrPartialPath(chunkDur string, subNr int) string {
	return fmt.Sprintf("/livesim2/ssras_2,1/chunkdurssr_2,%s/testpic_2s_low_delay/LD_360/51_%d.m4s", chunkDur, subNr)
}

// TestSSRPartialSegments checks that a Partial Segment becomes available after its own
// MPD duration, the sequence duration divided by @k (ISO/IEC 23009-1 5.3.9.7.1), not after
// the whole Segment Sequence, and that it has that duration.
func TestSSRPartialSegments(t *testing.T) {
	_, get := testLiveServer(t)
	init := ssrTestInit(t)
	cases := []struct {
		desc       string
		chunkDur   string
		subNr      int
		nowMS      int
		wantStatus int
		wantStartS float64
		wantDurS   float64
	}{
		{"first of 4 when available", "0.5", 0, 102_600, http.StatusOK, 102.0, 0.5},
		{"first of 4 too early", "0.5", 0, 102_400, http.StatusTooEarly, 0, 0},
		{"second of 4 too early", "0.5", 1, 102_600, http.StatusTooEarly, 0, 0},
		{"last of 4", "0.5", 3, 104_000, http.StatusOK, 103.5, 0.5},
		{"beyond @k", "0.5", 4, 105_000, http.StatusNotFound, 0, 0},
		{"second of 2", "1", 1, 104_000, http.StatusOK, 103.0, 1.0},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			status, body := get(ssrPartialPath(c.chunkDur, c.subNr), c.nowMS)
			require.Equal(t, c.wantStatus, status, string(body))
			if status != http.StatusOK {
				return
			}
			startS, durS := partialTiming(t, body, init)
			require.InDelta(t, c.wantStartS, startS, 1e-9, "start time")
			require.InDelta(t, c.wantDurS, durS, 1e-9, "duration")
		})
	}
}

// TestSSRChunkDurPerRequest checks that the chunkdurssr_ duration of a request decides its
// Partial Segments, also after and while MPDs with another duration are requested.
// Before, the MPD request stored its duration in the Representation data that all requests share.
func TestSSRChunkDurPerRequest(t *testing.T) {
	_, get := testLiveServer(t)
	init := ssrTestInit(t)
	mpdPath := "/livesim2/ssras_2,1/chunkdurssr_2,0.5/testpic_2s_low_delay/Manifest.mpd"
	checkPartial := func(t *testing.T) {
		status, body := get(ssrPartialPath("1", 1), 104_000)
		if !assert.Equal(t, http.StatusOK, status) {
			return
		}
		startS, durS := partialTiming(t, body, init)
		assert.InDelta(t, 103.0, startS, 1e-9, "start time")
		assert.InDelta(t, 1.0, durS, 1e-9, "duration")
	}

	status, _ := get(mpdPath, 110_000)
	require.Equal(t, http.StatusOK, status)
	checkPartial(t)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			status, _ := get(mpdPath, 110_000)
			assert.Equal(t, http.StatusOK, status)
		})
		wg.Go(func() { checkPartial(t) })
	}
	wg.Wait()
}

// TestSAPGrid checks the smallest period with a sync sample at each of its multiples in every segment.
func TestSAPGrid(t *testing.T) {
	seg := func(dur uint64, syncs ...uint64) Segment {
		return Segment{StartTime: 0, EndTime: dur, syncOffsets: syncs}
	}
	cases := []struct {
		desc string
		segs []Segment
		want uint64
	}{
		{"regular", []Segment{seg(8, 0, 2, 4, 6), seg(8, 0, 2, 4, 6)}, 2},
		{"extra sync samples", []Segment{seg(8, 0, 2, 3, 4, 6), seg(8, 0, 1, 2, 4, 6, 7)}, 2},
		{"one sync sample missing", []Segment{seg(8, 0, 2, 4, 6), seg(8, 0, 2, 4, 7)}, 4},
		{"only at segment starts", []Segment{seg(8, 0), seg(8, 0, 4)}, 0},
		{"no sync sample at start", []Segment{seg(8, 1, 2, 4, 6)}, 0},
		{"no segments", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			require.Equal(t, c.want, sapGrid(c.segs))
		})
	}
}

// TestSSRCadence checks that the SegmentSequenceProperties@cadence follows the IDR pictures
// of the low-delay Representations, which have one at each full second, and that the
// Partial Segments that it selects start with a sync sample.
func TestSSRCadence(t *testing.T) {
	_, get := testLiveServer(t)
	init := ssrTestInit(t)
	cases := []struct {
		chunkDur    string
		nrParts     int
		wantCadence uint32 // 0 for no SegmentSequenceProperties
	}{
		{"2", 1, 1},
		{"1", 2, 1},
		{"0.5", 4, 2},
		{"0.4", 5, 0}, // Only the first Partial Segment, as signalled by @startWithSAP
		{"0.2", 10, 5},
	}
	for _, c := range cases {
		t.Run(c.chunkDur, func(t *testing.T) {
			status, body := get(fmt.Sprintf("/livesim2/ssras_2,1/chunkdurssr_2,%s/testpic_2s_low_delay/Manifest.mpd", c.chunkDur), 110_000)
			require.Equal(t, http.StatusOK, status, string(body))
			mpd, err := m.MPDFromBytes(body)
			require.NoError(t, err)
			var as *m.AdaptationSetType
			for _, a := range mpd.Periods[0].AdaptationSets {
				if a.Id != nil && *a.Id == 2 {
					as = a
				}
			}
			require.NotNil(t, as)
			if c.wantCadence == 0 {
				require.Empty(t, as.SegmentSequenceProperties)
				return
			}
			require.Len(t, as.SegmentSequenceProperties, 1)
			ssp := as.SegmentSequenceProperties[0]
			require.Equal(t, uint32(1), ssp.SapType)
			require.Equal(t, c.wantCadence, ssp.Cadence)
			if c.nrParts == 1 {
				return
			}
			// The selected Partial Segments of segments 51-54, which come from all four VoD segments.
			for nr := 51; nr <= 54; nr++ {
				for sub := 0; sub < c.nrParts; sub += int(c.wantCadence) {
					path := fmt.Sprintf("/livesim2/ssras_2,1/chunkdurssr_2,%s/testpic_2s_low_delay/LD_360/%d_%d.m4s", c.chunkDur, nr, sub)
					status, body := get(path, 110_000)
					require.Equal(t, http.StatusOK, status, path)
					f, err := mp4.DecodeFile(bytes.NewReader(body))
					require.NoError(t, err)
					samples, err := f.Segments[0].Fragments[0].GetFullSamples(init.Moov.Mvex.Trex)
					require.NoError(t, err)
					require.True(t, samples[0].IsSync(), "%s starts with a sync sample", path)
				}
			}
		})
	}
}
