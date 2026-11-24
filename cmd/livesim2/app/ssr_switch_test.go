// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/av1"
	"github.com/stretchr/testify/require"
)

// av1OBU returns an OBU with obu_has_size_field set and a one-byte size.
func av1OBU(obuType av1.OBUType, payload []byte) []byte {
	return append([]byte{byte(obuType)<<3 | 0x02, byte(len(payload))}, payload...)
}

// av1FrameStart returns the first byte of an uncompressed frame header: show_existing_frame,
// frame_type (2 bits) and show_frame, followed by zero bits.
func av1FrameStart(showExisting bool, frameType av1.FrameType) []byte {
	if showExisting {
		return []byte{0x80}
	}
	return []byte{byte(frameType)<<5 | 0x10}
}

func TestIsAV1SwitchSample(t *testing.T) {
	sh := &av1.SequenceHeader{}
	td := av1OBU(av1.OBUTemporalDelimiter, nil)
	cases := []struct {
		desc   string
		sample []byte
		want   bool
	}{
		{"switch frame", append(td, av1OBU(av1.OBUFrame, av1FrameStart(false, av1.FrameTypeSwitch))...), true},
		{"switch frame header", av1OBU(av1.OBUFrameHeader, av1FrameStart(false, av1.FrameTypeSwitch)), true},
		{"key frame", av1OBU(av1.OBUFrame, av1FrameStart(false, av1.FrameTypeKey)), false},
		{"inter frame", av1OBU(av1.OBUFrame, av1FrameStart(false, av1.FrameTypeInter)), false},
		{"shown existing frame", av1OBU(av1.OBUFrameHeader, av1FrameStart(true, 0)), false},
		{"inter frame before a switch frame", append(av1OBU(av1.OBUFrame, av1FrameStart(false, av1.FrameTypeInter)),
			av1OBU(av1.OBUFrame, av1FrameStart(false, av1.FrameTypeSwitch))...), false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got, _, err := isAV1SwitchSample(c.sample, sh)
			require.NoError(t, err)
			require.Equal(t, c.want, got)
		})
	}
	_, _, err := isAV1SwitchSample(av1OBU(av1.OBUFrame, av1FrameStart(false, av1.FrameTypeSwitch)), nil)
	require.Error(t, err, "no sequence header")
}

func TestSwitchGrid(t *testing.T) {
	seg := func(start uint64, sync, switches []uint64) Segment {
		return Segment{StartTime: start, EndTime: start + 64, syncOffsets: sync, switchOffsets: switches}
	}
	cases := []struct {
		desc string
		segs []Segment
		want uint64
	}{
		{"switch frames every 16", []Segment{seg(0, []uint64{0}, []uint64{16, 32, 48}), seg(64, []uint64{0}, []uint64{16, 32, 48})}, 16},
		{"sync and switch frames alternate", []Segment{seg(0, []uint64{0, 32}, []uint64{16, 48})}, 16},
		{"no switch frames", []Segment{seg(0, []uint64{0, 32}, nil)}, 0},
		{"one segment lacks every other switch frame", []Segment{seg(0, []uint64{0}, []uint64{16, 32, 48}), seg(64, []uint64{0}, []uint64{32})}, 32},
		{"switch frames off the grid in one segment", []Segment{seg(0, []uint64{0}, []uint64{16, 32, 48}), seg(64, []uint64{0}, []uint64{16, 40})}, 0},
		{"no sync sample at the start", []Segment{seg(0, nil, []uint64{16, 32, 48})}, 0},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			require.Equal(t, c.want, switchGrid(c.segs))
		})
	}
}

// TestSSRSwitchFrameSignaling checks the SegmentSequenceProperties of a low-delay
// AdaptationSet with 8 Partial Segments of 32 frames at 60000/1001 fps per segment.
func TestSSRSwitchFrameSignaling(t *testing.T) {
	const partDur = 32 * 1001
	cases := []struct {
		desc       string
		sapGrid    uint64
		switchGrid uint64
		want       []*m.SegmentSequencePropertiesType
	}{
		{"switch frame at every partial", 0, partDur,
			[]*m.SegmentSequencePropertiesType{{SapType: SAPTypeAV1SwitchFrame, Cadence: 1}}},
		{"sync sample at every partial", partDur, 0,
			[]*m.SegmentSequencePropertiesType{{SapType: 1, Cadence: 1}}},
		{"sync sample at every 4th, switch frame at every partial", 4 * partDur, partDur,
			[]*m.SegmentSequencePropertiesType{{SapType: 1, Cadence: 4}, {SapType: SAPTypeAV1SwitchFrame, Cadence: 1}}},
		{"switch frames no denser than sync samples", 4 * partDur, 4 * partDur,
			[]*m.SegmentSequencePropertiesType{{SapType: 1, Cadence: 4}}},
		{"switch frames at every 2nd partial", 0, 2 * partDur,
			[]*m.SegmentSequencePropertiesType{{SapType: SAPTypeAV1SwitchFrame, Cadence: 2}}},
		{"neither", 0, 0, nil},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			a := newAsset("ssr")
			a.Reps["main"] = &RepData{ID: "main", ContentType: "video", MediaTimescale: 60000,
				Segments: []Segment{{StartTime: 0, EndTime: 8 * partDur}}, SAPGrid: c.sapGrid, SwitchGrid: c.switchGrid}
			as := &m.AdaptationSetType{Id: new(uint32(0)), ContentType: "video",
				Representations: []*m.RepresentationType{{Id: "main"}}}
			var explicitChunkDurS *float64
			updateSSRAdaptationSet(a, as, 1, nil, map[uint32]float64{0: 0.53387}, &explicitChunkDurS)
			require.Equal(t, c.want, as.SegmentSequenceProperties)
		})
	}

	// Without Partial Segments, each Segment Sequence is a single segment starting with a SAP.
	a := newAsset("ssr")
	a.Reps["main"] = &RepData{ID: "main", ContentType: "video", MediaTimescale: 60000,
		Segments: []Segment{{StartTime: 0, EndTime: 8 * partDur}}, SwitchGrid: partDur}
	as := &m.AdaptationSetType{Id: new(uint32(0)), ContentType: "video",
		Representations: []*m.RepresentationType{{Id: "main"}}}
	var explicitChunkDurS *float64
	updateSSRAdaptationSet(a, as, 1, nil, map[uint32]float64{}, &explicitChunkDurS)
	require.Equal(t, []*m.SegmentSequencePropertiesType{{SapType: 1, Cadence: 1}}, as.SegmentSequenceProperties)
}

func TestRepDataIsStale(t *testing.T) {
	cases := []struct {
		version int
		codecs  string
		want    bool
	}{
		{currentRepDataVersion, "av01.0.08M.08", false},
		{2, "avc1.64001e", false},
		{2, "mp4a.40.2", false},
		{2, "av01.0.08M.08", true},
		{1, "avc1.64001e", true},
		{0, "avc1.64001e", true},
	}
	for _, c := range cases {
		got := repDataIsStale(&RepData{Version: c.version, Codecs: c.codecs})
		require.Equal(t, c.want, got, "version %d codecs %s", c.version, c.codecs)
	}
}
