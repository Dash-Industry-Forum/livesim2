// Copyright 2023, DASH-Industry Forum. All rights reserved.
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
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStppTimeMessage(t *testing.T) {

	testCases := []struct {
		lang   string
		utcMS  int
		segNr  int
		wanted string
	}{
		{
			lang:   "en",
			utcMS:  0,
			segNr:  0,
			wanted: "1970-01-01T00:00:00Z<br/>en # 0",
		},
	}

	for _, tc := range testCases {
		got := makeStppMessage(tc.lang, tc.utcMS, tc.segNr)
		require.Equal(t, tc.wanted, got)
	}
}

func TestMSToTTMLTime(t *testing.T) {

	testCases := []struct {
		ms     int
		wanted string
	}{
		{
			ms:     0,
			wanted: "00:00:00.000",
		},
		{
			ms:     36605_230,
			wanted: "10:10:05.230",
		},
	}

	for _, tc := range testCases {
		got := msToTTMLTime(tc.ms)
		require.Equal(t, tc.wanted, got)
	}
}

func TestStppTimeCues(t *testing.T) {
	testCases := []struct {
		nr          uint32
		startTimeMS uint64
		dur         uint32
		startUTCMS  uint64
		lang        string
		wanted      []StppTimeCue
	}{
		{
			nr:          0,
			startTimeMS: 0,
			dur:         2000,
			startUTCMS:  0,
			lang:        "en",
			wanted: []StppTimeCue{
				{
					Id:    "en-0",
					Begin: "00:00:00.000",
					End:   "00:00:00.900",
					Msg:   "1970-01-01T00:00:00Z",
				},
			},
		},
		{
			nr:          0,
			startTimeMS: 3_600_000,
			dur:         2000,
			startUTCMS:  0,
			lang:        "en",
			wanted: []StppTimeCue{
				{
					Id:    "en-0",
					Begin: "00:00:00.000",
					End:   "00:00:00.900",
					Msg:   "1970-01-01T01:00:00Z",
				},
			},
		},
	}

	for _, tc := range testCases {

		require.Equal(t, tc, tc)
	}
}

func TestCalcCueItvls(t *testing.T) {

	testCases := []struct {
		desc     string
		startMS  int
		dur      int
		utcMS    int
		cueDurMS int
		wanted   []cueItvl
	}{
		{
			desc:     "long cue",
			startMS:  0,
			dur:      2000,
			utcMS:    0,
			cueDurMS: 1800,
			wanted: []cueItvl{
				{startMS: 0, endMS: 1800, utcS: 0},
			},
		},
		{
			desc:     "simple case w 2 cues",
			startMS:  0,
			dur:      2000,
			utcMS:    0,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 0, endMS: 900, utcS: 0},
				{startMS: 1000, endMS: 1900, utcS: 1},
			},
		},
		{
			desc:     "simple case w 1 cues",
			startMS:  0,
			dur:      2000,
			utcMS:    0,
			cueDurMS: 1800,
			wanted: []cueItvl{
				{startMS: 0, endMS: 1800, utcS: 0},
			},
		},
		{
			desc:     "utc shifted. Starting 100ms into second",
			startMS:  12000,
			dur:      800,
			utcMS:    12100,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 12000, endMS: 12800, utcS: 12},
			},
		},
		{
			desc:     "utc shifted. long segment",
			startMS:  12000,
			dur:      801,
			utcMS:    12100,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 12000, endMS: 12800, utcS: 12},
			},
		},
		{
			desc:     "utc shifted, somewhat short segment",
			startMS:  12000,
			dur:      799,
			utcMS:    12100,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 12000, endMS: 12799, utcS: 12},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := calcCueItvls(tc.startMS, tc.dur, tc.utcMS, tc.cueDurMS)
			require.Equal(t, tc.wanted, got)
		})
	}
}

