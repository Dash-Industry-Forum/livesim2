// Copyright 2026, DASH-Industry-Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/dash-mpd/xml"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// TestChunkedTimeSubsSegment checks that generated subtitles are delivered as chunks of
// the same duration as the video chunks, that a cue keeps its true begin time and is
// given an end time only in the chunk where it ends, and that a chunk that restates an
// unchanged cue is byte-identical and marked as redundant.
func TestChunkedTimeSubsSegment(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	testCases := []struct {
		desc     string
		url      string
		isStpp   bool
		nrChunks int
		trace    string
	}{
		{
			desc:     "stpp with 200ms chunks in a 2s segment",
			url:      "/livesim2/chunkdur_0.2/ato_1.8/ltgt_2000/timesubsstpp_en/testpic_2s/timestpp-en/0.m4s?nowMS=10000",
			isStpp:   true,
			nrChunks: 10,
			trace:    chunkedStppTrace,
		},
		{
			desc:     "wvtt with 200ms chunks in a 2s segment",
			url:      "/livesim2/chunkdur_0.2/ato_1.8/ltgt_2000/timesubswvtt_en/testpic_2s/timewvtt-en/0.m4s?nowMS=10000",
			isStpp:   false,
			nrChunks: 10,
			trace:    chunkedWvttTrace,
		},
		{
			desc:     "stpp with a chunk duration that does not divide the segment duration",
			url:      "/livesim2/chunkdur_0.7/ato_1.3/ltgt_2000/timesubsstpp_en/testpic_2s/timestpp-en/0.m4s?nowMS=10000",
			isStpp:   true,
			nrChunks: 3,
			trace:    chunkedStppUnevenTrace,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			frags := decodeSubsFragments(t, body, tc.nrChunks)
			require.Equal(t, tc.trace, traceSubsChunks(t, frags, tc.isStpp))
			requireRedundancyMatchesPayload(t, frags)
			requireChunksTileSegment(t, frags, 0, 2000)
		})
	}
}

// TestChunkedTimeSubsAvailability checks that the AdaptationSets of the generated
// subtitles get the same availability signalling as the video and audio ones, so that a
// low-latency client fetches them as early.
func TestChunkedTimeSubsAvailability(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	require.NoError(t, am.discoverAssets(slog.Default()))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.TimeSubsStpp = []string{"en"}
	cfg.AvailabilityTimeCompleteFlag = false
	cfg.AvailabilityTimeOffsetS = 1.8
	cfg.LatencyTargetMS = Ptr(uint32(2000))
	chunkDurS := 0.2
	cfg.ChunkDurS = &chunkDurS

	liveMPD, err := LiveMPD(asset, "Manifest.mpd", cfg, nil, 100_000)
	require.NoError(t, err)
	var subsAS *m.AdaptationSetType
	for _, as := range liveMPD.Periods[0].AdaptationSets {
		if as.ContentType == "text" {
			subsAS = as
			break
		}
	}
	require.NotNil(t, subsAS)
	data, err := xml.MarshalIndent(subsAS, " ", "")
	require.NoError(t, err)
	require.Equal(t, chunkedSubEn, string(data))
}

// nolint:lll
var chunkedSubEn = "" +
	` <AdaptationSetType id="100" lang="en" contentType="text" segmentAlignment="true" mimeType="application/mp4" codecs="stpp">
 <ProducerReferenceTime id="0" type="encoder" wallClockTime="1970-01-01T00:00:00Z" presentationTime="0">
 <UTCTiming schemeIdUri="urn:mpeg:dash:utc:http-xsdate:2014" value="https://time.akamai.com/?iso&amp;ms"></UTCTiming>
 </ProducerReferenceTime>
 <Role schemeIdUri="urn:mpeg:dash:role:2011" value="subtitle"></Role>
 <SegmentTemplate media="$RepresentationID$/$Number$.m4s" initialization="$RepresentationID$/init.mp4" duration="2000" startNumber="0" timescale="1000" availabilityTimeOffset="1.8" availabilityTimeComplete="false"></SegmentTemplate>
 <Representation id="timestpp-en" bandwidth="80000" startWithSAP="1"></Representation>
 </AdaptationSetType>`

