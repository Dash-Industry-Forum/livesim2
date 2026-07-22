// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/require"
)

// TestDetectCC608OnScan checks that asset discovery flags the captioned rep
// (testpic_2s/V300_with_cc1_and_cc3) as carrying CEA-608 and leaves the plain V300
// and the audio rep untouched.
func TestDetectCC608OnScan(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	captioned, ok := asset.Reps["V300_with_cc1_and_cc3"]
	require.True(t, ok)
	require.True(t, captioned.HasCEA608, "captioned rep must be detected")

	plain, ok := asset.Reps["V300"]
	require.True(t, ok)
	require.False(t, plain.HasCEA608, "plain video rep must not be flagged")

	audio, ok := asset.Reps["A48"]
	require.True(t, ok)
	require.False(t, audio.HasCEA608, "audio rep must not be flagged")
}

// TestCC608AlreadyCaptioned exercises both detection signals independently on a
// synthetic period: signal 1 = a CEA-608/708 Accessibility descriptor on the video
// AS, signal 2 = a referenced video rep whose samples carry cc_data SEI.
func TestCC608AlreadyCaptioned(t *testing.T) {
	a := &asset{Reps: map[string]*RepData{
		"vcap":   {ID: "vcap", ContentType: "video", HasCEA608: true},
		"vplain": {ID: "vplain", ContentType: "video", HasCEA608: false},
	}}
	period := func(repID string, accs ...*m.DescriptorType) *m.Period {
		return &m.Period{AdaptationSets: []*m.AdaptationSetType{{
			ContentType:     "video",
			Accessibilities: accs,
			Representations: []*m.RepresentationType{{Id: repID}},
		}}}
	}
	desc608 := &m.DescriptorType{SchemeIdUri: CEA608AccessibilitySchemeIdUri, Value: "CC1=eng"}
	desc708 := &m.DescriptorType{SchemeIdUri: CEA708AccessibilitySchemeIdUri, Value: "1=eng"}

	// Neither signal.
	require.False(t, cc608AlreadyCaptioned(a, period("vplain")))
	// Signal 2 only: rep carries SEI, no descriptor.
	require.True(t, cc608AlreadyCaptioned(a, period("vcap")))
	// Signal 1 only: descriptor present though the rep itself is clean.
	require.True(t, cc608AlreadyCaptioned(a, period("vplain", desc608)))
	require.True(t, cc608AlreadyCaptioned(a, period("vplain", desc708)))

	// A non-video AS with a descriptor must not trigger the reject.
	audioPeriod := &m.Period{AdaptationSets: []*m.AdaptationSetType{{
		ContentType:     "audio",
		Accessibilities: []*m.DescriptorType{desc608},
	}}}
	require.False(t, cc608AlreadyCaptioned(a, audioPeriod))
}

// TestLiveMPDCC608Reject confirms timecc608 is rejected for the captioned manifest
// (cea608.mpd) but accepted for the plain one (Manifest.mpd) of the same asset.
func TestLiveMPDCC608Reject(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)
	const nowMS = 100_000

	cfg := NewResponseConfig()
	cfg.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	_, err := LiveMPD(asset, "cea608.mpd", cfg, nil, nowMS)
	require.ErrorIs(t, err, errCC608AlreadyCaptioned, "captioned manifest must be rejected")

	// The plain manifest of the same asset still works and gets our descriptor.
	cfg2 := NewResponseConfig()
	cfg2.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	mpd, err := LiveMPD(asset, "Manifest.mpd", cfg2, nil, nowMS)
	require.NoError(t, err, "plain manifest must be accepted")
	require.True(t, cc608AlreadyCaptioned(asset, mpd.Periods[0]),
		"our own descriptor is now present on the generated period")
}

// TestGenLiveSegmentCC608RejectCaptioned confirms a direct segment request for the
// captioned rep is rejected, while the plain rep is served.
func TestGenLiveSegmentCC608RejectCaptioned(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)
	const nowMS = 100_000

	cfg := NewResponseConfig()
	cfg.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	_, err := genLiveSegment(logger, vodFS, asset, cfg, "V300_with_cc1_and_cc3/40.m4s", nowMS, false)
	require.ErrorIs(t, err, errCC608AlreadyCaptioned)

	// Plain rep of the same asset is injected, not rejected.
	_, err = genLiveSegment(logger, vodFS, asset, cfg, "V300/40.m4s", nowMS, false)
	require.NoError(t, err)
}

// TestCC608RejectHTTP checks the end-to-end status codes: 400 for a timecc608 request
// against the captioned manifest and one of its segments, 200 for the plain manifest.
func TestCC608RejectHTTP(t *testing.T) {
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	cases := []struct {
		desc string
		url  string
		want int
	}{
		{"captioned mpd rejected", "/livesim2/timecc608_CC1-eng/testpic_2s/cea608.mpd", http.StatusBadRequest},
		{"captioned segment rejected", "/livesim2/timecc608_CC1-eng/testpic_2s/V300_with_cc1_and_cc3/40.m4s?nowMS=100000", http.StatusBadRequest},
		{"plain mpd accepted", "/livesim2/timecc608_CC1-eng/testpic_2s/Manifest.mpd", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, _ := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, tc.want, resp.StatusCode)
		})
	}
}
