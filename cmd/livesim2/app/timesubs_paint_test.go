// Copyright 2026, DASH-Industry-Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// TestTimeSubsPaintConfigParsing checks the option grammar of the paint-model tracks.
func TestTimeSubsPaintConfigParsing(t *testing.T) {
	cases := []struct {
		desc     string
		kind     string
		val      string
		wantErr  string
		wantLang []string
		wantNoCh bool
		wantBody bool
	}{
		{desc: "languages only, no-change on by default", kind: "stpc", val: "en",
			wantLang: []string{"en"}, wantNoCh: true},
		{desc: "several languages", kind: "wvtc", val: "en,sv",
			wantLang: []string{"en", "sv"}, wantNoCh: true},
		{desc: "no-change turned off as a control", kind: "stpc", val: "en;nochange=0",
			wantLang: []string{"en"}, wantNoCh: false},
		{desc: "body-only deltas", kind: "stpc", val: "en;nochange=1;body=1",
			wantLang: []string{"en"}, wantNoCh: true, wantBody: true},
		{desc: "empty", kind: "stpc", val: "", wantErr: "empty stpc config"},
		{desc: "empty language", kind: "stpc", val: "en,", wantErr: "empty language"},
		{desc: "body on wvtc", kind: "wvtc", val: "en;body=1", wantErr: "does not support body"},
		{desc: "unknown param", kind: "stpc", val: "en;fast=1", wantErr: `unknown stpc param "fast"`},
		{desc: "not key=val", kind: "stpc", val: "en;body", wantErr: "must be key=val"},
		{desc: "bad bool", kind: "stpc", val: "en;nochange=yes", wantErr: "must be 0 or 1"},
		{desc: "extra spaces", kind: "stpc", val: "en; body=1", wantErr: "extra spaces"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			cfg, err := CreateTimeSubsPaintConfig(c.kind, c.val)
			if c.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), c.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.wantLang, cfg.Languages)
			require.Equal(t, c.wantNoCh, cfg.NoChange)
			require.Equal(t, c.wantBody, cfg.Body)
		})
	}
}

// TestPaintSubsInitSegment checks that the paint-model tracks get their own sample entry,
// since a receiver that only knows stpp or wvtt must not select them.
func TestPaintSubsInitSegment(t *testing.T) {
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	cases := []struct {
		url       string
		sampleEnt string
	}{
		{"/livesim2/timesubsstpc_en/testpic_2s/timestpc-en/init.mp4", "stpc"},
		{"/livesim2/timesubswvtc_en/testpic_2s/timewvtc-en/init.mp4", "wvtc"},
	}
	for _, c := range cases {
		t.Run(c.sampleEnt, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", c.url, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			init, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(body))
			require.NoError(t, err)
			stsd := init.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd
			require.Equal(t, 1, len(stsd.Children))
			require.Equal(t, c.sampleEnt, stsd.Children[0].Type())
		})
	}
}

// TestChunkedPaintSubsSegment checks what a paint-model track actually sends per chunk:
// a document in the first chunk of a segment and whenever the content changes, an 8-byte
// no-change box when it does not, and with body=1 only the body of a changed document.
func TestChunkedPaintSubsSegment(t *testing.T) {
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	const chunked = "/livesim2/chunkdur_0.2/ato_1.8/ltgt_2000/"
	testCases := []struct {
		desc     string
		url      string
		wvtt     bool
		nrChunks int
		trace    string
	}{
		{
			desc:     "stpc no-change boxes",
			url:      chunked + "timesubsstpc_en/testpic_2s/timestpc-en/0.m4s?nowMS=10000",
			nrChunks: 10,
			trace:    chunkedStpcTrace,
		},
		{
			desc:     "stpc with body-only deltas",
			url:      chunked + "timesubsstpc_en;body=1/testpic_2s/timestpc-en/0.m4s?nowMS=10000",
			nrChunks: 10,
			trace:    chunkedStpcBodyTrace,
		},
		{
			desc:     "stpc with no-change turned off is a control",
			url:      chunked + "timesubsstpc_en;nochange=0/testpic_2s/timestpc-en/0.m4s?nowMS=10000",
			nrChunks: 10,
			trace:    chunkedStpcNoChangeOffTrace,
		},
		{
			desc:     "wvtc no-change boxes",
			url:      chunked + "timesubswvtc_en/testpic_2s/timewvtc-en/0.m4s?nowMS=10000",
			wvtt:     true,
			nrChunks: 10,
			trace:    chunkedWvtcTrace,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			frags := decodeSubsFragments(t, body, tc.nrChunks)
			require.Equal(t, tc.trace, tracePaintChunks(t, frags, tc.wvtt))
			requirePaintFlagsMatchPayload(t, frags)
			requireChunksTileSegment(t, frags, 0, 2000)
		})
	}
}