// decodeSubsFragments decodes a chunked subtitle segment response and checks the
// number of chunks (one fragment each).
func decodeSubsFragments(t *testing.T, body []byte, nrChunks int) []*mp4.Fragment {
	t.Helper()
	sr := bits.NewFixedSliceReader(body)
	mp4d, err := mp4.DecodeFileSR(sr)
	require.NoError(t, err)
	require.Equal(t, 1, len(mp4d.Segments))
	frags := mp4d.Segments[0].Fragments
	require.Equal(t, nrChunks, len(frags))
	return frags
}

var stppCueRegexp = regexp.MustCompile(`<p xml:id="([^"]*)" begin="([^"]*)"( end="([^"]*)")?>`)

// traceSubsChunks renders the chunk structure as text: one line per chunk with the
// samples it carries, their duration, their redundancy marking, and the cues they state.
func traceSubsChunks(t *testing.T, frags []*mp4.Fragment, isStpp bool) string {
	t.Helper()
	var b strings.Builder
	for i, f := range frags {
		fss, err := f.GetFullSamples(nil)
		require.NoError(t, err)
		for _, fs := range fss {
			sf := mp4.DecodeSampleFlags(fs.Flags)
			fmt.Fprintf(&b, "chunk %d pts=%d dur=%d redundancy=%d: %s\n",
				i, fs.DecodeTime, fs.Dur, sf.SampleHasRedundancy, traceSubsPayload(t, fs.Data, isStpp))
		}
	}
	return b.String()
}

// traceSubsPayload renders a sample payload as a short one-line description.
func traceSubsPayload(t *testing.T, data []byte, isStpp bool) string {
	t.Helper()
	if isStpp {
		cues := make([]string, 0, 2)
		for _, m := range stppCueRegexp.FindAllStringSubmatch(string(data), -1) {
			if m[4] == "" {
				cues = append(cues, fmt.Sprintf("%s begin=%s", m[1], m[2]))
				continue
			}
			cues = append(cues, fmt.Sprintf("%s begin=%s end=%s", m[1], m[2], m[4]))
		}
		if len(cues) == 0 {
			return "no cues"
		}
		return strings.Join(cues, ", ")
	}
	box, err := mp4.DecodeBox(0, bytes.NewBuffer(data))
	require.NoError(t, err)
	if box.Type() == "vtte" {
		return "vtte"
	}
	vttc, ok := box.(*mp4.VttcBox)
	require.True(t, ok)
	require.NotNil(t, vttc.Payl)
	return fmt.Sprintf("vttc %q", vttc.Payl.CueText)
}

// requireRedundancyMatchesPayload checks that exactly those samples that repeat the
// preceding sample byte for byte are marked as redundant, and that the first sample of
// the segment never is, since that is what a client tuning in at the segment start reads.
func requireRedundancyMatchesPayload(t *testing.T, frags []*mp4.Fragment) {
	t.Helper()
	var prevData []byte
	for i, f := range frags {
		fss, err := f.GetFullSamples(nil)
		require.NoError(t, err)
		for j, fs := range fss {
			sf := mp4.DecodeSampleFlags(fs.Flags)
			require.Equal(t, byte(2), sf.SampleDependsOn, "chunk %d sample %d must be a sync sample", i, j)
			require.False(t, sf.SampleIsNonSync, "chunk %d sample %d must be a sync sample", i, j)
			isRepeat := prevData != nil && bytes.Equal(prevData, fs.Data)
			if isRepeat {
				require.Equal(t, byte(1), sf.SampleHasRedundancy,
					"chunk %d sample %d repeats the previous sample and must be marked redundant", i, j)
			} else {
				require.Equal(t, byte(0), sf.SampleHasRedundancy,
					"chunk %d sample %d differs from the previous sample and must not be marked redundant", i, j)
			}
			prevData = fs.Data
		}
	}
}

