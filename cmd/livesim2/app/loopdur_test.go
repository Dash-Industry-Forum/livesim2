// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"log/slog"
	"testing"
	"time"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/require"
)

// ntscAsset returns an asset of 14 segments of 256 frames at 60000/1001 fps. Its loop lasts
// 3584 * 1001 / 60 ms = 59793.0666... ms, which is not an integral number of milliseconds.
func ntscAsset(t *testing.T) *asset {
	t.Helper()
	const segDur = 256 * 1001
	segs := make([]Segment, 14)
	for i := range segs {
		segs[i] = Segment{StartTime: uint64(i * segDur), EndTime: uint64((i + 1) * segDur), Nr: uint32(i)}
	}
	a := newAsset("ntsc")
	a.Reps["video"] = &RepData{ID: "video", ContentType: "video", MediaTimescale: 60000, Segments: segs}
	require.NoError(t, a.consolidateAsset(slog.Default()))
	return a
}

// TestNonIntegralMSLoop checks that an asset whose loop is not an integral number of
// milliseconds is accepted, and that MPD timeline and segment lookup agree with the
// wall clock long after the start (1970), where a loop rounded to milliseconds has
// accumulated an error of more than 30 minutes.
func TestNonIntegralMSLoop(t *testing.T) {
	a := ntscAsset(t)
	require.Equal(t, 59793, a.LoopDurMS)
	rep := a.Reps["video"]
	segDurMS := 256 * 1001 / 60.0

	cfg := NewResponseConfig()
	nowMS := 1_795_513_070_000 // 2026-11-24
	wt := calcWrapTimes(a, cfg, nowMS, m.Duration(60*time.Second))
	se, err := a.generateTimelineEntries(rep.ID, wt, 0, nil)
	require.NoError(t, err)

	// The last segment in the timeline has just ended.
	lastEndMS := float64(se.lsi.startTime+se.lsi.dur) * 1000 / float64(rep.MediaTimescale)
	require.LessOrEqual(t, lastEndMS, float64(nowMS))
	require.Greater(t, lastEndMS, float64(nowMS)-segDurMS)

	// Segment lookup by number and by time finds the same segment at the same time.
	nr := uint32(se.lsi.nr)
	sm, err := findSegMetaFromNr(a, rep, nr, cfg, nowMS)
	require.NoError(t, err)
	require.Equal(t, se.lsi.startTime, sm.newTime)
	sm, err = findSegMetaFromTime(a, rep, se.lsi.startTime, cfg, nowMS)
	require.NoError(t, err)
	require.Equal(t, nr, sm.newNr)
	availMS, err := calcSegmentAvailabilityTime(a, rep, nr, cfg)
	require.NoError(t, err)
	require.InDelta(t, lastEndMS, float64(availMS), 1)

	// The next segment is not available yet.
	_, err = findSegMetaFromNr(a, rep, nr+1, cfg, nowMS)
	require.Error(t, err)
}

// TestNonIntegralMSLoopTimescales checks that an asset whose loop is not an integral number of
// milliseconds is refused if the loop is not an integral number of ticks in the timescale of
// another representation.
func TestNonIntegralMSLoopTimescales(t *testing.T) {
	const segDur = 256 * 1001
	video := &RepData{ID: "video", ContentType: "video", MediaTimescale: 60000,
		Segments: []Segment{{StartTime: 0, EndTime: segDur}}}
	// 256256 / 60000 s = 4.271 s of audio at 44100 Hz is 188348.16 samples.
	audio := &RepData{ID: "audio", ContentType: "audio", MediaTimescale: 44100,
		Segments: []Segment{{StartTime: 0, EndTime: 188348}}}
	a := newAsset("ntsc-audio")
	a.Reps["video"] = video
	a.Reps["audio"] = audio
	require.Error(t, a.consolidateAsset(slog.Default()))
}
