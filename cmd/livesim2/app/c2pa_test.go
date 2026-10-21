// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/c2pavsi"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/richardwooding/c2pa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// c2paTestServer starts a test server with the c2pa option.
func c2paTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	return c2paTestServerWith(t, true)
}

func c2paTestServerWith(t *testing.T, enableC2PA bool) (*httptest.Server, *Server) {
	t.Helper()
	cfg := ServerConfig{VodRoot: "testdata/assets", LogFormat: logging.LogDiscard, C2PA: enableC2PA}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	t.Cleanup(ts.Close)
	return ts, server
}

// c2paRepOf returns the C2PA init and session key signer livesim2 made for a testpic_2s
// representation at startup.
func c2paRepOf(t *testing.T, server *Server, rep string) *c2paRep {
	t.Helper()
	a, ok := server.assetMgr.findAsset("testpic_2s")
	require.True(t, ok)
	rd, ok := a.Reps[rep]
	require.True(t, ok)
	require.NotNil(t, rd.c2pa, "rep %s has a signed init", rep)
	return rd.c2pa
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

// c2paBoxUUID identifies a uuid box holding a C2PA Manifest Store (C2PA §A.5.1).
var c2paBoxUUID = []byte{0xd8, 0xfe, 0xc3, 0xd6, 0x1b, 0x0e, 0x48, 0x3c, 0x92, 0x97, 0x58, 0x28, 0x87, 0x7e, 0xc4, 0x81}

// withoutC2PABox returns an init segment without its top-level C2PA Manifest Store box.
func withoutC2PABox(t *testing.T, data []byte) []byte {
	t.Helper()
	var out []byte
	for pos := 0; pos < len(data); {
		require.GreaterOrEqual(t, len(data)-pos, 8)
		size := int(binary.BigEndian.Uint32(data[pos:]))
		require.True(t, size >= 8 && pos+size <= len(data), "box size at %d", pos)
		b := data[pos : pos+size]
		isC2PA := string(b[4:8]) == "uuid" && size >= 24 && bytes.Equal(b[8:24], c2paBoxUUID)
		if !isC2PA {
			out = append(out, b...)
		}
		pos += size
	}
	return out
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
	ts, _ := c2paTestServer(t)
	status, body := c2paGet(t, ts.URL+"/livesim2/c2pa_vsi/testpic_2s/Manifest.mpd?nowMS=610000")
	require.Equal(t, http.StatusOK, status, string(body))
	declaration := `<InbandEventStream schemeIdUri="urn:c2pa:verifiable-segment-info" value="fseg">`
	assert.Equal(t, 2, strings.Count(string(body), declaration), "one per audio and video AdaptationSet")

	status, body = c2paGet(t, ts.URL+"/livesim2/testpic_2s/Manifest.mpd?nowMS=610000")
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(body), c2pavsi.SchemeIDURI, "only with c2pa_vsi")
}