// requireChunksTileSegment checks that the chunks tile [startMS, endMS) with no gaps or
// overlaps, and that the samples inside each chunk do the same.
func requireChunksTileSegment(t *testing.T, frags []*mp4.Fragment, startMS, endMS int) {
	t.Helper()
	next := uint64(startMS)
	for i, f := range frags {
		require.Equal(t, next, f.Moof.Traf.Tfdt.BaseMediaDecodeTime(), "chunk %d starts at the end of chunk %d", i, i-1)
		fss, err := f.GetFullSamples(nil)
		require.NoError(t, err)
		for j, fs := range fss {
			require.Equal(t, next, fs.DecodeTime, "chunk %d sample %d is contiguous", i, j)
			next += uint64(fs.Dur)
		}
	}
	require.Equal(t, uint64(endMS), next)
}

// chunkedStppTrace shows the paint model in an stpp track: the cue keeps its true begin
// time and gets no end until chunk 4, where it ends. The four restatements in between are
// byte-identical and marked redundant.
// nolint:lll
const chunkedStppTrace = `chunk 0 pts=0 dur=200 redundancy=0: 0-0 begin=00:00:00.000
chunk 1 pts=200 dur=200 redundancy=1: 0-0 begin=00:00:00.000
chunk 2 pts=400 dur=200 redundancy=1: 0-0 begin=00:00:00.000
chunk 3 pts=600 dur=200 redundancy=1: 0-0 begin=00:00:00.000
chunk 4 pts=800 dur=200 redundancy=0: 0-0 begin=00:00:00.000 end=00:00:00.900
chunk 5 pts=1000 dur=200 redundancy=0: 0-1 begin=00:00:01.000
chunk 6 pts=1200 dur=200 redundancy=1: 0-1 begin=00:00:01.000
chunk 7 pts=1400 dur=200 redundancy=1: 0-1 begin=00:00:01.000
chunk 8 pts=1600 dur=200 redundancy=1: 0-1 begin=00:00:01.000
chunk 9 pts=1800 dur=200 redundancy=0: 0-1 begin=00:00:01.000 end=00:00:01.900
`

// chunkedWvttTrace shows the same in a wvtt track. A wvtt sample has no timing of its own,
// so a continued cue is restated with an identical payload and marked redundant, and the
// cue end shows up as a vtte sample inside the chunk where it happens.
// nolint:lll
const chunkedWvttTrace = `chunk 0 pts=0 dur=200 redundancy=0: vttc "1970-01-01T00:00:00Z\nen # 0"
chunk 1 pts=200 dur=200 redundancy=1: vttc "1970-01-01T00:00:00Z\nen # 0"
chunk 2 pts=400 dur=200 redundancy=1: vttc "1970-01-01T00:00:00Z\nen # 0"
chunk 3 pts=600 dur=200 redundancy=1: vttc "1970-01-01T00:00:00Z\nen # 0"
chunk 4 pts=800 dur=100 redundancy=1: vttc "1970-01-01T00:00:00Z\nen # 0"
chunk 4 pts=900 dur=100 redundancy=0: vtte
chunk 5 pts=1000 dur=200 redundancy=0: vttc "1970-01-01T00:00:01Z\nen # 0"
chunk 6 pts=1200 dur=200 redundancy=1: vttc "1970-01-01T00:00:01Z\nen # 0"
chunk 7 pts=1400 dur=200 redundancy=1: vttc "1970-01-01T00:00:01Z\nen # 0"
chunk 8 pts=1600 dur=200 redundancy=1: vttc "1970-01-01T00:00:01Z\nen # 0"
chunk 9 pts=1800 dur=100 redundancy=1: vttc "1970-01-01T00:00:01Z\nen # 0"
chunk 9 pts=1900 dur=100 redundancy=0: vtte
`

// chunkedStppUnevenTrace shows a chunk duration that does not divide the segment duration:
// the last chunk is shorter, and chunk 1 both ends one cue and starts the next.
// nolint:lll
const chunkedStppUnevenTrace = `chunk 0 pts=0 dur=700 redundancy=0: 0-0 begin=00:00:00.000
chunk 1 pts=700 dur=700 redundancy=0: 0-0 begin=00:00:00.000 end=00:00:00.900, 0-1 begin=00:00:01.000
chunk 2 pts=1400 dur=600 redundancy=0: 0-1 begin=00:00:01.000 end=00:00:01.900
`
