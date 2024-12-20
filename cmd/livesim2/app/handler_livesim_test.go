// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
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
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
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
		wantedInMPD      []string
	}{
		{
			desc:             "minimumUpdatePeriod",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "mup_1/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      []string{`minimumUpdatePeriod="PT1S"`},
		},
		{
			desc:             "latency target",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "ltgt_2500/ato_1/chunkdur_0.25/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      []string{`<Latency referenceId="0" target="2500" max="5000" min="1875"></Latency>`},
		},
		{
			desc:             "latency target",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "ato_1/chunkdur_0.25/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      []string{`<Latency referenceId="0" target="3500" max="7000" min="2625"></Latency>`},
		},
		{
			desc:             "period continuity",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "periods_60/continuous_1/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD:      []string{`<SupplementalProperty schemeIdUri="urn:mpeg:dash:period-continuity:2015" value="1"></SupplementalProperty>`},
		},
		{
			desc:             "ECCP ClearKey CBCS",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "eccp_cbcs/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD: []string{
				`<ContentProtection xmlns:cenc="urn:mpeg:cenc:2013" cenc:default_KID="2880fe36-e44b-f9bf-79d2-752e234818a5" schemeIdUri="urn:mpeg:dash:mp4protection:2011" value="cbcs"></ContentProtection>`,
				`<ContentProtection schemeIdUri="urn:uuid:e2719d58-a985-b3c9-781a-b030af78d30e" value="ClearKey1.0">`,
				`<dashif:Laurl xmlns:dashif="https://dashif.org/CPS" licenseType="EME-1.0">`,
			},
		},
		{
			desc:             "MPD with Patch",
			mpd:              "testpic_6s/Manifest.mpd",
			params:           "patch_60/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD: []string{
				`id="auto-patch-id"`, // id in MPD
				`id="1"`,             // id in AdaptationSet
				`id="2"`,             // id in AdaptationSet
				`<PatchLocation ttl="60">/patch/livesim2/patch_60/testpic_6s/Manifest.mpp?publishTime=`, // PatchLocation
			},
		},
		{
			desc:             "annexI without url query",
			mpd:              "testpic_2s/Manifest.mpd",
			params:           "annexI_a=1,b=3,a=3/",
			wantedStatusCode: http.StatusBadRequest,
			wantedInMPD:      nil,
		},
		{
			desc:             "annexI with url query",
			mpd:              "testpic_2s/Manifest.mpd?a=1&b=3&a=3",
			params:           "annexI_a=1,b=3,a=3/",
			wantedStatusCode: http.StatusOK,
			wantedInMPD: []string{
				`<up:UrlQueryInfo xmlns:up="urn:mpeg:dash:schema:urlparam:2014" queryTemplate="$querypart$" useMPDUrlQuery="true"></up:UrlQueryInfo>`,
			},
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
			for _, wanted := range tc.wantedInMPD {
				require.Greater(t, strings.Index(bodyStr, wanted), -1, fmt.Sprintf("no match in MPD for %s", wanted))
			}
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
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
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
			url:               "testpic_2s/Manifest_thumbs.mpd",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/dash+xml`,
		},
		{
			desc:              "mpd",
			url:               "testpic_2s/Manifest_thumbs.mpd",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/dash+xml`,
		},
		{
			desc:              "thumbnail image",
			url:               "testpic_2s/thumbs/300.jpg?nowMS=610000",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `image/jpeg`,
		},
		{
			desc:              "imsc1 image subtitle",
			url:               "testpic_2s/imsc1_img_en/300.m4s?nowMS=610000",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/mp4`,
		},
		{
			desc:              "imsc1 text subtitle",
			url:               "testpic_2s/imsc1_txt_sv/300.m4s?nowMS=610000",
			params:            "",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `application/mp4`,
		},
		{
			desc:              "encrypted init segment",
			url:               "testpic_2s/V300/init.mp4",
			params:            "eccp_cbcs/",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `video/mp4`,
		},
		{
			desc:              "encrypted media segment cbcs",
			url:               "testpic_2s/V300/300.m4s?nowMS=610000",
			params:            "eccp_cbcs/",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `video/mp4`,
		},
		{
			desc:              "encrypted media segment cenc",
			url:               "testpic_2s/V300/300.m4s?nowMS=610000",
			params:            "eccp_cenc/",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `video/mp4`,
		},
		{
			desc:              "thumbnail image too early",
			url:               "testpic_2s/thumbs/300.jpg?nowMS=510000",
			params:            "",
			wantedStatusCode:  425,
			wantedContentType: `image/jpeg`,
		},
		{
			desc:              "media segment too early",
			url:               "testpic_2s/V300/300.m4s?nowMS=510000",
			params:            "",
			wantedStatusCode:  425,
			wantedContentType: `video/mp4`,
		},
		{
			desc:             "video init segment Annex I, without query",
			url:              "testpic_2s/V300/init.mp4",
			params:           "annexI_a=1/",
			wantedStatusCode: http.StatusBadRequest,
		},
		{
			desc:              "video init segment Annex I, with query",
			url:               "testpic_2s/V300/init.mp4?a=1",
			params:            "annexI_a=1/",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `video/mp4`,
		},
		{
			desc:              "audio init segment Annex I, without query",
			url:               "testpic_2s/A48/init.mp4",
			params:            "annexI_a=1/",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: `audio/mp4`,
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
