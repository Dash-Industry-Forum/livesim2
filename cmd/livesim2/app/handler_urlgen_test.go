// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/require"
)

// testAssetsInfo returns a minimal assetsInfo with one asset/MPD for urlgen tests.
func testAssetsInfo() assetsInfo {
	return assetsInfo{
		Host:    "http://localhost:8888",
		PlayURL: "http://play/%s",
		Assets: []*assetInfo{
			{Path: "testpic_2s", MPDs: []mpdInfo{{Path: "Manifest.mpd"}}},
		},
	}
}

func TestCreateURLSVTA(t *testing.T) {
	cases := []struct {
		desc      string
		params    map[string]string
		wantInURL []string
		wantErr   string
	}{
		{
			desc:      "breaks only",
			params:    map[string]string{"svta": "30:15,90:15"},
			wantInURL: []string{"/livesim2/svta_30:15,90:15/testpic_2s/Manifest.mpd"},
		},
		{
			desc: "periodic with options and a session id",
			params: map[string]string{
				"svta":          "p60:20;ads=2;click=1",
				"sgaiSessionId": "alice",
			},
			wantInURL: []string{
				"/livesim2/svta_p60:20;ads=2;click=1/testpic_2s/Manifest.mpd",
				"?sessionId=alice",
			},
		},
		{
			desc:    "invalid break",
			params:  map[string]string{"svta": "bad"},
			wantErr: "invalid svta",
		},
		{
			desc:    "unknown option",
			params:  map[string]string{"svta": "30:15;foo=1"},
			wantErr: `unknown svta param "foo"`,
		},
		{
			desc:    "svta with periods is rejected",
			params:  map[string]string{"svta": "30:15", "periods": "2"},
			wantErr: "svta cannot be combined with periods",
		},
		{
			desc:    "svta with sgai is rejected",
			params:  map[string]string{"svta": "30:15", "sgai": "30:15"},
			wantErr: "svta cannot be combined with sgai",
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			q := url.Values{}
			q.Set("asset", "testpic_2s")
			q.Set("mpd", "Manifest.mpd")
			q.Set("stl", "nr")
			for k, v := range c.params {
				q.Set(k, v)
			}
			r := httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil)
			data := createURL(r, testAssetsInfo(), nil)
			if c.wantErr != "" {
				require.NotEmpty(t, data.Errors, "expected an error")
				require.Contains(t, strings.Join(data.Errors, " | "), c.wantErr)
				require.Empty(t, data.URL, "no URL should be produced on error")
				return
			}
			require.Empty(t, data.Errors, "unexpected errors: %v", data.Errors)
			for _, want := range c.wantInURL {
				require.Contains(t, data.URL, want)
			}
		})
	}
}

func TestCreateURLSGAI(t *testing.T) {
	cases := []struct {
		desc      string
		params    map[string]string
		wantInURL []string
		wantErr   string
	}{
		{
			desc:      "breaks only",
			params:    map[string]string{"sgai": "30:15,90:15"},
			wantInURL: []string{"/livesim2/sgai_30:15,90:15/testpic_2s/Manifest.mpd"},
		},
		{
			desc: "breaks with options and personalization",
			params: map[string]string{
				"sgai":          "30:15;skipafter=5;nojump=2",
				"sgaiSessionId": "alice",
				"sgaiInterests": "boats,sailing",
			},
			wantInURL: []string{
				"/livesim2/sgai_30:15;skipafter=5;nojump=2/testpic_2s/Manifest.mpd",
				"?sessionId=alice&interests=boats,sailing",
			},
		},
		{
			desc:    "invalid break",
			params:  map[string]string{"sgai": "bad"},
			wantErr: "invalid sgai",
		},
		{
			desc:    "sgai with periods is rejected",
			params:  map[string]string{"sgai": "30:15", "periods": "2"},
			wantErr: "sgai cannot be combined with periods",
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			q := url.Values{}
			q.Set("asset", "testpic_2s")
			q.Set("mpd", "Manifest.mpd")
			q.Set("stl", "nr")
			for k, v := range c.params {
				q.Set(k, v)
			}
			r := httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil)
			data := createURL(r, testAssetsInfo(), nil)
			if c.wantErr != "" {
				require.NotEmpty(t, data.Errors, "expected an error")
				require.Contains(t, strings.Join(data.Errors, " | "), c.wantErr)
				require.Empty(t, data.URL, "no URL should be produced on error")
				return
			}
			require.Empty(t, data.Errors, "unexpected errors: %v", data.Errors)
			for _, want := range c.wantInURL {
				require.Contains(t, data.URL, want)
			}
		})
	}
}

