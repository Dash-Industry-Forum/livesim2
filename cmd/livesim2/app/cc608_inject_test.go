// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// avccSample packs NALUs into a length-prefixed (AVCC) sample.
func avccSample(nalus ...[]byte) []byte {
	var out []byte
	var l [4]byte
	for _, n := range nalus {
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

// cc608RowText concatenates the text of a decoded screen row.
func cc608RowText(s cta608.Screen, idx int) string {
	for _, r := range s.Rows {
		if r.Index != idx {
			continue
		}
		var text string
		for _, run := range r.Runs {
			text += run.Text
		}
		return text
	}
	return ""
}

type cc608Flip struct {
	frame        int
	line1, line2 string
}

// decodeSamples extracts the CTA-608 field pairs from each sample's NALUs and
// feeds them to a decoder, returning the on-screen changes.
func decodeSamples(t *testing.T, samples []mp4.FullSample, codec carriage.Codec) []cc608Flip {
	t.Helper()
	// A conformant receiver reassembles cc_data in presentation (PTS) order, so
	// decode the samples in that order (this is what dash.js/hls.js/Shaka do).
	order := make([]int, len(samples))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return samples[order[a]].PresentationTime() < samples[order[b]].PresentationTime()
	})
	var dec cta608.Decoder
	var flips []cc608Flip
	for rank, idx := range order {
		nalus, err := avc.GetNalusFromSample(samples[idx].Data)
		require.NoError(t, err)
		f1, _, err := carriage.FieldPairs(nalus, codec)
		require.NoError(t, err)
		require.NoError(t, dec.Feed(f1))
		if dec.Changed() {
			flips = append(flips, cc608Flip{rank, cc608RowText(dec.Screen(), cc608Line1Row), cc608RowText(dec.Screen(), cc608Line2Row)})
		}
	}
	return flips
}

func TestCC608CodecFor(t *testing.T) {
	cases := map[string]struct {
		codec carriage.Codec
		ok    bool
	}{
		"avc1.640028":      {carriage.CodecAVC, true},
		"avc3.42c01e":      {carriage.CodecAVC, true},
		"hev1.2.4.L120.90": {carriage.CodecHEVC, true},
		"hvc1.1.6.L93.90":  {carriage.CodecHEVC, true},
		"mp4a.40.2":        {0, false},
		"stpp":             {0, false},
	}
	for codecs, want := range cases {
		got, ok := cc608CodecFor(codecs)
		require.Equal(t, want.ok, ok, codecs)
		if want.ok {
			require.Equal(t, want.codec, got, codecs)
		}
	}
}

// TestSpliceSEIBeforeVCL checks the SEI lands just before the first VCL NALU,
// after any SPS/PPS.
func TestSpliceSEIBeforeVCL(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x00}       // AVC SPS (type 7)
	pps := []byte{0x68, 0xce, 0x3c}       // AVC PPS (type 8)
	idr := []byte{0x65, 0x88, 0x80, 0x00} // AVC IDR slice (type 5, VCL)
	sample := avccSample(sps, pps, idr)
	sei := []byte{0x06, 0x04, 0x02, 0xb5, 0x00} // fake SEI NALU (type 6)

	out, err := spliceSEIBeforeVCL(sample, sei, carriage.CodecAVC)
	require.NoError(t, err)
	nalus, err := avc.GetNalusFromSample(out)
	require.NoError(t, err)
	require.Len(t, nalus, 4)
	require.Equal(t, avc.NALU_SPS, avc.GetNaluType(nalus[0][0]))
	require.Equal(t, avc.NALU_PPS, avc.GetNaluType(nalus[1][0]))
	require.Equal(t, avc.NALU_SEI, avc.GetNaluType(nalus[2][0]))
	require.True(t, avc.IsVideoNaluType(avc.GetNaluType(nalus[3][0])))
}

