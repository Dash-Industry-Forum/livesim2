// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"os"
	"testing"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const slateTestInit = "testdata/assets/testpic_2s/V300/init.mp4"

func slateTestGen(t *testing.T) *slateGen {
	t.Helper()
	initData, err := os.ReadFile(slateTestInit)
	require.NoError(t, err)
	g, err := newSlateGen(initData)
	require.NoError(t, err)
	return g
}

func TestNewSlateGen(t *testing.T) {
	g := slateTestGen(t)
	assert.Equal(t, 640, g.params.Width)
	assert.Equal(t, 360, g.params.Height)
	assert.True(t, g.params.CABAC, "testpic V300 is CABAC")
	require.NotNil(t, g.sps)
	assert.Contains(t, []uint{0, 2}, g.sps.PicOrderCntType)

	_, err := newSlateGen([]byte("not an mp4"))
	assert.Error(t, err)
}

// naluType returns the type of the first NALU in a length-prefixed sample.
func naluType(data []byte) byte {
	return data[4] & 0x1f
}

func TestSlateGenSamples(t *testing.T) {
	g := slateTestGen(t)
	// 60 frames of 3000 ticks (2s @ 90000); the countdown label changes at frame 30.
	specs := make([]slateFrameSpec, 60)
	for i := range specs {
		label := "AD BREAK\n17"
		if i >= 30 {
			label = "AD BREAK\n16"
		}
		specs[i] = slateFrameSpec{dur: 3000, label: label}
	}
	samples, err := g.samples(specs, 5_400_000, 6000)
	require.NoError(t, err)
	require.Len(t, samples, 60)

	for i, s := range samples {
		isIDR := i == 0 || i == 30
		if isIDR {
			assert.Equal(t, mp4.SyncSampleFlags, s.Flags, "sample %d sync", i)
			assert.Equal(t, byte(5), naluType(s.Data), "sample %d IDR NALU", i)
		} else {
			assert.Equal(t, mp4.NonSyncSampleFlags, s.Flags, "sample %d non-sync", i)
			assert.Equal(t, byte(1), naluType(s.Data), "sample %d non-IDR NALU", i)
		}
		assert.Equal(t, uint32(3000), s.Dur)
		assert.Equal(t, int32(6000), s.CompositionTimeOffset)
		assert.Equal(t, uint64(5_400_000+i*3000), s.DecodeTime)
	}
	// The IDRs carry an image (a flat slate compresses very well), the P_Skips are tiny.
	assert.Greater(t, len(samples[0].Data), 300, "IDR with rendered text")
	assert.Less(t, len(samples[1].Data), 100, "P_Skip is tiny")
}

func TestSlateGenSamplesPadding(t *testing.T) {
	g := slateTestGen(t)
	// Target sizes mirror the replaced samples: padded with filler NALUs to match the
	// original bitrate. A target of 0 (or smaller than the frame) keeps the raw size.
	specs := []slateFrameSpec{
		{dur: 3000, size: 4000, label: "AD BREAK\n9"},
		{dur: 3000, size: 1200, label: "AD BREAK\n9"},
		{dur: 3000, size: 0, label: "AD BREAK\n9"},
		{dur: 3000, size: 10, label: "AD BREAK\n9"}, // smaller than a P_Skip frame
	}
	samples, err := g.samples(specs, 0, 0)
	require.NoError(t, err)
	require.Len(t, samples, 4)
	assert.Equal(t, 4000, len(samples[0].Data), "IDR padded to the replaced sample size")
	assert.Equal(t, 1200, len(samples[1].Data), "P_Skip padded to the replaced sample size")
	assert.Less(t, len(samples[2].Data), 100, "no target -> raw P_Skip size")
	assert.Less(t, len(samples[3].Data), 100, "tiny target -> raw size kept")
	assert.Equal(t, byte(5), naluType(samples[0].Data), "padding keeps the IDR first")
}

// slateTestSegment loads and decodes a real testpic segment for substitution tests.
func slateTestSegment(t *testing.T) *mp4.MediaSegment {
	t.Helper()
	data, err := os.ReadFile("testdata/assets/testpic_2s/V300/1.m4s")
	require.NoError(t, err)
	f, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(data))
	require.NoError(t, err)
	require.Len(t, f.Segments, 1)
	return f.Segments[0]
}