func TestCreateURLCC608(t *testing.T) {
	cases := []struct {
		desc      string
		params    map[string]string
		wantInURL []string
		wantErr   string
	}{
		{
			desc:      "valid CC1-eng",
			params:    map[string]string{"timecc608": "CC1-eng"},
			wantInURL: []string{"/livesim2/timecc608_CC1-eng/testpic_2s/Manifest.mpd"},
		},
		{
			desc:      "self-contained pop-on CC1-eng-pop-sc",
			params:    map[string]string{"timecc608": "CC1-eng-pop-sc"},
			wantInURL: []string{"/livesim2/timecc608_CC1-eng-pop-sc/testpic_2s/Manifest.mpd"},
		},
		{
			desc:      "roll-up CC1-eng-roll3",
			params:    map[string]string{"timecc608": "CC1-eng-roll3"},
			wantInURL: []string{"/livesim2/timecc608_CC1-eng-roll3/testpic_2s/Manifest.mpd"},
		},
		{
			desc:    "bare sc without a mode",
			params:  map[string]string{"timecc608": "CC1-eng-sc"},
			wantErr: "invalid timecc608",
		},
		{
			desc:    "unsupported channel",
			params:  map[string]string{"timecc608": "CC2-eng"},
			wantErr: "invalid timecc608",
		},
		{
			desc:    "malformed value",
			params:  map[string]string{"timecc608": "bad"},
			wantErr: "invalid timecc608",
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			q := url.Values{}
			q.Set("asset", "testpic_2s")
			q.Set("mpd", "Manifest.mpd")
			q.Set("stl", "nr")
			for k, v := range c.params {
				q.Set(k, v)
			}
			r := httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil)
			data := createURL(r, testAssetsInfo(), nil)
			if c.wantErr != "" {
				require.NotEmpty(t, data.Errors, "expected an error")
				require.Contains(t, strings.Join(data.Errors, " | "), c.wantErr)
				require.Empty(t, data.URL, "no URL should be produced on error")
				return
			}
			require.Empty(t, data.Errors, "unexpected errors: %v", data.Errors)
			require.Equal(t, c.params["timecc608"], data.TimeCC608, "the form field keeps what was entered")
			for _, want := range c.wantInURL {
				require.Contains(t, data.URL, want)
			}
		})
	}
}

func TestCreateURLSteering(t *testing.T) {
	cases := []struct {
		desc      string
		params    map[string]string
		wantInURL []string
		wantErr   string
	}{
		{
			desc:      "locations only",
			params:    map[string]string{"steer": "alpha,beta;ttl=20"},
			wantInURL: []string{"/livesim2/steer_alpha,beta;ttl=20/testpic_2s/Manifest.mpd"},
		},
		{
			desc: "with session id",
			params: map[string]string{
				"steer":          "alpha,beta;mode=trigger",
				"steerSessionId": "alice",
			},
			wantInURL: []string{
				"/livesim2/steer_alpha,beta;mode=trigger/testpic_2s/Manifest.mpd",
				"?sessionId=alice",
			},
		},
		{
			desc: "with group csid",
			params: map[string]string{
				"steer":          "alpha,beta;mode=trigger",
				"steerCsid":      "groupA",
				"steerSessionId": "alice",
			},
			wantInURL: []string{
				"/livesim2/csid_groupA/steer_alpha,beta;mode=trigger/testpic_2s/Manifest.mpd",
				"?sessionId=alice",
			},
		},
		{
			desc:    "invalid spec",
			params:  map[string]string{"steer": "onlyone"},
			wantErr: "invalid steer",
		},
		{
			desc:    "invalid csid",
			params:  map[string]string{"steer": "alpha,beta", "steerCsid": "bad/slash"},
			wantErr: "invalid steerCsid",
		},
		{
			desc:    "steer with traffic is rejected",
			params:  map[string]string{"steer": "a,b", "traffic": "u20:d5"},
			wantErr: "steer cannot be combined with traffic",
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			q := url.Values{}
			q.Set("asset", "testpic_2s")
			q.Set("mpd", "Manifest.mpd")
			q.Set("stl", "nr")
			for k, v := range c.params {
				q.Set(k, v)
			}
			r := httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil)
			data := createURL(r, testAssetsInfo(), nil)
			if c.wantErr != "" {
				require.NotEmpty(t, data.Errors, "expected an error")
				require.Contains(t, strings.Join(data.Errors, " | "), c.wantErr)
				require.Empty(t, data.URL, "no URL should be produced on error")
				return
			}
			require.Empty(t, data.Errors, "unexpected errors: %v", data.Errors)
			for _, want := range c.wantInURL {
				require.Contains(t, data.URL, want)
			}
		})
	}
}

