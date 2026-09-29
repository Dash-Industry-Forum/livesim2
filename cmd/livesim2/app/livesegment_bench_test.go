// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"io/fs"
	"log/slog"
	"os"
	"testing"
)

var benchLog = slog.New(slog.DiscardHandler)

func benchAsset(b *testing.B, name string) (fs.FS, *asset) {
	b.Helper()
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	if err := am.discoverAssets(benchLog); err != nil {
		b.Fatal(err)
	}
	a, ok := am.findAsset(name)
	if !ok {
		b.Fatalf("asset %s not found", name)
	}
	return vodFS, a
}

// BenchmarkPrepareChunks measures splitting an 8s video segment into 1s chunks,
// with and without encryption of the chunks.
func BenchmarkPrepareChunks(b *testing.B) {
	vodFS, a := benchAsset(b, "testpic_8s")
	for _, drm := range []string{"", "eccp-cenc"} {
		name := drm
		if name == "" {
			name = "clear"
		}
		b.Run(name, func(b *testing.B) {
			cfg := NewResponseConfig()
			cfg.ChunkDurS = Ptr(1.0)
			cfg.DRM = drm
			var buf bytes.Buffer
			b.ReportAllocs()
			for b.Loop() {
				_, chunks, err := prepareChunks(benchLog, vodFS, a, cfg, nil, "V300/10.m4s", 100_000, false, nil)
				if err != nil {
					b.Fatal(err)
				}
				buf.Reset()
				for _, chk := range chunks {
					if err := chk.frag.Encode(&buf); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// BenchmarkGenLiveAudioSegment measures an audio segment re-segmented to match the video
// segment boundaries, which gathers samples from two VoD segments.
func BenchmarkGenLiveAudioSegment(b *testing.B) {
	vodFS, a := benchAsset(b, "testpic_8s")
	cfg := NewResponseConfig()
	var buf bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		so, err := genLiveSegment(benchLog, vodFS, a, cfg, "A48/10.m4s", 100_000, false)
		if err != nil {
			b.Fatal(err)
		}
		buf.Reset()
		if err := so.seg.Encode(&buf); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGenLiveSegmentCC608 measures a 2s video segment with injected CTA-608 captions.
func BenchmarkGenLiveSegmentCC608(b *testing.B) {
	vodFS, a := benchAsset(b, "testpic_2s")
	cfg := NewResponseConfig()
	cc, err := CreateCC608Config("CC1-eng-pop")
	if err != nil {
		b.Fatal(err)
	}
	cfg.CC608 = cc
	var buf bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		so, err := genLiveSegment(benchLog, vodFS, a, cfg, "V300/40.m4s", 100_000, false)
		if err != nil {
			b.Fatal(err)
		}
		buf.Reset()
		if err := so.seg.Encode(&buf); err != nil {
			b.Fatal(err)
		}
	}
}