func TestApplySGAISlate(t *testing.T) {
	vodFS := os.DirFS("testdata")
	a := &asset{AssetPath: "assets/testpic_2s"}
	rep := &RepData{ID: "V300", InitURI: "V300/init.mp4"}
	cfg := NewResponseConfig()
	cfg.SGAI = &SGAIConfig{
		AdBreaks:       AdBreaks{Periodic: &AdBreakPeriodic{PeriodS: 60, DurationS: 20}},
		ResolveOffsetS: 60,
	}
	seg := slateTestSegment(t)
	origNrSamples := int(seg.Fragments[0].Moof.Traf.Trun.SampleCount())

	// Segment at media time 0 = epoch with AST = epoch: inside the break [0, 20s).
	meta := segMeta{rep: rep, newTime: 0, newNr: 42, timescale: 90000}
	slate, err := applyAdBreakSlate(vodFS, a, cfg, meta, seg)
	require.NoError(t, err)
	require.NotNil(t, slate, "segment inside the break window is slated")

	require.Len(t, slate.Fragments, 1)
	frag := slate.Fragments[0]
	assert.Equal(t, uint32(42), frag.Moof.Mfhd.SequenceNumber)
	assert.Equal(t, uint64(0), frag.Moof.Traf.Tfdt.BaseMediaDecodeTime())
	assert.Equal(t, origNrSamples, int(frag.Moof.Traf.Trun.SampleCount()),
		"slate mirrors the original sample count")
	assert.Nil(t, slate.Sidx, "sidx dropped for slate segments")
	first := frag.Moof.Traf.Trun.Samples[0]
	assert.Equal(t, mp4.SyncSampleFlags, first.Flags, "slate starts with an IDR")

	// Serializes to a valid CMAF segment.
	totSize := slate.Size()
	assert.Greater(t, totSize, uint64(1000))

	// Bitrate match: every slate sample is padded to its replaced sample's size
	// (modulo frames the filler NALU cannot bridge), so the totals are very close.
	var origBytes, slateBytes int
	for _, s := range seg.Fragments[0].Moof.Traf.Trun.Samples {
		origBytes += int(s.Size)
	}
	for _, s := range frag.Moof.Traf.Trun.Samples {
		slateBytes += int(s.Size)
	}
	assert.InDelta(t, origBytes, slateBytes, 0.02*float64(origBytes),
		"slate segment size matches the replaced segment size")

	// A segment at 30s is outside every break occurrence -> no substitution.
	meta = segMeta{rep: rep, newTime: 30 * 90000, newNr: 57, timescale: 90000}
	slate, err = applyAdBreakSlate(vodFS, a, cfg, meta, seg)
	require.NoError(t, err)
	assert.Nil(t, slate)

	// A rep that cannot be slated (bad init) is skipped without error.
	badRep := &RepData{ID: "missing", InitURI: "missing/init.mp4"}
	meta = segMeta{rep: badRep, newTime: 0, newNr: 1, timescale: 90000}
	slate, err = applyAdBreakSlate(vodFS, a, cfg, meta, seg)
	require.NoError(t, err)
	assert.Nil(t, slate)
}

// TestApplySGAISlateFallsBackToRepSampleDuration covers low-delay packagings whose segments
// carry no per-sample duration in trun/tfhd (it lives in the init trex). The slate must then
// fall back to RepData.sampleDur() instead of failing.
func TestApplySGAISlateFallsBackToRepSampleDuration(t *testing.T) {
	vodFS := os.DirFS("testdata")
	a := &asset{AssetPath: "assets/testpic_2s"}
	cfg := NewResponseConfig()
	cfg.SGAI = &SGAIConfig{AdBreaks: AdBreaks{Periodic: &AdBreakPeriodic{PeriodS: 60, DurationS: 20}}, ResolveOffsetS: 60}
	meta := segMeta{newTime: 0, newNr: 7, timescale: 90000}

	// Strip the per-sample (trun) and default (tfhd) durations to mimic such a segment.
	stripped := func() *mp4.MediaSegment {
		seg := slateTestSegment(t)
		traf := seg.Fragments[0].Moof.Traf
		traf.Tfhd.Flags &^= mp4.TfhdDefaultSampleDurationPresentFlag
		for i := range traf.Trun.Samples {
			traf.Trun.Samples[i].Dur = 0
		}
		return seg
	}

	// With RepData.DefaultSampleDuration set (as read from trex at load time), the slate is
	// produced and every sample uses that duration.
	meta.rep = &RepData{ID: "V300", InitURI: "V300/init.mp4", DefaultSampleDuration: 3000}
	slate, err := applyAdBreakSlate(vodFS, a, cfg, meta, stripped())
	require.NoError(t, err)
	require.NotNil(t, slate)
	for _, s := range slate.Fragments[0].Moof.Traf.Trun.Samples {
		assert.Equal(t, uint32(3000), s.Dur, "slate uses the RepData default sample duration")
	}

	// With no duration source at all, it still errors rather than emit a malformed slate.
	meta.rep = &RepData{ID: "V300", InitURI: "V300/init.mp4"}
	_, err = applyAdBreakSlate(vodFS, a, cfg, meta, stripped())
	require.Error(t, err)
}