// TestURLGenTemplateRendersSGAI confirms the urlgenPage templ component renders
// with the SGAI fields populated.
func TestURLGenTemplateRendersSGAI(t *testing.T) {
	r := httptest.NewRequest("GET",
		"/urlgen/create?asset=testpic_2s&mpd=Manifest.mpd&stl=nr&sgai=30:15&sgaiSessionId=alice&sgaiInterests=boats", nil)
	data := createURL(r, testAssetsInfo(), nil)
	var buf bytes.Buffer
	require.NoError(t, urlgenPage(data).Render(context.Background(), &buf))
	out := buf.String()
	require.Contains(t, out, `name="sgai"`)
	require.Contains(t, out, `name="sgaiSessionId"`)
	require.Contains(t, out, `name="sgaiInterests"`)
	require.Contains(t, out, `value="30:15"`)
	require.Contains(t, out, `value="alice"`)
}

// TestURLGenTemplateRendersSVTA confirms the urlgenPage templ component renders
// with the SVTA2053 ad-creative-signaling field populated.
func TestURLGenTemplateRendersSVTA(t *testing.T) {
	q := url.Values{}
	q.Set("asset", "testpic_2s")
	q.Set("mpd", "Manifest.mpd")
	q.Set("stl", "nr")
	q.Set("svta", "p60:20;ads=2")
	q.Set("sgaiSessionId", "alice")
	r := httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil)
	data := createURL(r, testAssetsInfo(), nil)
	require.Empty(t, data.Errors)
	var buf bytes.Buffer
	require.NoError(t, urlgenPage(data).Render(context.Background(), &buf))
	out := buf.String()
	require.Contains(t, out, `name="svta"`)
	require.Contains(t, out, `value="p60:20;ads=2"`)
	require.Contains(t, out, `value="alice"`)
	require.Contains(t, out, "svta_p60:20;ads=2/testpic_2s/Manifest.mpd")
}

// TestURLGenAssetOptsOOB confirms that the "assetopts" template (served when the asset <select>
// changes) updates two regions from one response: the MPD options for #mpd and, via an htmx
// out-of-band swap, the DRM options for #drms. A pre-encrypted asset must disable the DRM choice;
// a clear asset must offer the full DRM list.
func TestURLGenAssetOptsOOB(t *testing.T) {
	drmCfg := &drm.DrmConfig{Packages: []*drm.Package{{Name: "cbcs-all", Desc: "cbcs all tracks"}}}

	render := func(a *assetInfo) string {
		data := urlGenData{
			MPDs: []nameWithSelect{{Name: "stream.mpd", Selected: true}},
			DRMs: drmsFromAssetInfo(a, drmCfg, ""),
		}
		var buf bytes.Buffer
		require.NoError(t, assetOpts(data).Render(context.Background(), &buf))
		return buf.String()
	}

	// Pre-encrypted: the DRM options collapse to a single disabled "None" with an explanation,
	// and none of the real DRM choices are offered.
	preEnc := render(&assetInfo{Path: "encrypted/asset", PreEncrypted: true})
	require.Contains(t, preEnc, `value="stream.mpd"`, "MPD option must be present for #mpd")
	require.Contains(t, preEnc, `hx-swap-oob="innerHTML"`, "DRM block must be an out-of-band swap")
	require.Contains(t, preEnc, `id="drms"`, "OOB element must target #drms")
	require.Contains(t, preEnc, "pre-encrypted", "must explain why DRM is unavailable")
	require.Contains(t, preEnc, "disabled", "the lone DRM choice must be disabled")
	require.NotContains(t, preEnc, `value="eccp-cbcs"`, "no DRM choices for a pre-encrypted asset")
	require.NotContains(t, preEnc, `value="cbcs-all"`, "no commercial DRM for a pre-encrypted asset")

	// Clear asset: the full DRM list is offered and nothing is disabled.
	clear := render(&assetInfo{Path: "clear/asset", PreEncrypted: false})
	require.Contains(t, clear, `hx-swap-oob="innerHTML"`, "DRM block must still be an out-of-band swap")
	require.Contains(t, clear, `value="eccp-cbcs"`)
	require.Contains(t, clear, `value="eccp-cenc"`)
	require.Contains(t, clear, `value="cbcs-all"`)
	require.NotContains(t, clear, "disabled", "no DRM choice should be disabled for a clear asset")
}

// TestURLGenMpdsEndpointUpdatesDRMsOOB drives the /urlgen/mpds endpoint over HTTP and confirms a
// single response updates two regions: the MPD <select> (#mpd) and, out-of-band, the DRM options
// (#drms). This is the wiring that lets the DRM choices follow the selected asset.
func TestURLGenMpdsEndpointUpdatesDRMsOOB(t *testing.T) {
	drmCfg, err := drm.ReadDrmConfig("testdata/drm.json")
	require.NoError(t, err)
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
		DrmCfg:    drmCfg,
	}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, body := testFullRequest(t, ts, "GET", "/urlgen/mpds?asset=testpic_2s", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	out := string(body)
	require.Contains(t, out, "Manifest.mpd", "MPD options for #mpd must be present")
	require.Contains(t, out, `id="drms" hx-swap-oob="innerHTML"`, "DRM block must be an out-of-band swap into #drms")
	require.Contains(t, out, `value="eccp-cbcs"`, "a clear asset must still offer the DRM choices")
}