func TestC2PAVSISegments(t *testing.T) {
	ts, server := c2paTestServer(t)
	ids := map[int]map[uint32]string{} // segment number → emsg id → rep
	for _, rep := range []string{"V300", "A48"} {
		t.Run(rep, func(t *testing.T) {
			base := ts.URL + "/livesim2/c2pa_vsi/testpic_2s/" + rep

			c2paRep := c2paRepOf(t, server, rep)
			status, init := c2paGet(t, base+"/init.mp4?nowMS=610000")
			require.Equal(t, http.StatusOK, status)
			assert.Equal(t, c2paRep.init, init, "the signed init segment is served")

			// The manifest verifies, binds the init segment and announces the session key.
			res := c2pa.Validate(context.Background(), c2pa.BMFF, bytes.NewReader(init))
			assert.Equal(t, c2paRep.signer.ManifestID(), res.ActiveManifestLabel)
			codes := map[c2pa.StatusCode]bool{}
			for _, st := range res.Statuses {
				codes[st.Code] = true
			}
			assert.True(t, codes[c2pa.StatusClaimSignatureValidated], "claim signature")
			assert.True(t, codes[c2pa.StatusAssertionBMFFHashMatch], "bmff hash")
			assert.Contains(t, res.Info.ClaimGenerator, "livesim2")
			assert.True(t, bytes.Contains(init, []byte("1970-01-01T00:00:00Z")), "session key createdAt is the Unix epoch")
			_, plain := c2paGet(t, ts.URL+"/livesim2/testpic_2s/"+rep+"/init.mp4?nowMS=610000")
			assert.Equal(t, plain, withoutC2PABox(t, init), "the signed init is the plain init plus the manifest box")

			initSeg, err := mp4.DecodeFile(bytes.NewReader(init))
			require.NoError(t, err)
			timescale := initSeg.Init.Moov.Trak.Mdia.Mdhd.Timescale

			for _, nr := range []int{299, 300} {
				status, seg := c2paGet(t, fmt.Sprintf("%s/%d.m4s?nowMS=610000", base, nr))
				require.Equal(t, http.StatusOK, status)
				info, err := c2pavsi.Verify(seg, c2paRep.signer.PublicKey())
				require.NoError(t, err, "segment %d", nr)
				assert.Equal(t, uint64(nr), info.SequenceNumber)
				assert.Equal(t, c2paRep.signer.ManifestID(), info.ManifestID)

				emsg, err := c2pavsi.FindEmsg(seg)
				require.NoError(t, err)
				assert.Equal(t, timescale, emsg.Timescale)
				assert.Zero(t, emsg.PresentationTimeDelta)
				assert.NotZero(t, emsg.EventDuration)
				if ids[nr] == nil {
					ids[nr] = map[uint32]string{}
				}
				assert.NotContains(t, ids[nr], emsg.ID, "emsg id of %s segment %d is used by another track", rep, nr)
				ids[nr][emsg.ID] = rep

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
	ts, server := c2paTestServer(t)
	pub := c2paRepOf(t, server, "V300").signer.PublicKey()
	withCue := 0
	for nr := 270; nr <= 300; nr++ { // a minute of 2 s segments
		url := fmt.Sprintf("%s/livesim2/c2pa_vsi/scte35_1/testpic_2s/V300/%d.m4s?nowMS=610000", ts.URL, nr)
		status, seg := c2paGet(t, url)
		require.Equal(t, http.StatusOK, status)
		_, err := c2pavsi.Verify(seg, pub)
		require.NoError(t, err, "segment %d", nr)
		schemes := emsgSchemes(t, seg)
		assert.Contains(t, schemes, c2pavsi.SchemeIDURI, "segment %d", nr)
		if len(schemes) > 1 {
			withCue++
		}
	}
	assert.NotZero(t, withCue, "a segment with an SCTE-35 cue")
}

// TestC2PACredentials checks loading the claim-signing certificate and key from PEM files.
func TestC2PACredentials(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	key, chain, err := c2paTestCredentials()
	require.NoError(t, err)
	dir := t.TempDir()
	var certPEM []byte
	for _, c := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))

	creds, err := newC2PACredentials(logger, certPath, keyPath)
	require.NoError(t, err)
	assert.Equal(t, chain[0].Raw, creds.leaf.Raw)

	_, err = newC2PACredentials(logger, certPath, "")
	assert.ErrorContains(t, err, "must be set together")
}

func TestC2PAVSIRejections(t *testing.T) {
	ts, _ := c2paTestServer(t)
	for _, tc := range []struct {
		desc, url, wantErr string
	}{
		{"unknown method", "c2pa_manifest/testpic_2s/Manifest.mpd", "unknown method"},
		{"chunked", "c2pa_vsi/chunkdur_0.5/testpic_2s/Manifest.mpd", "chunked delivery"},
		{"drm", "c2pa_vsi/drm_EZDRM1/testpic_2s/Manifest.mpd", "drm"},
		{"time subtitles", "c2pa_vsi/timesubsstpp_en/testpic_2s/Manifest.mpd", "time subtitles"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			status, body := c2paGet(t, ts.URL+"/livesim2/"+tc.url+"?nowMS=610000")
			assert.Equal(t, http.StatusBadRequest, status)
			assert.Contains(t, string(body), tc.wantErr)
		})
	}
}

// TestC2PAInitsSigned checks that every clear audio and video representation of the test
// assets gets a C2PA-signed init segment at startup.
func TestC2PAInitsSigned(t *testing.T) {
	_, server := c2paTestServer(t)
	signed := 0
	for path, a := range server.assetMgr.assets {
		for id, rd := range a.Reps {
			if rd.PreEncrypted || (rd.ContentType != "audio" && rd.ContentType != "video") {
				assert.Nil(t, rd.c2pa, "%s/%s", path, id)
				continue
			}
			if assert.NotNil(t, rd.c2pa, "%s/%s", path, id) {
				signed++
			}
		}
	}
	assert.Positive(t, signed)
}

// TestC2PADisabled checks that without the c2pa option no init segment is signed and
// c2pa_vsi requests are rejected.
func TestC2PADisabled(t *testing.T) {
	ts, server := c2paTestServerWith(t, false)
	assert.Nil(t, server.assetMgr.c2pa)
	for path, a := range server.assetMgr.assets {
		for id, rd := range a.Reps {
			assert.Nil(t, rd.c2pa, "%s/%s", path, id)
		}
	}
	for _, url := range []string{"testpic_2s/Manifest.mpd", "testpic_2s/V300/init.mp4", "testpic_2s/V300/300.m4s"} {
		status, body := c2paGet(t, ts.URL+"/livesim2/c2pa_vsi/"+url+"?nowMS=610000")
		assert.Equal(t, http.StatusBadRequest, status, url)
		assert.Contains(t, string(body), "C2PA is not enabled on this server", url)
	}
	status, _ := c2paGet(t, ts.URL+"/livesim2/testpic_2s/V300/300.m4s?nowMS=610000")
	assert.Equal(t, http.StatusOK, status)
}

// TestC2PAConfigNeedsOption checks that the credential options are refused without c2pa.
func TestC2PAConfigNeedsOption(t *testing.T) {
	_, err := LoadConfig([]string{"livesim2", "--c2pacert", "cert.pem", "--c2pakey", "key.pem"}, t.TempDir())
	assert.ErrorContains(t, err, "need the c2pa option")
	cfg, err := LoadConfig([]string{"livesim2", "--c2pa"}, t.TempDir())
	require.NoError(t, err)
	assert.True(t, cfg.C2PA)
}
