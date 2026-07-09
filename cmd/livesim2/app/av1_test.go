// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// genAV1Segment generates one live AV1 media segment (segment number nr) from the
// testpic_2s_av1 asset. The returned RepData carries the encryption data built at load time.
func genAV1Segment(t *testing.T, vodFS fs.FS, am *assetMgr, nr int) (segOut, *RepData) {
	t.Helper()
	asset, ok := am.findAsset("testpic_2s_av1")
	require.True(t, ok)
	rep, ok := asset.Reps["av1"]
	require.True(t, ok)
	require.NotNil(t, rep.encData, "av01 rep must have encryption data prepared")

	cfg := NewResponseConfig()
	media := strings.ReplaceAll("av1/$NrOrTime$.m4s", "$NrOrTime$", strconv.Itoa(nr))
	so, err := genLiveSegment(slog.Default(), vodFS, asset, cfg, media, 100_000, false)
	require.NoError(t, err)
	require.NotNil(t, so.seg)
	require.NotEmpty(t, so.seg.Fragments)
	return so, so.meta.rep
}

// TestAV1SegmentEncryption verifies that on-the-fly cenc and cbcs encryption of an AV1
// segment produces a senc box with per-sample subsample patterns that actually protect
// tile data, and that a clear segment has no senc box.
func TestAV1SegmentEncryption(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	require.NoError(t, am.discoverAssets(slog.Default()))

	// A clear segment must not have a senc box.
	so, _ := genAV1Segment(t, vodFS, am, 40)
	require.Nil(t, so.seg.Fragments[0].Moof.Traf.Senc, "clear segment should have no senc")

	for _, scheme := range []string{"cbcs", "cenc"} {
		t.Run(scheme, func(t *testing.T) {
			so, rep := genAV1Segment(t, vodFS, am, 40)
			cfg := NewResponseConfig()
			cfg.DRM = "eccp-" + scheme
			frags := so.seg.Fragments
			err := encryptFrags(slog.Default(), cfg, nil, rep, frags)
			require.NoError(t, err)

			senc := frags[0].Moof.Traf.Senc
			require.NotNil(t, senc, "encrypted segment must have a senc box")
			require.Equal(t, uint32(50), senc.SampleCount, "one entry per AV1 sample")
			require.Len(t, senc.SubSamples, 50, "AV1 uses subsample encryption")

			var totalProtected, totalClear uint64
			for _, ss := range senc.SubSamples {
				require.NotEmpty(t, ss, "each sample must have at least one subsample pattern")
				for _, p := range ss {
					totalProtected += uint64(p.BytesOfProtectedData)
					totalClear += uint64(p.BytesOfClearData)
				}
			}
			require.Greater(t, totalProtected, uint64(0), "some tile data must be protected")
			require.Greater(t, totalClear, uint64(0), "obu/tile headers must be left clear")

			// The encrypted fragment must still serialize cleanly.
			require.Positive(t, int(frags[0].Size()))
		})
	}
}

// TestAV1EncryptionConcurrent stresses the per-segment AV1 protection-range function.
// mp4ff's AV1 ProtFunc captures a mutable frame-header decoder, so livesim2 builds a fresh
// one per segment. Running many concurrent encryptions of the same rep must be race-free
// (run with -race) and produce identical subsample layouts every time.
func TestAV1EncryptionConcurrent(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	require.NoError(t, am.discoverAssets(slog.Default()))

	// Reference layout from a single-threaded run.
	refSo, refRep := genAV1Segment(t, vodFS, am, 40)
	cfg := NewResponseConfig()
	cfg.DRM = "eccp-cenc"
	require.NoError(t, encryptFrags(slog.Default(), cfg, nil, refRep, refSo.seg.Fragments))
	want := subsampleSignature(refSo.seg.Fragments[0].Moof.Traf.Senc)
	require.NotEqual(t, "", want)

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	sigs := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			so, rep := genAV1Segment(t, vodFS, am, 40)
			cfg := NewResponseConfig()
			cfg.DRM = "eccp-cenc"
			if err := encryptFrags(slog.Default(), cfg, nil, rep, so.seg.Fragments); err != nil {
				errs[i] = err
				return
			}
			sigs[i] = subsampleSignature(so.seg.Fragments[0].Moof.Traf.Senc)
		}(i)
	}
	wg.Wait()
	for i := range n {
		require.NoError(t, errs[i])
		require.Equal(t, want, sigs[i], "concurrent encryption must match single-threaded layout (goroutine %d)", i)
	}
}

// subsampleSignature returns a stable string describing the per-sample clear/protected
// byte layout of a senc box, used to compare encryption results.
func subsampleSignature(senc *mp4.SencBox) string {
	if senc == nil {
		return "<nil>"
	}
	var b strings.Builder
	for _, ss := range senc.SubSamples {
		for _, p := range ss {
			b.WriteString(strconv.Itoa(int(p.BytesOfClearData)))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(int(p.BytesOfProtectedData)))
			b.WriteByte(',')
		}
		b.WriteByte('|')
	}
	return b.String()
}