func TestCreateURLInbandPS(t *testing.T) {
	q := url.Values{}
	q.Set("asset", "testpic_2s")
	q.Set("mpd", "Manifest.mpd")
	q.Set("stl", "nr")
	q.Set("inbandps", "on")
	r := httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil)
	data := createURL(r, testAssetsInfo(), nil)
	require.Empty(t, data.Errors)
	require.True(t, data.InbandPS)
	require.Contains(t, data.URL, "/inbandps_1/")
	require.Contains(t, data.URL, "/testpic_2s/Manifest.mpd")
}

func TestCreateURLC2PA(t *testing.T) {
	cases := []struct {
		desc      string
		params    map[string]string
		noC2PA    bool // the server runs without --c2pa
		wantInURL string
		wantErr   string
	}{
		{desc: "vsi", params: map[string]string{"c2pa": "vsi"}, wantInURL: "/livesim2/c2pa_vsi/testpic_2s/Manifest.mpd"},
		{desc: "none", params: map[string]string{"c2pa": ""}, wantInURL: "/livesim2/testpic_2s/Manifest.mpd"},
		{desc: "server without --c2pa", params: map[string]string{"c2pa": "vsi"}, noC2PA: true,
			wantErr: "c2pa needs a server started with --c2pa"},
		{desc: "unknown method", params: map[string]string{"c2pa": "foo"}, wantErr: `c2pa: unknown method "foo"`},
		{desc: "with ECCP", params: map[string]string{"c2pa": "vsi", "drm": "eccp-cbcs"},
			wantErr: "c2pa_vsi cannot be combined with drm or eccp"},
		{desc: "with chunks", params: map[string]string{"c2pa": "vsi", "chunkdur": "0.5"},
			wantErr: "c2pa_vsi cannot be combined with chunked delivery (chunkdur, ssras)"},
		{desc: "with time subtitles", params: map[string]string{"c2pa": "vsi", "timesubswvtt": "en"},
			wantErr: "c2pa_vsi cannot be combined with generated time subtitles"},
		{desc: "with inbandps", params: map[string]string{"c2pa": "vsi", "inbandps": "on"},
			wantErr: "c2pa_vsi cannot be combined with inbandps"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			q := url.Values{}
			q.Set("asset", "testpic_2s")
			q.Set("mpd", "Manifest.mpd")
			q.Set("stl", "nr")
			for k, v := range c.params {
				q.Set(k, v)
			}
			aInfo := testAssetsInfo()
			aInfo.C2PA = !c.noC2PA
			data := createURL(httptest.NewRequest("GET", "/urlgen/create?"+q.Encode(), nil), aInfo, nil)
			if c.wantErr != "" {
				require.Equal(t, []string{c.wantErr}, data.Errors)
				require.Empty(t, data.URL)
				return
			}
			require.Empty(t, data.Errors)
			require.Contains(t, data.URL, c.wantInURL)
			// The URL is accepted by the live handler's own parser.
			_, err := processURLCfg(strings.TrimPrefix(data.URL, aInfo.Host), 0)
			require.NoError(t, err)
		})
	}
}

// TestURLGenTemplateC2PA checks that the C2PA settings are only shown when the server runs with
// --c2pa, with the chosen method selected.
func TestURLGenTemplateC2PA(t *testing.T) {
	render := func(c2pa bool, query string) string {
		aInfo := testAssetsInfo()
		aInfo.C2PA = c2pa
		data := createURL(httptest.NewRequest("GET", "/urlgen/create?asset=testpic_2s&mpd=Manifest.mpd&stl=nr"+query, nil),
			aInfo, nil)
		var buf bytes.Buffer
		require.NoError(t, urlgenPage(data).Render(context.Background(), &buf))
		return buf.String()
	}
	require.NotContains(t, render(false, ""), `name="c2pa"`, "hidden without --c2pa")
	out := render(true, "&c2pa=vsi")
	require.Contains(t, out, `<select id="c2pa" name="c2pa">`)
	require.Contains(t, out, `<option value="vsi" selected>`)
	require.Contains(t, out, "c2pa_vsi/testpic_2s/Manifest.mpd")

	aInfo := testAssetsInfo()
	aInfo.C2PA = true
	data, err := (&Server{Cfg: &ServerConfig{}}).createInitData(aInfo)
	require.NoError(t, err)
	require.True(t, data.C2PAAvailable, "the start page shows the settings too")
}