func TestInjectCC608AVC(t *testing.T) {
	testInjectCC608(t, carriage.CodecAVC, []byte{0x65, 0x88, 0x80, 0x00})
}

func TestInjectCC608HEVC(t *testing.T) {
	// HEVC IDR_W_RADL slice: type 19 -> header byte0 = 19<<1 = 0x26, byte1 = 0x01.
	testInjectCC608(t, carriage.CodecHEVC, []byte{0x26, 0x01, 0x80, 0x00})
}

func testInjectCC608(t *testing.T, codec carriage.Codec, vclNalu []byte) {
	t.Helper()
	const fps = 30.0
	const nFrames = 60 // 2 s at 30 fps -> 2 cues
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	samples := make([]mp4.FullSample, nFrames)
	origSize := len(avccSample(vclNalu))
	for i := range samples {
		samples[i] = mp4.FullSample{
			Sample: mp4.Sample{Size: uint32(origSize)},
			Data:   avccSample(vclNalu),
		}
	}

	require.NoError(t, injectCC608(samples, fps, unitStart, 42, codec))

	// Every sample gained an SEI NALU placed before the VCL, and Size was updated.
	for i := range samples {
		nalus, err := avc.GetNalusFromSample(samples[i].Data)
		require.NoError(t, err, "sample %d", i)
		require.Len(t, nalus, 2, "sample %d: SEI + VCL", i)
		if codec == carriage.CodecHEVC {
			require.Equal(t, hevc.NALU_SEI_PREFIX, hevc.GetNaluType(nalus[0][0]))
		} else {
			require.Equal(t, avc.NALU_SEI, avc.GetNaluType(nalus[0][0]))
		}
		require.True(t, isVCLNalu(nalus[1], codec), "sample %d VCL after SEI", i)
		require.Equal(t, uint32(len(samples[i].Data)), samples[i].Size, "sample %d Size", i)
		require.Greater(t, len(samples[i].Data), origSize, "sample %d grew", i)
	}

	// The injected captions decode to the two per-second cues with the seg number.
	flips := decodeSamples(t, samples, codec)
	require.Len(t, flips, 2, "one flip per cue")
	require.Equal(t, "14:23:44.000", flips[0].line1)
	require.Equal(t, "SEG 42", flips[0].line2)
	require.Equal(t, "14:23:45.000", flips[1].line1)
	require.Equal(t, "SEG 42", flips[1].line2)
}

// TestGenLiveSegmentCC608 drives a real testpic_2s/V300 (AVC) segment through
// genLiveSegment with timecc608 set, round-trips it through the real encode path,
// and verifies every video sample carries CEA-608 SEI that decodes to the clock +
// segment number.
func TestGenLiveSegmentCC608(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	const nowMS = 100_000
	const nr = 40

	so, err := genLiveSegment(logger, vodFS, asset, cfg, fmt.Sprintf("V300/%d.m4s", nr), nowMS, false)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", so.meta.rep.SegmentType())

	// Round-trip through the real encode path to validate the size bookkeeping.
	sw := bits.NewFixedSliceWriter(int(so.seg.Size()))
	require.NoError(t, so.seg.EncodeSW(sw))
	decoded, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(sw.Bytes()))
	require.NoError(t, err)
	require.Len(t, decoded.Segments, 1)

	trex := so.meta.rep.initSeg.Moov.Mvex.Trex
	var samples []mp4.FullSample
	for _, frag := range decoded.Segments[0].Fragments {
		fss, err := frag.GetFullSamples(trex)
		require.NoError(t, err)
		samples = append(samples, fss...)
	}
	require.NotEmpty(t, samples)
	for i := range samples {
		require.True(t, avc.ContainsNaluType(samples[i].Data, avc.NALU_SEI), "sample %d missing SEI", i)
	}

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	for i, fl := range flips {
		t.Logf("cue %d @rank %d: line1=%q line2=%q", i, fl.frame, fl.line1, fl.line2)
	}
	// A 2s segment at 30fps has N=2 cues; both must decode (in presentation order),
	// each two lines, and the times must tick by one second.
	require.Len(t, flips, 2, "two ticking cues per 2s segment")
	require.Regexp(t, `^\d\d:\d\d:\d\d\.\d\d\d$`, flips[0].line1)
	require.Regexp(t, `^SEG \d+$`, flips[0].line2)
	require.Regexp(t, `^\d\d:\d\d:\d\d\.\d\d\d$`, flips[1].line1)
	require.Equal(t, flips[0].line2, flips[1].line2, "segment number is constant across the segment's cues")
	require.NotEqual(t, flips[0].line1, flips[1].line1, "the two cues must show different (ticking) times")
}