const outputStppPayload = `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns:ttp="http://www.w3.org/ns/ttml#parameter" xmlns="http://www.w3.org/ns/ttml"
    xmlns:tts="http://www.w3.org/ns/ttml#styling" xmlns:ttm="http://www.w3.org/ns/ttml#metadata"
    xmlns:ebuttm="urn:ebu:tt:metadata" xmlns:ebutts="urn:ebu:tt:style"
    xml:lang="en" xml:space="default"
    ttp:timeBase="media"
    ttp:cellResolution="32 15">
  <head>
    <metadata>
      <ttm:title>DASH-IF Live Simulator 2</ttm:title>
      <ebuttm:documentMetadata>
        <ebuttm:conformsToStandard>urn:ebu:distribution:2014-01</ebuttm:conformsToStandard>
        <ebuttm:authoredFrameRate>30</ebuttm:authoredFrameRate>
      </ebuttm:documentMetadata>
    </metadata>
    <styling>
      <style xml:id="s0" tts:fontStyle="normal" tts:fontFamily="sansSerif" tts:fontSize="100%" tts:lineHeight="normal"
      tts:color="white" tts:wrapOption="noWrap" tts:textAlign="center" ebutts:linePadding="0.5c"/>
      <style xml:id="s1" tts:color="yellow" tts:backgroundColor="black"/>
      <style xml:id="s2" tts:color="green" tts:backgroundColor="black"/>
    </styling>
    <layout>
      <region xml:id="r0" tts:origin="15% 80%" tts:extent="70% 20%" tts:overflow="visible"/>
      <region xml:id="r1" tts:origin="15% 20%" tts:extent="70% 20%" tts:overflow="visible"/>
    </layout>
  </head>
  <body style="s0">
<div region="r1">
<p xml:id="0-0" begin="00:00:00.000" end="00:00:00.600"><span style="s1">1970-01-01T00:00:00Z<br/>en # 0</span></p>
<p xml:id="0-1" begin="00:00:01.000" end="00:00:01.600"><span style="s1">1970-01-01T00:00:01Z<br/>en # 0</span></p>
</div>
  </body>
</tt>
`

const wvttSamples = `Sample 0, pts=3600000, dur=600
[vttc] size=60
  [sttg] size=14
   - settings: line:2
  [payl] size=38
   - cueText: "1970-01-01T01:00:00Z\nen # 1800"
Sample 1, pts=3600600, dur=400
[vtte] size=8
Sample 2, pts=3601000, dur=600
[vttc] size=60
  [sttg] size=14
   - settings: line:2
  [payl] size=38
   - cueText: "1970-01-01T01:00:01Z\nen # 1800"
Sample 3, pts=3601600, dur=400
[vtte] size=8
`

func TestTimeSubsInitSegment(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()
	testCases := []struct {
		desc               string
		asset              string
		url                string
		segmentMimeType    string
		lang               string
		mediaTimescale     int
		expectedStatusCode int
	}{
		{
			desc:               "init stpp segment with segmtimeline nr",
			asset:              "testpic_2s",
			url:                "/livesim2/segtimelinenr_1/timesubsstpp_en,sv/testpic_2s/timestpp-en/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "en",
			mediaTimescale:     SUBS_TIME_TIMESCALE,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "init stpp segment with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/testpic_2s/timestpp-en/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "en",
			mediaTimescale:     SUBS_TIME_TIMESCALE,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "init segment but language not matching URL => NotFound",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/testpic_2s/timestpp-se/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "se",
			mediaTimescale:     SUBS_TIME_TIMESCALE,
			expectedStatusCode: http.StatusNotFound,
		},
		{
			desc:               "init wvtt segment with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubswvtt_en,sv/testpic_2s/timewvtt-en/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "en",
			mediaTimescale:     SUBS_TIME_TIMESCALE,
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, tc.expectedStatusCode, resp.StatusCode)
			if tc.expectedStatusCode != http.StatusOK {
				return
			}
			sr := bits.NewFixedSliceReader(body)
			mp4d, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			mediaTimescale := int(mp4d.Moov.Trak.Mdia.Mdhd.Timescale)
			assert.Equal(t, SUBS_TIME_TIMESCALE, mediaTimescale)
			lang := mp4d.Moov.Trak.Mdia.Elng.Language
			assert.Equal(t, tc.lang, lang)
		})
	}
}

