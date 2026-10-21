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
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ssrTestServer serves the test assets, and returns a function that GETs a path at nowMS.
func ssrTestServer(t *testing.T) func(path string, nowMS int) (int, []byte) {
	t.Helper()
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewTestServer(t, server.Router)
	client := ts.Client()
	return func(path string, nowMS int) (int, []byte) {
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

// TestSSRChunkDurPerRequest checks that the chunkdurssr_ duration of a request decides its
// Partial Segments, also after and while MPDs with another duration are requested.
// Before, the MPD request stored its duration in the Representation data that all requests share.
func TestSSRChunkDurPerRequest(t *testing.T) {
	get := ssrTestServer(t)
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
