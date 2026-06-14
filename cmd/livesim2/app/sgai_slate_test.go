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
		Periodic:       &SGAIPeriodic{PeriodS: 60, DurationS: 20},
		ResolveOffsetS: 60,
	}
	seg := slateTestSegment(t)
	origNrSamples := int(seg.Fragments[0].Moof.Traf.Trun.SampleCount())

	// Segment at media time 0 = epoch with AST = epoch: inside the break [0, 20s).
	meta := segMeta{rep: rep, newTime: 0, newNr: 42, timescale: 90000}
	slate, err := applySGAISlate(vodFS, a, cfg, meta, seg)
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
	slate, err = applySGAISlate(vodFS, a, cfg, meta, seg)
	require.NoError(t, err)
	assert.Nil(t, slate)

	// A rep that cannot be slated (bad init) is skipped without error.
	badRep := &RepData{ID: "missing", InitURI: "missing/init.mp4"}
	meta = segMeta{rep: badRep, newTime: 0, newNr: 1, timescale: 90000}
	slate, err = applySGAISlate(vodFS, a, cfg, meta, seg)
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
	cfg.SGAI = &SGAIConfig{Periodic: &SGAIPeriodic{PeriodS: 60, DurationS: 20}, ResolveOffsetS: 60}
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
	slate, err := applySGAISlate(vodFS, a, cfg, meta, stripped())
	require.NoError(t, err)
	require.NotNil(t, slate)
	for _, s := range slate.Fragments[0].Moof.Traf.Trun.Samples {
		assert.Equal(t, uint32(3000), s.Dur, "slate uses the RepData default sample duration")
	}

	// With no duration source at all, it still errors rather than emit a malformed slate.
	meta.rep = &RepData{ID: "V300", InitURI: "V300/init.mp4"}
	_, err = applySGAISlate(vodFS, a, cfg, meta, stripped())
	require.Error(t, err)
}

func TestSGAIBreakForSegment(t *testing.T) {
	cfg := NewResponseConfig()
	cfg.SGAI = &SGAIConfig{Breaks: []SGAIBreak{{OffsetS: 30, DurationS: 15}}}

	// Segment starting at 30s (in 90k ticks) is in the break; end reported in epoch ms.
	// The event id is the 1-based break index (matches the live MPD's Replace event id).
	endMS, id, ok := sgaiBreakForSegment(cfg, segMeta{newTime: 30 * 90000, timescale: 90000})
	assert.True(t, ok)
	assert.Equal(t, int64(45_000), endMS)
	assert.Equal(t, uint64(1), id)

	// Just before and at the break end: not in the break.
	_, _, ok = sgaiBreakForSegment(cfg, segMeta{newTime: 28 * 90000, timescale: 90000})
	assert.False(t, ok)
	_, _, ok = sgaiBreakForSegment(cfg, segMeta{newTime: 45 * 90000, timescale: 90000})
	assert.False(t, ok)

	// Periodic: membership is a pure function of the segment time — a long-past break
	// (still in the timeshift buffer) keeps its slate no matter when it is requested.
	// The event id is the occurrence number since the epoch (breakStart/period + 1).
	per := &SGAIConfig{Periodic: &SGAIPeriodic{PeriodS: 60, DurationS: 20}}
	endMS, id, ok = per.breakWindowAt(999_970_000, 0) // 10s into the break [999_960s, 999_980s)
	assert.True(t, ok)
	assert.Equal(t, int64(999_980_000), endMS)
	assert.Equal(t, uint64(16667), id)           // 999_960/60 + 1
	_, _, ok = per.breakWindowAt(999_985_000, 0) // between breaks
	assert.False(t, ok)
	endMS, id, ok = per.breakWindowAt(60_000, 0) // exactly at a break start
	assert.True(t, ok)
	assert.Equal(t, int64(80_000), endMS)
	assert.Equal(t, uint64(2), id)          // 60/60 + 1: the 2nd occurrence since the epoch
	_, _, ok = per.breakWindowAt(80_000, 0) // exactly at the break end
	assert.False(t, ok)

	// A break occurrence that started before the availabilityStartTime is not signaled by
	// breakInstances (its presentationTime would be negative), so breakWindowAt must not slate
	// it either: with astS=65 the occurrence at [60s, 80s) straddles the AST and is skipped,
	// even though the request time (70s) is inside the window and after the AST.
	_, _, ok = per.breakWindowAt(70_000, 65)
	assert.False(t, ok)
	// breakInstances agrees: the straddling [60s,80s) break is dropped; the first signaled
	// occurrence is the next full one at [120s,140s) with id 120/60+1 = 3.
	insts := per.breakInstances(70_000, 65)
	assert.Equal(t, 1, len(insts))
	assert.Equal(t, uint64(3), insts[0].id)
	// That next full occurrence is both signaled and slated, with matching event ids.
	_, id, ok = per.breakWindowAt(130_000, 65)
	assert.True(t, ok)
	assert.Equal(t, uint64(3), id)
}
