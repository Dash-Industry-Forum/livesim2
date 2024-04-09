// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"text/template"
	"time"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

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

// makeWvttMessage makes a message for an wvtt cue.
// region = 0 => default alignment
// region = 1 => line:2
// region = 2 => line:2 and line:10 align: right
func makeWvttCuePayload(lang string, region, utcMS, segNr int) []byte {
	t := time.UnixMilli(int64(utcMS))
	utc := t.UTC().Format(time.RFC3339)
	pl := mp4.PaylBox{
		CueText: fmt.Sprintf("%s\n%s # %d", utc, lang, segNr),
	}
	boxes := make([]mp4.Box, 0, 2)
	vttc := mp4.VttcBox{}
	size := 0
	switch {
	case region == 1:
		sttg := mp4.SttgBox{Settings: "line:2"}
		vttc.AddChild(&sttg)
	case region == 2:
		sttg := mp4.SttgBox{Settings: "line:2 align:left"}
		vttc.AddChild(&sttg)
	}
	vttc.AddChild(&pl)
	size += int(vttc.Size())
	boxes = append(boxes, &vttc)
	if region > 1 {
		vttc := mp4.VttcBox{}
		sttg := mp4.SttgBox{Settings: "line:10 align:right"}
		vttc.AddChild(&sttg)
		vttc.AddChild(&pl)
		size += int(vttc.Size())
		boxes = append(boxes, &vttc)
	}
	sw := bits.NewFixedSliceWriter(size)
	for _, b := range boxes {
		err := b.EncodeSW(sw)
		if err != nil {
			panic("cannot write vttc")
		}
	}
	return sw.Bytes()
}

func createSubtitlesWvttMediaSegment(nr uint32, baseMediaDecodeTime uint64, dur uint32, lang string, utcTimeMS uint64,
	tt *template.Template, timeSubsDurMS, region int) (*mp4.MediaSegment, error) {
	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(nr, 1)
	if err != nil {
		return nil, err
	}
	seg.AddFragment(frag)
	cueItvls := calcCueItvls(int(baseMediaDecodeTime), int(dur), int(utcTimeMS), timeSubsDurMS)
	currEnd := baseMediaDecodeTime
	vtte := []byte{0, 0, 0, 8, 0x76, 0x74, 0x74, 0x65}
	for _, ci := range cueItvls {
		start := ci.startMS
		end := ci.endMS
		cuePL := makeWvttCuePayload(lang, region, ci.utcS*1000, int(nr))
		if start > int(currEnd) {
			frag.AddFullSample(fullSample(int(currEnd), start, vtte))
		}
		frag.AddFullSample(fullSample(start, end, cuePL))
		currEnd = uint64(end)
	}
	segEnd := int(baseMediaDecodeTime) + int(dur)
	if int(currEnd) < segEnd {
		frag.AddFullSample(fullSample(int(currEnd), segEnd, vtte))
	}
	return seg, nil
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
