// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
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

const outputPayload = `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns:ttp="http://www.w3.org/ns/ttml#parameter" xmlns="http://www.w3.org/ns/ttml"
    xmlns:tts="http://www.w3.org/ns/ttml#styling" xmlns:ttm="http://www.w3.org/ns/ttml#metadata"
    xmlns:ebuttm="urn:ebu:metadata" xmlns:ebutts="urn:ebu:style"
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
      tts:color="#FFFFFF" tts:wrapOption="noWrap" tts:textAlign="center"/>
      <style xml:id="s1" tts:color="#00FF00" tts:backgroundColor="#000000" ebutts:linePadding="0.5c"/>
      <style xml:id="s2" tts:color="#ff0000" tts:backgroundColor="#000000" ebutts:linePadding="0.5c"/>
    </styling>
    <layout>
      <region xml:id="r0" tts:origin="15% 80%" tts:extent="70% 20%" tts:overflow="visible" tts:displayAlign="before"/>
      <region xml:id="r1" tts:origin="15% 20%" tts:extent="70% 20%" tts:overflow="visible" tts:displayAlign="before"/>
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

func TestTimeStppInitSegment(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	_, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
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
			desc:               "init segment with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/testpic_2s/timestpp-en/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "en",
			mediaTimescale:     SUBS_STPP_TIMESCALE,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "init segment but language not matching URL => NotFound",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/testpic_2s/timestpp-se/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "se",
			mediaTimescale:     SUBS_STPP_TIMESCALE,
			expectedStatusCode: http.StatusNotFound,
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
			assert.Equal(t, SUBS_STPP_TIMESCALE, mediaTimescale)
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

func TestTimeStppMediaSegment(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	_, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
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
		cues               []string
		expectedStatusCode int
	}{
		{
			desc:               "mediasegment 0 with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/timesubsdur_600/timesubsreg_1/testpic_2s/timestpp-en/0.m4s?nowMS=10000",
			segmentMimeType:    "application/mp4",
			cues:               nil,
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
			require.Equal(t, uint64(0), frag.Moof.Traf.Tfdt.BaseMediaDecodeTime())
			fss, err := frag.GetFullSamples(nil)
			require.NoError(t, err)
			require.Equal(t, 1, len(fss))
			payload := string(fss[0].Data)
			// Replace \r\n with \n to handle accidental Windows line endings
			payload = strings.ReplaceAll(payload, "\r\n", "\n")
			require.Equal(t, outputPayload, payload)
		})
	}
}

func TestParamToMPD(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	_, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()
	testCases := []struct {
		desc             string
		mpd              string
		params           string
		wantedStatusCode int
		wantedInMPD      string
	}{
		{
			desc:             "minimumUpdatePeriod",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "mup_1/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      `minimumUpdatePeriod="PT1S"`,
		},
		{
			desc:             "latency target",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "ltgt_2500/ato_1/chunkdur_0.25/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      `<Latency referenceId="0" target="2500" max="5000" min="1875"></Latency>`,
		},
		{
			desc:             "latency target",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "ato_1/chunkdur_0.25/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      `<Latency referenceId="0" target="3500" max="7000" min="2625"></Latency>`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			mpdURL := "/livesim2/" + tc.params + tc.mpd
			resp, body := testFullRequest(t, ts, "GET", mpdURL, nil)
			require.Equal(t, tc.wantedStatusCode, resp.StatusCode)
			if tc.wantedStatusCode != http.StatusOK {
				return
			}
			bodyStr := string(body)
			//fmt.Println(bodyStr)
			require.Greater(t, strings.Index(bodyStr, tc.wantedInMPD), -1, "no match in MPD")
		})
	}
}