// TestPaintSubsUnchunked checks that without chunking a paint-model track is an ordinary
// document track: there is one sample per segment, so there is nothing to leave out.
func TestPaintSubsUnchunked(t *testing.T) {
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, body := testFullRequest(t, ts, "GET",
		"/livesim2/timesubsstpc_en/testpic_2s/timestpc-en/0.m4s?nowMS=10000", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	frags := decodeSubsFragments(t, body, 1)
	fss, err := frags[0].GetFullSamples(nil)
	require.NoError(t, err)
	require.Equal(t, 1, len(fss))
	require.True(t, strings.HasPrefix(string(fss[0].Data), "<?xml"))
	require.True(t, mp4.IsSyncSampleFlags(fss[0].Flags))
}

// TestPaintSubsMPD checks that the paint-model AdaptationSets carry the new 4CC, so that
// selection happens through the RFC 6381 codecs parameter that clients already use.
func TestPaintSubsMPD(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	require.NoError(t, am.discoverAssets(slog.Default()))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.TimeSubsStpp = []string{"en"}
	cfg.TimeSubsStpc = &TimeSubsPaintConfig{Languages: []string{"en"}, NoChange: true}
	cfg.TimeSubsWvtc = &TimeSubsPaintConfig{Languages: []string{"en"}, NoChange: true}

	liveMPD, err := LiveMPD(asset, "Manifest.mpd", cfg, nil, 100_000)
	require.NoError(t, err)

	got := make(map[string]string) // codecs -> representation id
	for _, as := range liveMPD.Periods[0].AdaptationSets {
		if as.ContentType != "text" {
			continue
		}
		require.Equal(t, 1, len(as.Representations))
		got[string(as.Codecs)] = as.Representations[0].Id
	}
	require.Equal(t, map[string]string{
		"stpp": "timestpp-en",
		"stpc": "timestpc-en",
		"wvtc": "timewvtc-en",
	}, got)
}

// paintBoxOfSample returns the box that is the whole sample, if the sample is one.
// The test is the dispatch rule: a box header of a known type whose size is the sample
// size. A TTML document cannot match, since it cannot start with a zero byte.
func paintBoxOfSample(data []byte) (mp4.Box, bool) {
	if len(data) < 8 {
		return nil, false
	}
	switch string(data[4:8]) {
	case "ttmn", "ttmb", "vttn":
	default:
		return nil, false
	}
	if binary.BigEndian.Uint32(data[:4]) != uint32(len(data)) {
		return nil, false
	}
	box, err := mp4.DecodeBoxSR(0, bits.NewFixedSliceReader(data))
	if err != nil {
		return nil, false
	}
	return box, true
}

// tracePaintChunks renders the chunk structure of a paint-model track as text, showing
// for each sample whether it is a sync sample and what it carries.
func tracePaintChunks(t *testing.T, frags []*mp4.Fragment, wvtt bool) string {
	t.Helper()
	var b strings.Builder
	for i, f := range frags {
		fss, err := f.GetFullSamples(nil)
		require.NoError(t, err)
		for _, fs := range fss {
			sync := 0
			if mp4.IsSyncSampleFlags(fs.Flags) {
				sync = 1
			}
			fmt.Fprintf(&b, "chunk %d pts=%d dur=%d sync=%d size=%d: %s\n",
				i, fs.DecodeTime, fs.Dur, sync, fs.Size, tracePaintPayload(t, fs.Data, wvtt))
		}
	}
	return b.String()
}

func tracePaintPayload(t *testing.T, data []byte, wvtt bool) string {
	t.Helper()
	if box, ok := paintBoxOfSample(data); ok {
		if ttmb, isBody := box.(*mp4.TtmbBox); isBody {
			return "ttmb " + traceSubsPayload(t, []byte(ttmb.Body), true)
		}
		return box.Type()
	}
	return traceSubsPayload(t, data, !wvtt)
}

// requirePaintFlagsMatchPayload checks the sample flags against what each sample carries:
// a no-change or body-only box is a non-sync sample that depends on an earlier one, a
// document is a sync sample, and the first sample of the segment is always a document,
// since that is what a client tuning in at the segment boundary has to decode.
func requirePaintFlagsMatchPayload(t *testing.T, frags []*mp4.Fragment) {
	t.Helper()
	first := true
	for i, f := range frags {
		fss, err := f.GetFullSamples(nil)
		require.NoError(t, err)
		for j, fs := range fss {
			_, isBox := paintBoxOfSample(fs.Data)
			sf := mp4.DecodeSampleFlags(fs.Flags)
			if first {
				require.False(t, isBox, "the first sample of the segment must be self-contained")
			}
			first = false
			if !isBox {
				require.True(t, mp4.IsSyncSampleFlags(fs.Flags),
					"chunk %d sample %d carries content and must be a sync sample", i, j)
				continue
			}
			require.True(t, sf.SampleIsNonSync,
				"chunk %d sample %d is not self-contained and must not be a sync sample", i, j)
			require.Equal(t, byte(1), sf.SampleDependsOn,
				"chunk %d sample %d must say that it depends on an earlier sample", i, j)
		}
	}
}

// chunkedStpcTrace shows what the no-change box buys: eight of the ten chunks say nothing
// new and cost 8 bytes each instead of a complete TTML document.
// nolint:lll
const chunkedStpcTrace = `chunk 0 pts=0 dur=200 sync=1 size=1477: c0 begin=00:00:00.000
chunk 1 pts=200 dur=200 sync=0 size=8: ttmn
chunk 2 pts=400 dur=200 sync=0 size=8: ttmn
chunk 3 pts=600 dur=200 sync=0 size=8: ttmn
chunk 4 pts=800 dur=200 sync=1 size=1496: c0 begin=00:00:00.000 end=00:00:00.900
chunk 5 pts=1000 dur=200 sync=1 size=1477: c1 begin=00:00:01.000
chunk 6 pts=1200 dur=200 sync=0 size=8: ttmn
chunk 7 pts=1400 dur=200 sync=0 size=8: ttmn
chunk 8 pts=1600 dur=200 sync=0 size=8: ttmn
chunk 9 pts=1800 dur=200 sync=1 size=1496: c1 begin=00:00:01.000 end=00:00:01.900
`

// chunkedStpcBodyTrace adds Layer 2: a changed chunk after the first of its segment sends
// only the body, and the head is spliced from that first chunk.
// nolint:lll
const chunkedStpcBodyTrace = `chunk 0 pts=0 dur=200 sync=1 size=1477: c0 begin=00:00:00.000
chunk 1 pts=200 dur=200 sync=0 size=8: ttmn
chunk 2 pts=400 dur=200 sync=0 size=8: ttmn
chunk 3 pts=600 dur=200 sync=0 size=8: ttmn
chunk 4 pts=800 dur=200 sync=0 size=178: ttmb c0 begin=00:00:00.000 end=00:00:00.900
chunk 5 pts=1000 dur=200 sync=0 size=159: ttmb c1 begin=00:00:01.000
chunk 6 pts=1200 dur=200 sync=0 size=8: ttmn
chunk 7 pts=1400 dur=200 sync=0 size=8: ttmn
chunk 8 pts=1600 dur=200 sync=0 size=8: ttmn
chunk 9 pts=1800 dur=200 sync=0 size=178: ttmb c1 begin=00:00:01.000 end=00:00:01.900
`

// chunkedStpcNoChangeOffTrace is the control: the same track with nochange=0 sends the
// full document in every chunk, exactly as an stpp track does.
// nolint:lll
const chunkedStpcNoChangeOffTrace = `chunk 0 pts=0 dur=200 sync=1 size=1477: c0 begin=00:00:00.000
chunk 1 pts=200 dur=200 sync=1 size=1477: c0 begin=00:00:00.000
chunk 2 pts=400 dur=200 sync=1 size=1477: c0 begin=00:00:00.000
chunk 3 pts=600 dur=200 sync=1 size=1477: c0 begin=00:00:00.000
chunk 4 pts=800 dur=200 sync=1 size=1496: c0 begin=00:00:00.000 end=00:00:00.900
chunk 5 pts=1000 dur=200 sync=1 size=1477: c1 begin=00:00:01.000
chunk 6 pts=1200 dur=200 sync=1 size=1477: c1 begin=00:00:01.000
chunk 7 pts=1400 dur=200 sync=1 size=1477: c1 begin=00:00:01.000
chunk 8 pts=1600 dur=200 sync=1 size=1477: c1 begin=00:00:01.000
chunk 9 pts=1800 dur=200 sync=1 size=1496: c1 begin=00:00:01.000 end=00:00:01.900
`

// chunkedWvtcTrace shows the same for WebVTT, where the no-change box is the peer of the
// empty vtte box: vtte clears the screen, vttn says the cues continue.
// nolint:lll
const chunkedWvtcTrace = `chunk 0 pts=0 dur=200 sync=1 size=55: vttc "1970-01-01T00:00:00Z\nen # 0"
chunk 1 pts=200 dur=200 sync=0 size=8: vttn
chunk 2 pts=400 dur=200 sync=0 size=8: vttn
chunk 3 pts=600 dur=200 sync=0 size=8: vttn
chunk 4 pts=800 dur=100 sync=0 size=8: vttn
chunk 4 pts=900 dur=100 sync=1 size=8: vtte
chunk 5 pts=1000 dur=200 sync=1 size=55: vttc "1970-01-01T00:00:01Z\nen # 0"
chunk 6 pts=1200 dur=200 sync=0 size=8: vttn
chunk 7 pts=1400 dur=200 sync=0 size=8: vttn
chunk 8 pts=1600 dur=200 sync=0 size=8: vttn
chunk 9 pts=1800 dur=100 sync=0 size=8: vttn
chunk 9 pts=1900 dur=100 sync=1 size=8: vtte
`
