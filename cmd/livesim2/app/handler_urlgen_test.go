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

// TestURLGenTemplateRendersSGAI confirms the urlgen.html template still parses and renders
// with the SGAI fields populated.
func TestURLGenTemplateRendersSGAI(t *testing.T) {
	tmpls, err := compileHTMLTemplates(content, "templates")
	require.NoError(t, err)
	r := httptest.NewRequest("GET",
		"/urlgen/create?asset=testpic_2s&mpd=Manifest.mpd&stl=nr&sgai=30:15&sgaiSessionId=alice&sgaiInterests=boats", nil)
	data := createURL(r, testAssetsInfo(), nil)
	var buf bytes.Buffer
	require.NoError(t, tmpls.ExecuteTemplate(&buf, "urlgen.html", data))
	out := buf.String()
	require.Contains(t, out, `name="sgai"`)
	require.Contains(t, out, `name="sgaiSessionId"`)
	require.Contains(t, out, `name="sgaiInterests"`)
	require.Contains(t, out, "value=\"30:15\"")
	require.Contains(t, out, "value=\"alice\"")
}

// TestURLGenAssetOptsOOB confirms that the "assetopts" template (served when the asset <select>
// changes) updates two regions from one response: the MPD options for #mpd and, via an htmx
// out-of-band swap, the DRM options for #drms. A pre-encrypted asset must disable the DRM choice;
// a clear asset must offer the full DRM list.
func TestURLGenAssetOptsOOB(t *testing.T) {
	tmpls, err := compileHTMLTemplates(content, "templates")
	require.NoError(t, err)
	drmCfg := &drm.DrmConfig{Packages: []*drm.Package{{Name: "cbcs-all", Desc: "cbcs all tracks"}}}

	render := func(a *assetInfo) string {
		data := urlGenData{
			MPDs: []nameWithSelect{{Name: "stream.mpd", Selected: true}},
			DRMs: drmsFromAssetInfo(a, drmCfg, ""),
		}
		var buf bytes.Buffer
		require.NoError(t, tmpls.ExecuteTemplate(&buf, "assetopts", data))
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
