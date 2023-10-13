// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/require"
)

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
		{
			desc:             "period continuity",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "periods_60/continuous_1/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      `<SupplementalProperty schemeIdUri="urn:mpeg:dash:period-continuity:2015" value="1"></SupplementalProperty>`,
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

// TestFetches tests fetching of segments and other content.
func TestFetches(t *testing.T) {
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
		desc              string
		url               string
		params            string
		wantedStatusCode  int
		wantedContentType string
	}{
		{
			desc:              "mpd",
			url:               "testpic_2s_thumbs/Manifest.mpd",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/dash+xml`,
		},
		{
			desc:              "thumbnail image",
			url:               "testpic_2s_thumbs/thumbs/300.jpg?nowMS=610000",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `image/jpeg`,
		},
		{
			desc:              "imsc1 image subtitle",
			url:               "testpic_2s_imsc1/imsc1_img_en/300.cmft?nowMS=610000",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/mp4`,
		},
		{
			desc:              "imsc1 text subtitle",
			url:               "testpic_2s_imsc1/imsc1_txt_sv/300.m4s?nowMS=610000",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/mp4`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			totURL := "/livesim2/" + tc.params + tc.url
			resp, body := testFullRequest(t, ts, "GET", totURL, nil)
			require.Equal(t, tc.wantedStatusCode, resp.StatusCode)
			if tc.wantedStatusCode != http.StatusOK {
				return
			}
			require.Greater(t, len(body), 0, "no body")
			gotContentType := resp.Header.Get("Content-Type")
			require.Equal(t, tc.wantedContentType, gotContentType, "wrong content type for url %s", totURL)
		})
	}
}
