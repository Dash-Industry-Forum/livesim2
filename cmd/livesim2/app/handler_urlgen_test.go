// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