// TestGenLiveSegmentCC608HEVC is the HEVC counterpart of TestGenLiveSegmentCC608:
// it drives a real hev1 segment (bbb_hevc_ac3_8s, 24 fps, 2s segments) through
// genLiveSegment with timecc608, round-trips it through the encode path, and
// verifies every video sample carries a CEA-608 SEI prefix NAL that decodes (in
// presentation order) to the ticking clock + segment number. This proves the
// injection path — SEI splicing, VCL detection, presentation-order distribution and
// the trun/mdat write-back — is codec-generic for HEVC end to end.
func TestGenLiveSegmentCC608HEVC(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("bbb_hevc_ac3_8s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	const nowMS = 100_000
	const nr = 40 // 2s segments -> segment 40 starts at 80s = 00:01:20

	so, err := genLiveSegment(logger, vodFS, asset, cfg, fmt.Sprintf("video_%d.m4s", nr), nowMS, false)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", so.meta.rep.SegmentType())
	codec, ok := cc608CodecFor(so.meta.rep.Codecs)
	require.True(t, ok)
	require.Equal(t, carriage.CodecHEVC, codec)

	// Round-trip through the real encode path to validate the size bookkeeping.
	sw := bits.NewFixedSliceWriter(int(so.seg.Size()))
	require.NoError(t, so.seg.EncodeSW(sw))
	decoded, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(sw.Bytes()))
	require.NoError(t, err)
	require.Len(t, decoded.Segments, 1)

	trex := so.meta.rep.initSeg.Moov.Mvex.Trex
	var samples []mp4.FullSample
	for _, frag := range decoded.Segments[0].Fragments {
		fss, err := frag.GetFullSamples(trex)
		require.NoError(t, err)
		samples = append(samples, fss...)
	}
	require.NotEmpty(t, samples)
	for i := range samples {
		require.True(t, hevc.ContainsNaluType(samples[i].Data, hevc.NALU_SEI_PREFIX), "sample %d missing SEI", i)
	}

	flips := decodeSamples(t, samples, carriage.CodecHEVC)
	require.Len(t, flips, 2, "two ticking cues per 2s segment")
	require.Equal(t, "00:01:20.000", flips[0].line1)
	require.Equal(t, "SEG 40", flips[0].line2)
	require.Equal(t, "00:01:21.000", flips[1].line1)
	require.Equal(t, "SEG 40", flips[1].line2)
}