func TestAdBreakForSegment(t *testing.T) {
	cfg := NewResponseConfig()
	cfg.SGAI = &SGAIConfig{AdBreaks: AdBreaks{Breaks: []AdBreak{{OffsetS: 30, DurationS: 15}}}}

	// Segment starting at 30s (in 90k ticks) is in the break; end reported in epoch ms.
	// The event id is the 1-based break index (matches the live MPD's Replace event id).
	endMS, id, ok := adBreakForSegment(cfg, segMeta{newTime: 30 * 90000, timescale: 90000})
	assert.True(t, ok)
	assert.Equal(t, int64(45_000), endMS)
	assert.Equal(t, uint64(1), id)

	// Just before and at the break end: not in the break.
	_, _, ok = adBreakForSegment(cfg, segMeta{newTime: 28 * 90000, timescale: 90000})
	assert.False(t, ok)
	_, _, ok = adBreakForSegment(cfg, segMeta{newTime: 45 * 90000, timescale: 90000})
	assert.False(t, ok)

	// svta_ drives the same slate from its own schedule.
	svtaCfg := NewResponseConfig()
	svtaCfg.SVTA = &SVTAConfig{AdBreaks: AdBreaks{Breaks: []AdBreak{{OffsetS: 30, DurationS: 15}}}, AdsPerBreak: 1}
	endMS, id, ok = adBreakForSegment(svtaCfg, segMeta{newTime: 30 * 90000, timescale: 90000})
	assert.True(t, ok)
	assert.Equal(t, int64(45_000), endMS)
	assert.Equal(t, uint64(1), id)

	// No ad-signaling option at all: no slate.
	_, _, ok = adBreakForSegment(NewResponseConfig(), segMeta{newTime: 30 * 90000, timescale: 90000})
	assert.False(t, ok)
}

// TestSlateWindowForSegment covers the two windows the slate renders: the break itself,
// counting down to its end, and the pre-break announcement, counting down to its start.
func TestSlateWindowForSegment(t *testing.T) {
	// A 20 s break 10 s after every full minute, announced 5 s ahead.
	cfg := NewResponseConfig()
	sc, err := CreateSCTE35Config("p60:20@10;pre=5")
	require.NoError(t, err)
	cfg.SCTE35 = sc

	cases := []struct {
		desc        string
		segStartS   int
		wantOK      bool
		wantUntilMS int64
		wantHeading string
	}{
		{desc: "well before the break", segStartS: 2, wantOK: false},
		{desc: "just outside the pre window", segStartS: 4, wantOK: false},
		{desc: "in the pre window", segStartS: 6, wantOK: true, wantUntilMS: 10_000, wantHeading: slateHeadingPre},
		{desc: "last second before the break", segStartS: 9, wantOK: true, wantUntilMS: 10_000, wantHeading: slateHeadingPre},
		{desc: "break start", segStartS: 10, wantOK: true, wantUntilMS: 30_000, wantHeading: slateHeadingBreak},
		{desc: "inside the break", segStartS: 28, wantOK: true, wantUntilMS: 30_000, wantHeading: slateHeadingBreak},
		{desc: "break end", segStartS: 30, wantOK: false},
		{desc: "pre window of the next minute", segStartS: 66, wantOK: true, wantUntilMS: 70_000, wantHeading: slateHeadingPre},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			meta := segMeta{newTime: uint64(c.segStartS) * 90000, timescale: 90000}
			untilMS, id, heading, ok := slateWindowForSegment(cfg, meta)
			require.Equal(t, c.wantOK, ok)
			if !ok {
				return
			}
			assert.Equal(t, c.wantUntilMS, untilMS)
			assert.Equal(t, c.wantHeading, heading)
			assert.NotZero(t, id, "the countdown names the break occurrence")
		})
	}

	// The pre-break slate needs pre=, and the slate itself needs slate=1.
	noPre := NewResponseConfig()
	noPre.SCTE35, err = CreateSCTE35Config("p60:20@10")
	require.NoError(t, err)
	_, _, _, ok := slateWindowForSegment(noPre, segMeta{newTime: 6 * 90000, timescale: 90000})
	assert.False(t, ok, "without pre= the seconds before a break are normal content")

	noSlate := NewResponseConfig()
	noSlate.SCTE35, err = CreateSCTE35Config("1") // legacy preset: signaling only
	require.NoError(t, err)
	_, _, _, ok = slateWindowForSegment(noSlate, segMeta{newTime: 15 * 90000, timescale: 90000})
	assert.False(t, ok, "the legacy presets do not slate their breaks")
}

