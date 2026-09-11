// Copyright 2023, DASH-Industry-Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"time"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// subsTrackID is the track ID of the generated subtitle tracks. There is only one
// track per representation, and mp4.InitSegment.AddEmptyTrack numbers it from 1.
const subsTrackID = 1

// vtteBox is an empty WebVTT cue box, used to fill the intervals where nothing is shown.
// It is never modified.
var vtteBox = []byte{0, 0, 0, 8, 'v', 't', 't', 'e'}

func createSubtitlesWvttInitSegment(lang string, timescale uint32) *mp4.InitSegment {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(timescale, "wvtt", lang)
	trak := init.Moov.Trak
	_ = trak.SetWvttDescriptor("WEBVTT")
	return init
}

// WvttTimeData is information for creating a wvtt media segment.
type WvttTimeData struct {
	Lang   string
	Region int
	Cues   []WvttTimeCue
}

// WvttTimeCue is cue information to put in template.
type WvttTimeCue struct {
	Id      string
	StartMS int
	EndMS   int
	Vttc    []byte
}

// makeWvttMessage makes a message for an stpptime cue.
func makeWvttCuePayload(lang string, region, utcMS, segNr int) []byte {
	t := time.UnixMilli(int64(utcMS))
	utc := t.UTC().Format(time.RFC3339)
	pl := mp4.PaylBox{
		CueText: fmt.Sprintf("%s\n%s # %d", utc, lang, segNr),
	}
	vttc := mp4.VttcBox{}
	if region == 1 {
		sttg := mp4.SttgBox{
			Settings: "line:2",
		}
		vttc.AddChild(&sttg)
	}
	vttc.AddChild(&pl)
	sw := bits.NewFixedSliceWriter(int(vttc.Size()))
	err := vttc.EncodeSW(sw)
	if err != nil {
		panic("cannot write vttc")
	}
	return sw.Bytes()
}

// wvttTimeSamples returns the wvtt samples that tile [startMS, endMS) with no gaps.
//
// Unlike TTML, a wvtt sample carries no timing of its own, so a cue is always clipped to
// the fragment that carries it. A cue that continues across a fragment boundary is
// therefore restated, but with a byte-identical payload, which is what lets
// genTimeSubsChunks mark the restatement as redundant. The same holds for consecutive
// fragments with nothing on screen, which repeat the same empty vtte box.
func wvttTimeSamples(cues []cueItvl, startMS, endMS int, lang string, nr uint32, region int) []mp4.FullSample {
	samples := make([]mp4.FullSample, 0, 2*len(cues)+1)
	currEnd := startMS
	for _, ci := range cues {
		if !ci.overlaps(startMS, endMS) {
			continue
		}
		cueStart := max(ci.startMS, startMS)
		cueEnd := min(ci.endMS, endMS)
		if cueStart > currEnd {
			samples = append(samples, fullSample(currEnd, cueStart, vtteBox))
		}
		samples = append(samples, fullSample(cueStart, cueEnd, makeWvttCuePayload(lang, region, ci.utcS*1000, int(nr))))
		currEnd = cueEnd
	}
	if currEnd < endMS {
		samples = append(samples, fullSample(currEnd, endMS, vtteBox))
	}
	return samples
}

func fullSample(start int, end int, data []byte) mp4.FullSample {
	return mp4.FullSample{
		Sample: mp4.Sample{
			Flags: mp4.SyncSampleFlags,
			Dur:   uint32(end - start),
			Size:  uint32(len(data)),
		},
		DecodeTime: uint64(start),
		Data:       data,
	}
}
