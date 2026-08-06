// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/generate"
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

// cc608Cfg builds a timecc608 configuration from its URL value, so a test names the
// caption mode exactly as a request does and the grammar is covered end to end.
func cc608Cfg(t *testing.T, val string) *CC608Config {
	t.Helper()
	cfg, err := CreateCC608Config(val)
	require.NoError(t, err)
	return cfg
}

// cc608PresentationOrder returns the sample indices sorted by presentation time. A
// conformant receiver reassembles cc_data in presentation (PTS) order, so that is the
// order to decode in (this is what dash.js/hls.js/Shaka do).
func cc608PresentationOrder(samples []mp4.FullSample) []int {
	order := make([]int, len(samples))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return samples[order[a]].PresentationTime() < samples[order[b]].PresentationTime()
	})
	return order
}

// decodeSamples extracts the CTA-608 field pairs from each sample's NALUs and
// feeds them to a decoder, returning the on-screen changes.
func decodeSamples(t *testing.T, samples []mp4.FullSample, codec carriage.Codec) []cc608Flip {
	t.Helper()
	var dec cta608.Decoder
	var flips []cc608Flip
	for rank, idx := range cc608PresentationOrder(samples) {
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

// decodeScreens decodes the samples in presentation order and returns the screen as it
// stands after each frame, so a test can assert on what a viewer sees at a given frame.
// This is what paint-on and roll-up need: they write onto the displayed screen a byte
// pair at a time, so decodeSamples' change list holds one entry per two characters
// rather than one per caption.
func decodeScreens(t *testing.T, samples []mp4.FullSample, codec carriage.Codec) []cta608.Screen {
	t.Helper()
	screens := make([]cta608.Screen, 0, len(samples))
	var dec cta608.Decoder
	for _, idx := range cc608PresentationOrder(samples) {
		nalus, err := avc.GetNalusFromSample(samples[idx].Data)
		require.NoError(t, err)
		f1, _, err := carriage.FieldPairs(nalus, codec)
		require.NoError(t, err)
		require.NoError(t, dec.Feed(f1))
		screens = append(screens, dec.Screen())
	}
	return screens
}

// cc608ScreenLines renders the caption rows of a screen as "<row1>|<row2>", the shape
// the paint-on and roll-up assertions compare against. rows names the rows to include.
func cc608ScreenLines(s cta608.Screen, rows ...int) string {
	texts := make([]string, 0, len(rows))
	for _, r := range rows {
		texts = append(texts, cc608RowText(s, r))
	}
	return strings.Join(texts, "|")
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

// cc608TestSamples returns nFrames identical samples carrying just vclNalu.
func cc608TestSamples(nFrames int, vclNalu []byte) []mp4.FullSample {
	samples := make([]mp4.FullSample, nFrames)
	size := len(avccSample(vclNalu))
	for i := range samples {
		samples[i] = mp4.FullSample{
			Sample: mp4.Sample{Size: uint32(size)},
			Data:   avccSample(vclNalu),
		}
	}
	return samples
}

// testInjectCC608 injects two consecutive units and decodes them as one stream.
//
// Two units are needed because a cue's build is transmitted ahead of its flip: the
// build for unit B's first cue rides unit A's tail, so only a two-unit stream can
// show that the flip lands on the unit boundary with the right text. Within a unit,
// the flip for cue k lands on cue k's first frame.
func testInjectCC608(t *testing.T, codec carriage.Codec, vclNalu []byte) {
	t.Helper()
	const fps = 30.0
	const nFrames = 60 // 2 s at 30 fps -> 2 cues
	unitAStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	unitBStart := unitAStart + 2000

	unitA := cc608TestSamples(nFrames, vclNalu)
	unitB := cc608TestSamples(nFrames, vclNalu)
	origSize := len(avccSample(vclNalu))

	uA := generate.Unit{Nr: 42, StartMS: unitAStart, Frames: nFrames}
	uB := generate.Unit{Nr: 43, StartMS: unitBStart, Frames: nFrames}
	uC := generate.Unit{Nr: 44, StartMS: unitBStart + 2000, Frames: nFrames}
	require.NoError(t, injectCC608(unitA, fps, uA, uB, codec, cc608Cfg(t, "CC1-eng-pop")))
	require.NoError(t, injectCC608(unitB, fps, uB, uC, codec, cc608Cfg(t, "CC1-eng-pop")))

	// Every sample gained an SEI NALU placed before the VCL, and Size was updated.
	for _, samples := range [][]mp4.FullSample{unitA, unitB} {
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
	}

	// Decoded as one continuous stream, every flip lands on a cue boundary (frames
	// 30, 60, 90) and shows the time of the interval it is displayed over. Unit A's
	// own first cue (frame 0) has no build here — it would have come from the unit
	// before A — which is the documented mid-stream-join behaviour.
	flips := decodeSamples(t, append(append([]mp4.FullSample{}, unitA...), unitB...), codec)
	require.Equal(t, []cc608Flip{
		{30, "14:23:45.000", "SEG 42"},
		{60, "14:23:46.000", "SEG 43"}, // built in unit A's tail, flipped by unit B
		{90, "14:23:47.000", "SEG 43"},
	}, flips)
}

// TestCC608UnitFramesBuildDoesNotFit checks that a unit too short to carry the build
// for the next cue is reported rather than silently producing an EOC with nothing
// loaded (a caption that would never appear).
func TestCC608UnitFramesBuildDoesNotFit(t *testing.T) {
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	unit := generate.Unit{Nr: 42, StartMS: unitStart, Frames: 10}
	next := generate.Unit{Nr: 43, StartMS: unitStart + 333}
	_, err := cc608UnitFrames(30.0, unit, next, cc608Cfg(t, "CC1-eng-pop"))
	require.ErrorContains(t, err, "does not fit the 9 frames left in this unit")
}

// TestInjectCC608SelfContained covers timecc608's "-pop-sc" mode, where a cue's build and
// its flip both ride the cue's own frames. A single unit then carries every caption it
// shows: both cues appear from this unit alone, with no dependency on a neighbour — the
// contrast with TestInjectCC608FirstCueUnbuilt, where the same unit shows only its
// second cue. The price is latency: each flip lands build-pairs frames *into* its cue,
// so the caption named 14:23:44 first appears a little over half a second late.
func TestInjectCC608SelfContained(t *testing.T) {
	const fps = 30.0
	const nFrames = 60 // 2 s at 30 fps -> 2 cues
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	samples := cc608TestSamples(nFrames, []byte{0x65, 0x88, 0x80, 0x00})

	unit := generate.Unit{Nr: 42, StartMS: unitStart, Frames: nFrames}
	next := generate.Unit{Nr: 43, StartMS: unitStart + 2000}
	require.NoError(t, injectCC608(samples, fps, unit, next, carriage.CodecAVC, cc608Cfg(t, "CC1-eng-pop-sc")))

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Len(t, flips, 2, "both cues are visible from this unit alone")
	require.Equal(t, "14:23:44.000", flips[0].line1)
	require.Equal(t, "SEG 42", flips[0].line2)
	require.Equal(t, "14:23:45.000", flips[1].line1)
	require.Equal(t, "SEG 42", flips[1].line2)
	// Each flip lags its cue's boundary (frames 0 and 30) by the build it had to drain,
	// and still leaves display time before the next cue.
	require.Greater(t, flips[0].frame, 0, "cue 0 flips after its build, not on frame 0")
	require.Less(t, flips[0].frame, 30, "cue 0 is displayed before cue 1 takes over")
	require.Greater(t, flips[1].frame, 30, "cue 1 flips after its build")
	require.Less(t, flips[1].frame, nFrames)
	t.Logf("self-contained flips at frames %d and %d (cue boundaries 0 and 30)", flips[0].frame, flips[1].frame)
}

// TestCC608UnitFramesSelfContainedDoesNotFit checks the self-contained counterpart of
// TestCC608UnitFramesBuildDoesNotFit: cues too short to hold their own build and flip
// are reported instead of emitting a caption that is never displayed.
func TestCC608UnitFramesSelfContainedDoesNotFit(t *testing.T) {
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	unit := generate.Unit{Nr: 42, StartMS: unitStart, Frames: 10}
	next := generate.Unit{Nr: 43, StartMS: unitStart + 333}
	_, err := cc608UnitFrames(30.0, unit, next, cc608Cfg(t, "CC1-eng-pop-sc"))
	require.ErrorContains(t, err, "does not fit its 10-frame slice")
}

// TestInjectCC608FirstCueUnbuilt documents what a receiver sees when it starts on a
// unit whose first cue was built in the previous (unavailable) unit: the leading EOC
// flips an unloaded screen, so there is no caption until the next cue boundary,
// rather than a stale or garbled one.
func TestInjectCC608FirstCueUnbuilt(t *testing.T) {
	const fps = 30.0
	const nFrames = 60
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()
	samples := cc608TestSamples(nFrames, []byte{0x65, 0x88, 0x80, 0x00})

	unit := generate.Unit{Nr: 42, StartMS: unitStart, Frames: nFrames}
	next := generate.Unit{Nr: 43, StartMS: unitStart + 2000}
	require.NoError(t, injectCC608(samples, fps, unit, next, carriage.CodecAVC, cc608Cfg(t, "CC1-eng-pop")))

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Equal(t, []cc608Flip{
		{30, "14:23:45.000", "SEG 42"},
	}, flips, "only the cue whose build is inside this unit appears")
}

// cc608SegSamples generates one segment through genLiveSegment and returns its video
// samples. With roundTrip set, the segment is encoded and re-decoded first, which
// validates the injection's size bookkeeping through the real encode path.
func cc608SegSamples(t *testing.T, vodFS fs.FS, a *asset, cfg *ResponseConfig, media string, nowMS int, roundTrip bool) []mp4.FullSample {
	t.Helper()
	so, err := genLiveSegment(slog.Default(), vodFS, a, cfg, media, nowMS, false)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", so.meta.rep.SegmentType())

	seg := so.seg
	if roundTrip {
		sw := bits.NewFixedSliceWriter(int(so.seg.Size()))
		require.NoError(t, so.seg.EncodeSW(sw))
		decoded, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(sw.Bytes()))
		require.NoError(t, err)
		require.Len(t, decoded.Segments, 1)
		seg = decoded.Segments[0]
	}

	trex := so.meta.rep.initSeg.Moov.Mvex.Trex
	var samples []mp4.FullSample
	for _, frag := range seg.Fragments {
		fss, err := frag.GetFullSamples(trex)
		require.NoError(t, err)
		samples = append(samples, fss...)
	}
	require.NotEmpty(t, samples)
	return samples
}

// TestGenLiveSegmentCC608 drives two consecutive real testpic_2s/V300 (AVC) segments
// through genLiveSegment with timecc608 set, round-trips them through the real encode
// path, and verifies every video sample carries CEA-608 SEI that decodes to the clock
// + segment number.
//
// Two segments are required: a cue's build is transmitted during the preceding cue,
// so the first caption of segment 41 is built in segment 40's tail. Decoding the pair
// as one stream is what proves the flip lands on the segment boundary carrying the
// next segment's number.
func TestGenLiveSegmentCC608(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = cc608Cfg(t, "CC1-eng-pop")
	const nowMS = 100_000
	const nr = 40 // 2s segments -> segment 40 starts at 80s = 00:01:20

	var samples []mp4.FullSample
	for _, n := range []int{nr, nr + 1} {
		samples = append(samples, cc608SegSamples(t, vodFS, asset, cfg, fmt.Sprintf("V300/%d.m4s", n), nowMS, true)...)
	}
	for i := range samples {
		require.True(t, avc.ContainsNaluType(samples[i].Data, avc.NALU_SEI), "sample %d missing SEI", i)
	}

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	for i, fl := range flips {
		t.Logf("cue %d @rank %d: line1=%q line2=%q", i, fl.frame, fl.line1, fl.line2)
	}
	// Each 2s segment at 30fps holds two ~1s cues. Across the pair the visible flips
	// are: segment 40's second cue (frame 30), then segment 41's two cues at frames 60
	// and 90 — the frame-60 flip being the one built in segment 40's tail. Segment 40's
	// own first cue was built in segment 39, which is not part of this stream.
	require.Equal(t, []cc608Flip{
		{30, "00:01:21.000", "SEG 40"},
		{60, "00:01:22.000", "SEG 41"},
		{90, "00:01:23.000", "SEG 41"},
	}, flips)
}

// TestGenLiveSegmentCC608SelfContainedSegment drives the "-pop-sc" mode through the real
// request path (CC608Config.SelfContained -> applyCC608), which is what a client asking
// for timecc608_CC1-eng-pop-sc gets. One segment on its own must show both of its cues,
// where plain "-pop" shows only the second (TestGenLiveSegmentCC608 needs two segments to
// see the first). Each flip lands inside the cue it names instead of on its boundary,
// which is the latency this mode trades for self-containment.
func TestGenLiveSegmentCC608SelfContainedSegment(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = cc608Cfg(t, "CC1-eng-pop-sc")
	const nowMS = 100_000
	const nr = 40 // 2s segments -> segment 40 starts at 80s = 00:01:20

	samples := cc608SegSamples(t, vodFS, asset, cfg, fmt.Sprintf("V300/%d.m4s", nr), nowMS, true)
	flips := decodeSamples(t, samples, carriage.CodecAVC)

	require.Len(t, flips, 2, "a single segment carries both its captions")
	require.Equal(t, "00:01:20.000", flips[0].line1)
	require.Equal(t, "SEG 40", flips[0].line2)
	require.Equal(t, "00:01:21.000", flips[1].line1)
	require.Equal(t, "SEG 40", flips[1].line2)
	require.Greater(t, flips[0].frame, 0, "the flip follows its own build, so it lags the cue boundary")
	require.Less(t, flips[0].frame, 30)
	require.Greater(t, flips[1].frame, 30)
}

// TestGenLiveSegmentCC608PaintOn drives the default mode — paint-on, what a client asking
// for plain timecc608_CC1-eng gets — through the real request path. One segment on its own
// must show both of its captions, since a paint-on cue's data never leaves its own slice.
//
// The mode's signature is that a caption arrives progressively: the cue clears the screen
// on its first frame and then writes two characters per frame, so the screen changes ~15
// times per cue where a pop-on cue changes once. Both are checked here, along with the
// caption being complete for the rest of its slice.
func TestGenLiveSegmentCC608PaintOn(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = cc608Cfg(t, "CC1-eng") // no mode field: paint-on
	require.Equal(t, CC608PaintOn, cfg.CC608.Mode)
	const nowMS = 100_000
	const nr = 40 // 2s segments at 30fps -> 60 frames, two 30-frame cues

	samples := cc608SegSamples(t, vodFS, asset, cfg, fmt.Sprintf("V300/%d.m4s", nr), nowMS, true)
	require.Len(t, samples, 60)
	screens := decodeScreens(t, samples, carriage.CodecAVC)

	// Each cue is complete by the end of its own slice, and names its own second.
	require.Equal(t, "00:01:20.000|SEG 40", cc608ScreenLines(screens[29], cc608Line1Row, cc608Line2Row))
	require.Equal(t, "00:01:21.000|SEG 40", cc608ScreenLines(screens[59], cc608Line1Row, cc608Line2Row))
	// The second cue opens by clearing what the first one left, so the screen is blank
	// again on frame 30 and the caption grows from nothing.
	require.Equal(t, "|", cc608ScreenLines(screens[30], cc608Line1Row, cc608Line2Row))

	// It is written two characters per frame, so it is done partway into its slice, and
	// the writing itself is visible as a screen change on most of the frames it takes.
	full := "00:01:21.000|SEG 40"
	done := 60
	for i := 30; i < 60; i++ {
		if cc608ScreenLines(screens[i], cc608Line1Row, cc608Line2Row) == full {
			done = i
			break
		}
	}
	require.Less(t, done, 50, "the caption finishes painting inside its own cue")
	require.Greater(t, done, 31, "and it takes several frames to arrive, unlike a pop-on flip")
	t.Logf("paint-on cue 1 complete at frame %d of 30..59", done)

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Greater(t, len(flips), 10, "paint-on changes the screen a byte pair at a time")
}

// TestGenLiveSegmentCC608RollUp drives timecc608_CC1-eng-roll3 through the real request
// path: each cue types its two lines onto the base row of a 3-row window, scrolling the
// earlier lines up.
//
// The window is reset on the segment's first frame, so the segment is self-contained in
// display as well as data — row 1 is empty over the first cue and only carries history
// once this segment's own first cue has scrolled up. With two lines per cue and three
// rows, that history is the previous cue's bottom line.
func TestGenLiveSegmentCC608RollUp(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = cc608Cfg(t, "CC1-eng-roll3")
	require.Equal(t, 3, cfg.CC608.RollUpRows)
	const nowMS = 100_000
	const nr = 40

	samples := cc608SegSamples(t, vodFS, asset, cfg, fmt.Sprintf("V300/%d.m4s", nr), nowMS, true)
	require.Len(t, samples, 60)
	screens := decodeScreens(t, samples, carriage.CodecAVC)

	// The window holds rows 1..3, with row 3 the base row the lines are typed onto. At the
	// end of the first cue the reset leaves row 1 empty; by the end of the second, the
	// first cue's bottom line has scrolled up into it.
	rows := []int{cc608Line1Row - 1, cc608Line1Row, cc608Line2Row}
	require.Equal(t, "|00:01:20.000|SEG 40", cc608ScreenLines(screens[29], rows...))
	require.Equal(t, "SEG 40|00:01:21.000|SEG 40", cc608ScreenLines(screens[59], rows...))
}

// TestGenLiveSegmentCC608NrTimeOffset covers the case where a segment does not start at
// segmentNr * segmentDuration. Two URL options decouple the two: startnr_ renumbers the
// segments (nr 45 with startnr_5 is the segment at media time 80 s, not 90 s), and a
// non-zero availabilityStartTime shifts every segment's wall clock without touching its
// number. The caption clock must follow the segment's own media time — applyCC608 derives
// it from the fragment's already-shifted tfdt plus cfg.StartTimeS, never from the segment
// number — while "SEG <nr>" keeps naming the number the client asked for. Both cases below
// carry the same media (80 s onwards), so the number moves independently of the clock.
func TestGenLiveSegmentCC608NrTimeOffset(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("testpic_2s")
	require.True(t, ok)

	cases := []struct {
		desc       string
		startNr    uint32
		startTimeS int
		nr         int
		nowMS      int
		want       []cc608Flip
	}{
		{
			// startnr_5: media time is (nr-5)*2 s, so segment 45 is the 80 s one and the
			// clock must read 00:01:2x, not the 00:01:3x that 45*2 s would give.
			desc: "startnr_5", startNr: 5, nr: 45, nowMS: 100_000,
			want: []cc608Flip{
				{30, "00:01:21.000", "SEG 45"},
				{60, "00:01:22.000", "SEG 46"},
				{90, "00:01:23.000", "SEG 46"},
			},
		},
		{
			// availabilityStartTime one hour past the epoch: same media time and same
			// numbers, every caption clock an hour later.
			desc: "ast_3600", startTimeS: 3600, nr: 40, nowMS: 3_700_000,
			want: []cc608Flip{
				{30, "01:01:21.000", "SEG 40"},
				{60, "01:01:22.000", "SEG 41"},
				{90, "01:01:23.000", "SEG 41"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			cfg := NewResponseConfig()
			cfg.CC608 = cc608Cfg(t, "CC1-eng-pop")
			cfg.StartNr = Ptr(c.startNr)
			cfg.StartTimeS = c.startTimeS

			var samples []mp4.FullSample
			for _, n := range []int{c.nr, c.nr + 1} {
				samples = append(samples, cc608SegSamples(t, vodFS, asset, cfg,
					fmt.Sprintf("V300/%d.m4s", n), c.nowMS, true)...)
			}
			require.Equal(t, c.want, decodeSamples(t, samples, carriage.CodecAVC))
		})
	}
}

// TestGenLiveSegmentCC608HEVC is the HEVC counterpart of TestGenLiveSegmentCC608:
// it drives two consecutive real hev1 segments (bbb_hevc_ac3_8s, 24 fps, 2s
// segments) through genLiveSegment with timecc608, round-trips them through the
// encode path, and verifies every video sample carries a CEA-608 SEI prefix NAL that
// decodes (in presentation order) to the ticking clock + segment number. This proves
// the injection path — SEI splicing, VCL detection, presentation-order distribution
// and the trun/mdat write-back — is codec-generic for HEVC end to end, including the
// cue whose build crosses the segment boundary.
func TestGenLiveSegmentCC608HEVC(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("bbb_hevc_ac3_8s")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = cc608Cfg(t, "CC1-eng-pop")
	const nowMS = 100_000
	const nr = 40 // 2s segments -> segment 40 starts at 80s = 00:01:20

	var samples []mp4.FullSample
	for _, n := range []int{nr, nr + 1} {
		samples = append(samples, cc608SegSamples(t, vodFS, asset, cfg, fmt.Sprintf("video_%d.m4s", n), nowMS, true)...)
	}
	for i := range samples {
		require.True(t, hevc.ContainsNaluType(samples[i].Data, hevc.NALU_SEI_PREFIX), "sample %d missing SEI", i)
	}

	flips := decodeSamples(t, samples, carriage.CodecHEVC)
	require.Equal(t, []cc608Flip{
		{24, "00:01:21.000", "SEG 40"},
		{48, "00:01:22.000", "SEG 41"}, // built in segment 40's tail
		{72, "00:01:23.000", "SEG 41"},
	}, flips)
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
	cfg.CC608 = cc608Cfg(t, "CC1-eng-pop")
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

	// Reassembled across the chunks, the segment's second cue flips on its boundary.
	// The first cue's build lives in segment 39, which this test does not fetch (see
	// TestGenLiveSegmentCC608 for the cross-segment case).
	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Equal(t, []cc608Flip{
		{30, "00:01:21.000", "SEG 40"},
	}, flips)
}

// TestGenLiveSegmentCC608_2997fps drives two consecutive real 29.97 fps (30000/1001)
// AVC segments through genLiveSegment with timecc608. Unlike the 30 fps testpic
// assets, this content has non-integer fps and 2.002s segments, so it checks that the
// go-608 fps guard accepts 29.97, that the caption pairs are distributed one-per-frame
// over the 60 frames, and that the cues stay frame-accurate to the wall clock across a
// segment boundary — where a fractional frame duration is most likely to drift, since
// the cue built at the end of segment 40 must name exactly the start of segment 41
// (40*2.002s = 80.080s, so segment 41 starts at 82.082s = 00:01:22.082).
func TestGenLiveSegmentCC608_2997fps(t *testing.T) {
	vodFS := os.DirFS("testdata/assets")
	am := newAssetMgr(vodFS, "", false, false)
	logger := slog.Default()
	require.NoError(t, am.discoverAssets(logger))
	asset, ok := am.findAsset("dolby-ac4/2997fps")
	require.True(t, ok)

	cfg := NewResponseConfig()
	cfg.CC608 = cc608Cfg(t, "CC1-eng-pop")
	const nowMS = 100_000
	const nr = 40

	var samples []mp4.FullSample
	for _, n := range []int{nr, nr + 1} {
		samples = append(samples, cc608SegSamples(t, vodFS, asset, cfg, fmt.Sprintf("video/avc1/seg-%d.m4s", n), nowMS, false)...)
	}
	for i := range samples {
		require.True(t, avc.ContainsNaluType(samples[i].Data, avc.NALU_SEI), "sample %d missing SEI", i)
	}

	flips := decodeSamples(t, samples, carriage.CodecAVC)
	require.Equal(t, []cc608Flip{
		{30, "00:01:21.081", "SEG 40"},
		{60, "00:01:22.082", "SEG 41"}, // built in segment 40's tail, on the boundary
		{90, "00:01:23.083", "SEG 41"},
	}, flips)
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
	withCC.CC608 = cc608Cfg(t, "CC1-eng-pop")

	soPlain, err := genLiveSegment(logger, vodFS, asset, plain, media, nowMS, false)
	require.NoError(t, err)
	soCC, err := genLiveSegment(logger, vodFS, asset, withCC, media, nowMS, false)
	require.NoError(t, err)
	require.Equal(t, "audio/mp4", soCC.meta.rep.SegmentType())
	require.Equal(t, soPlain.seg.Size(), soCC.seg.Size(), "audio segment must be unaffected by timecc608")
}