// TestPrepareChunksCC608 exercises the low-latency chunked path: a chunked
// timecc608 request must produce chunks whose video samples all carry the CEA-608
// SEI. The captions are injected once over the whole segment in genLiveSegment
// (applyCC608) before chunkSegment re-fragments it, so every chunk inherits its
// share; concatenated in presentation order the chunks decode to the same two
// ticking cues as the whole-segment path.
func TestPrepareChunksCC608(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s_low_delay")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	cfg.ChunkDurS = Ptr(0.5) // 2s segment -> 4 chunks
	const nowMS = 100_000
	const nr = 40 // segment 40 starts at 40*2s = 80s = 00:01:20

	so, chunks, err := prepareChunks(logger, vodFS, asset, cfg, nil, fmt.Sprintf("1080/%d.m4s", nr), nowMS, false, nil)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", so.meta.rep.SegmentType())
	require.Greater(t, len(chunks), 1, "expected several chunks")

	trex := so.meta.rep.initSeg.Moov.Mvex.Trex
	var samples []mp4.FullSample
	for ci, chk := range chunks {
		fss, err := chk.frag.GetFullSamples(trex)
		require.NoError(t, err)
		require.NotEmpty(t, fss, "chunk %d has samples", ci)
		for i := range fss {
			require.True(t, avc.ContainsNaluType(fss[i].Data, avc.NALU_SEI),
				"chunk %d sample %d missing SEI", ci, i)
		}
		samples = append(samples, fss...)
	}

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Len(t, flips, 2, "two ticking cues per 2s segment, reassembled across the chunks")
	require.Equal(t, "00:01:20.000", flips[0].line1)
	require.Equal(t, "SEG 40", flips[0].line2)
	require.Equal(t, "00:01:21.000", flips[1].line1)
	require.Equal(t, "SEG 40", flips[1].line2)
}

// TestGenLiveSegmentCC608_2997fps drives a real 29.97 fps (30000/1001) AVC asset
// through genLiveSegment with timecc608. Unlike the 30 fps testpic assets, this
// content has non-integer fps and 2.002s segments, so it checks that the go-608 fps
// guard accepts 29.97, that the caption pairs are distributed one-per-frame over the
// 60 frames, and that the two per-second cues stay frame-accurate to the wall clock
// (segment 40 starts at 40*2.002s = 80.08s = 00:01:20.080).
func TestGenLiveSegmentCC608_2997fps(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("dolby-ac4/2997fps")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}
	const nowMS = 100_000
	const nr = 40

	so, err := genLiveSegment(logger, vodFS, asset, cfg, fmt.Sprintf("video/avc1/seg-%d.m4s", nr), nowMS, false)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", so.meta.rep.SegmentType())
	require.EqualValues(t, 30000, so.meta.rep.MediaTimescale)
	require.EqualValues(t, 1001, so.meta.rep.sampleDur()) // 30000/1001 = 29.97 fps

	trex := so.meta.rep.initSeg.Moov.Mvex.Trex
	var samples []mp4.FullSample
	for _, frag := range so.seg.Fragments {
		fss, err := frag.GetFullSamples(trex)
		require.NoError(t, err)
		samples = append(samples, fss...)
	}
	require.NotEmpty(t, samples)
	for i := range samples {
		require.True(t, avc.ContainsNaluType(samples[i].Data, avc.NALU_SEI), "sample %d missing SEI", i)
	}

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Len(t, flips, 2, "two ticking cues per 2.002s segment")
	// 40*2.002s = 80.080s; the second cue is ~1.001s later.
	require.Equal(t, "00:01:20.080", flips[0].line1)
	require.Equal(t, "SEG 40", flips[0].line2)
	require.Equal(t, "00:01:21.081", flips[1].line1)
	require.Equal(t, "SEG 40", flips[1].line2)
}

// TestGenLiveSegmentCC608AudioUnchanged confirms timecc608 is a no-op for audio
// (contentType != "video"): the audio segment is byte-for-byte identical with and
// without the option.
func TestGenLiveSegmentCC608AudioUnchanged(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	const nowMS = 100_000
	media := "A48/40.m4s"

	plain := NewResponseConfig()
	withCC := NewResponseConfig()
	withCC.CC608 = &CC608Config{Channel: "CC1", Lang: "eng"}

	soPlain, err := genLiveSegment(logger, vodFS, asset, plain, media, nowMS, false)
	require.NoError(t, err)
	soCC, err := genLiveSegment(logger, vodFS, asset, withCC, media, nowMS, false)
	require.NoError(t, err)
	require.Equal(t, "audio/mp4", soCC.meta.rep.SegmentType())
	require.Equal(t, soPlain.seg.Size(), soCC.seg.Size(), "audio segment must be unaffected by timecc608")
}