// TestNextBreakWithin covers the forward lookup the pre-break countdown uses, including the
// wrap to the next cycle and the multi-offset schedules.
func TestNextBreakWithin(t *testing.T) {
	ab := AdBreaks{Periodic: &AdBreakPeriodic{PeriodS: 60, DurationS: 10, OffsetsS: []int{10, 40}}}
	cases := []struct {
		desc      string
		wallMS    int64
		withinS   int
		wantStart int64
		wantID    uint64
		wantOK    bool
	}{
		{desc: "before the first break", wallMS: 6_000, withinS: 5, wantStart: 10_000, wantID: 1, wantOK: true},
		{desc: "too far ahead", wallMS: 2_000, withinS: 5, wantOK: false},
		{desc: "at the break start is not ahead", wallMS: 10_000, withinS: 5, wantOK: false},
		{desc: "second break of the cycle", wallMS: 37_000, withinS: 5, wantStart: 40_000, wantID: 2, wantOK: true},
		{desc: "wraps to the next cycle", wallMS: 57_000, withinS: 15, wantStart: 70_000, wantID: 3, wantOK: true},
		{desc: "no lookahead at all", wallMS: 6_000, withinS: 0, wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			start, id, ok := ab.nextBreakWithin(c.wallMS, 0, c.withinS)
			require.Equal(t, c.wantOK, ok)
			if !ok {
				return
			}
			assert.Equal(t, c.wantStart, start)
			assert.Equal(t, c.wantID, id)
		})
	}

	// Fixed breaks, and nothing before the availabilityStartTime.
	fixed := AdBreaks{Breaks: []AdBreak{{OffsetS: 30, DurationS: 15}}}
	start, id, ok := fixed.nextBreakWithin(27_000, 0, 5)
	require.True(t, ok)
	assert.Equal(t, int64(30_000), start)
	assert.Equal(t, uint64(1), id)
	_, _, ok = ab.nextBreakWithin(6_000, 100, 5)
	assert.False(t, ok, "an occurrence before the availabilityStartTime is never signaled or slated")
}

// TestApplySCTE35Slate checks the wiring from the scte35_ option to the generated slate:
// the break window and the pre-break window are both slated, and the seconds before the
// pre-window are served as normal content.
func TestApplySCTE35Slate(t *testing.T) {
	vodFS := os.DirFS("testdata")
	a := &asset{AssetPath: "assets/testpic_2s"}
	rep := &RepData{ID: "V300", InitURI: "V300/init.mp4"}
	cfg := NewResponseConfig()
	sc, err := CreateSCTE35Config("p60:20@10;cmd=timesignal;pre=4")
	require.NoError(t, err)
	cfg.SCTE35 = sc

	for _, c := range []struct {
		desc      string
		segStartS int
		wantSlate bool
	}{
		{desc: "before the pre window", segStartS: 4, wantSlate: false},
		{desc: "in the pre window", segStartS: 6, wantSlate: true},
		{desc: "in the break", segStartS: 12, wantSlate: true},
		{desc: "after the break", segStartS: 30, wantSlate: false},
	} {
		t.Run(c.desc, func(t *testing.T) {
			meta := segMeta{rep: rep, newTime: uint64(c.segStartS) * 90000, newNr: 1, timescale: 90000}
			slate, err := applyAdBreakSlate(vodFS, a, cfg, meta, slateTestSegment(t))
			require.NoError(t, err)
			if !c.wantSlate {
				assert.Nil(t, slate)
				return
			}
			require.NotNil(t, slate)
			require.Len(t, slate.Fragments, 1)
			first := slate.Fragments[0].Moof.Traf.Trun.Samples[0]
			assert.Equal(t, mp4.SyncSampleFlags, first.Flags, "slate starts with an IDR")
		})
	}

	// The same stream with slate=0 keeps the underlying content everywhere.
	quiet := NewResponseConfig()
	quiet.SCTE35, err = CreateSCTE35Config("p60:20@10;cmd=timesignal;slate=0")
	require.NoError(t, err)
	meta := segMeta{rep: rep, newTime: 12 * 90000, newNr: 1, timescale: 90000}
	slate, err := applyAdBreakSlate(vodFS, a, quiet, meta, slateTestSegment(t))
	require.NoError(t, err)
	assert.Nil(t, slate)
}
