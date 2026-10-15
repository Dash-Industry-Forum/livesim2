// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/c2pavsi"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func c2paTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := ServerConfig{VodRoot: "testdata/assets", LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	t.Cleanup(ts.Close)
	return ts
}

func c2paGet(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// c2paPublicKey is the session public key of a testpic_2s representation's pre-signed init.
func c2paPublicKey(t *testing.T, rep string) ed25519.PublicKey {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "assets", "testpic_2s", c2paDir, rep, c2paInfoName))
	require.NoError(t, err)
	var info c2paVSIInfo
	require.NoError(t, json.Unmarshal(data, &info))
	seed, err := hex.DecodeString(info.Seed)
	require.NoError(t, err)
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
}

func emsgSchemes(t *testing.T, seg []byte) []string {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(seg))
	require.NoError(t, err)
	var schemes []string
	for _, frag := range f.Segments[0].Fragments {
		for _, e := range frag.Emsgs {
			schemes = append(schemes, e.SchemeIDURI)
		}
	}
	return schemes
}

func TestC2PAVSIMPD(t *testing.T) {
	ts := c2paTestServer(t)
	status, body := c2paGet(t, ts.URL+"/livesim2/c2pa_vsi/testpic_2s/Manifest.mpd?nowMS=610000")
	require.Equal(t, http.StatusOK, status, string(body))
	declaration := `<InbandEventStream schemeIdUri="urn:c2pa:verifiable-segment-info" value="fseg">`
	assert.Equal(t, 2, strings.Count(string(body), declaration), "one per audio and video AdaptationSet")

	status, body = c2paGet(t, ts.URL+"/livesim2/testpic_2s/Manifest.mpd?nowMS=610000")
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(body), c2pavsi.SchemeIDURI, "only with c2pa_vsi")
}

func TestC2PAVSISegments(t *testing.T) {
	ts := c2paTestServer(t)
	for _, rep := range []string{"V300", "A48"} {
		t.Run(rep, func(t *testing.T) {
			base := ts.URL + "/livesim2/c2pa_vsi/testpic_2s/" + rep

			status, init := c2paGet(t, base+"/init.mp4?nowMS=610000")
			require.Equal(t, http.StatusOK, status)
			preSigned, err := os.ReadFile(filepath.Join("testdata", "assets", "testpic_2s", c2paDir, rep, c2paInitName))
			require.NoError(t, err)
			assert.Equal(t, preSigned, init, "the pre-signed init segment is served")

			initSeg, err := mp4.DecodeFile(bytes.NewReader(init))
			require.NoError(t, err)
			timescale := initSeg.Init.Moov.Trak.Mdia.Mdhd.Timescale

			for _, nr := range []int{299, 300} {
				status, seg := c2paGet(t, fmt.Sprintf("%s/%d.m4s?nowMS=610000", base, nr))
				require.Equal(t, http.StatusOK, status)
				info, err := c2pavsi.Verify(seg, c2paPublicKey(t, rep))
				require.NoError(t, err, "segment %d", nr)
				assert.Equal(t, uint64(nr), info.SequenceNumber)

				emsg, err := c2pavsi.FindEmsg(seg)
				require.NoError(t, err)
				assert.Equal(t, timescale, emsg.Timescale)
				assert.Zero(t, emsg.PresentationTimeDelta)
				assert.NotZero(t, emsg.EventDuration)

				// Signing is deterministic, so repeated requests return the same bytes.
				_, again := c2paGet(t, fmt.Sprintf("%s/%d.m4s?nowMS=610000", base, nr))
				assert.Equal(t, seg, again)
			}
		})
	}
}

// TestC2PAVSIWithSCTE35 checks that SCTE-35 emsg boxes are signed along with the media: a
// segment carrying a cue also carries the VSI emsg and verifies.
func TestC2PAVSIWithSCTE35(t *testing.T) {
	ts := c2paTestServer(t)
	withCue := 0
	for nr := 270; nr <= 300; nr++ { // a minute of 2 s segments
		url := fmt.Sprintf("%s/livesim2/c2pa_vsi/scte35_1/testpic_2s/V300/%d.m4s?nowMS=610000", ts.URL, nr)
		status, seg := c2paGet(t, url)
		require.Equal(t, http.StatusOK, status)
		_, err := c2pavsi.Verify(seg, c2paPublicKey(t, "V300"))
		require.NoError(t, err, "segment %d", nr)
		schemes := emsgSchemes(t, seg)
		if len(schemes) > 1 {
			withCue++
			assert.Equal(t, c2pavsi.SchemeIDURI, schemes[0], "the VSI emsg comes first")
		}
	}
	assert.NotZero(t, withCue, "a segment with an SCTE-35 cue")
}

func TestC2PAVSIRejections(t *testing.T) {
	ts := c2paTestServer(t)
	for _, tc := range []struct {
		desc, url, wantErr string
	}{
		{"unknown method", "c2pa_manifest/testpic_2s/Manifest.mpd", "unknown method"},
		{"chunked", "c2pa_vsi/chunkdur_0.5/testpic_2s/Manifest.mpd", "chunked delivery"},
		{"drm", "c2pa_vsi/drm_EZDRM1/testpic_2s/Manifest.mpd", "drm"},
		{"time subtitles", "c2pa_vsi/timesubsstpp_en/testpic_2s/Manifest.mpd", "time subtitles"},
		{"no pre-signed init (MPD)", "c2pa_vsi/testpic_8s/Manifest.mpd", errC2PAUnavailable.Error()},
		{"no pre-signed init (init)", "c2pa_vsi/testpic_8s/V300/init.mp4", errC2PAUnavailable.Error()},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			status, body := c2paGet(t, ts.URL+"/livesim2/"+tc.url+"?nowMS=610000")
			assert.Equal(t, http.StatusBadRequest, status)
			assert.Contains(t, string(body), tc.wantErr)
		})
	}
}