func testFullRequest(t *testing.T, ts *httptest.Server, method, path string, reqBody io.Reader) (*http.Response, []byte) {
	req, err := http.NewRequest(method, ts.URL+path, reqBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer resp.Body.Close()

	return resp, respBody
}

func TestTimeSubsMediaSegment(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()
	testCases := []struct {
		desc               string
		asset              string
		url                string
		segmentMimeType    string
		startTime          int
		cues               string
		expectedStatusCode int
	}{
		{
			desc:               "stpp segment 0 with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/timesubsdur_600/timesubsreg_1/testpic_2s/timestpp-en/0.m4s?nowMS=10000",
			segmentMimeType:    "application/mp4",
			startTime:          0,
			cues:               outputStppPayload,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "wvtt segment 0 with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubswvtt_en,sv/timesubsdur_600/timesubsreg_1/testpic_2s/timewvtt-en/1800.m4s?nowMS=3610000",
			segmentMimeType:    "application/mp4",
			startTime:          3600_000,
			cues:               wvttSamples,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "wvtt segment 1800 for segtimeline nr",
			asset:              "testpic_2s",
			url:                "/livesim2/segtimelinenr_1/timesubswvtt_en,sv/timesubsdur_600/timesubsreg_1/testpic_2s/timewvtt-en/1800.m4s?nowMS=3610000",
			segmentMimeType:    "application/mp4",
			startTime:          3600_000,
			cues:               wvttSamples,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "wvtt segment 1800 for segtimeline time",
			asset:              "testpic_2s",
			url:                "/livesim2/segtimeline_1/timesubswvtt_en,sv/timesubsdur_600/timesubsreg_1/testpic_2s/timewvtt-en/3600000.m4s?nowMS=3610000",
			segmentMimeType:    "application/mp4",
			startTime:          3600_000,
			cues:               wvttSamples,
			expectedStatusCode: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, tc.expectedStatusCode, resp.StatusCode)
			if tc.expectedStatusCode != http.StatusOK {
				return
			}
			sr := bits.NewFixedSliceReader(body)
			mp4d, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			require.Equal(t, 1, len(mp4d.Segments))
			seg := mp4d.Segments[0]
			require.Equal(t, 1, len(seg.Fragments))
			frag := seg.Fragments[0]
			require.Equal(t, tc.startTime, int(frag.Moof.Traf.Tfdt.BaseMediaDecodeTime()))
			fss, err := frag.GetFullSamples(nil)
			require.NoError(t, err)
			if strings.Contains(tc.url, "stpp") {
				require.Equal(t, 1, len(fss))
				payload := string(fss[0].Data)
				// Replace \r\n with \n to handle accidental Windows line endings
				payload = strings.ReplaceAll(payload, "\r\n", "\n")
				require.Equal(t, tc.cues, payload)
			} else {
				payload, err := genWvttCueText(fss)
				require.NoError(t, err)
				payload = strings.ReplaceAll(payload, "\r\n", "\n")
				require.Equal(t, tc.cues, payload)
			}
		})
	}
}

func genWvttCueText(fss []mp4.FullSample) (string, error) {
	var b strings.Builder

	for nr, fs := range fss {
		b.WriteString(fmt.Sprintf("Sample %d, pts=%d, dur=%d\n", nr, fs.PresentationTime(), fs.Dur))
		buf := bytes.NewBuffer(fs.Data)
		box, err := mp4.DecodeBox(0, buf)
		if err != nil {
			return "", err
		}
		_ = box.Info(&b, "", "", "  ")
	}
	return b.String(), nil
}
