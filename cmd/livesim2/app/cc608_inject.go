// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/generate"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// cc608TargetPeriodMS is the nominal caption update period; go-608 snaps it to an
// even division of each segment (see generate.NumCues), so a 2.002s segment gets
// two ~1.001s cues, a 1.92s segment two ~0.96s cues, etc.
const cc608TargetPeriodMS = 1000

// cc608CodecFor maps a representation codec string to the go-608 carriage codec.
func cc608CodecFor(codecs string) (carriage.Codec, bool) {
	switch {
	case strings.HasPrefix(codecs, "avc"):
		return carriage.CodecAVC, true
	case strings.HasPrefix(codecs, "hev"), strings.HasPrefix(codecs, "hvc"):
		return carriage.CodecHEVC, true
	default:
		return 0, false
	}
}

// cc608CueContent formats one cue for a segment: line 1 is the cue's UTC time
// (millisecond precision, so it stays accurate for non-integer-second segments),
// line 2 is "SEG <nr>" held constant across the segment's cues. The caller closes
// over segNr; keeping the content a pure function of (cueIdx, cueStartMS) makes a
// segment's captions independent of any other segment.
func cc608CueContent(segNr uint32) generate.CueContentFunc {
	return func(cueIdx int, cueStartMS int64) generate.UnitCue {
		ts := time.UnixMilli(cueStartMS).UTC().Format("15:04:05.000")
		seg := fmt.Sprintf("SEG %d", segNr)
		return generate.UnitCue{Lines: []cta608.Line{
			{Row: 14, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: ts, Pen: cta608.Pen{Color: cta608.White}}}},
			{Row: 15, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: seg, Pen: cta608.Pen{Color: cta608.Yellow}}}},
		}}
	}
}

// injectCC608 splices in-band CTA-608 caption SEI into a segment's video samples
// in place. It builds one self-contained per-segment caption (a UTC clock + the
// segment number, updated ~every second) via go-608 BuildUnitCues, then inserts
// the resulting per-frame SEI NALU before the first VCL NALU of each sample,
// updating Data and Size. samples are the video track's FullSamples in decode
// order; fps and unitStartMS give the caption timing; segNr is the segment number.
func injectCC608(samples []mp4.FullSample, fps float64, unitStartMS int64, segNr uint32, codec carriage.Codec) error {
	if len(samples) == 0 {
		return nil
	}
	frames, err := generate.BuildUnitCues(fps, len(samples), unitStartMS, cc608TargetPeriodMS, cc608CueContent(segNr))
	if err != nil {
		return fmt.Errorf("cc608 build cues: %w", err)
	}
	for i := range samples {
		f := frames[i]
		seiNALU := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, codec)
		newData, err := spliceSEIBeforeVCL(samples[i].Data, seiNALU, codec)
		if err != nil {
			return fmt.Errorf("cc608 splice sample %d: %w", i, err)
		}
		samples[i].Data = newData
		samples[i].Size = uint32(len(newData))
	}
	return nil
}

// spliceSEIBeforeVCL returns sampleData (length-prefixed AVCC) with seiNALU
// inserted — with its own 4-byte length prefix — immediately before the first VCL
// NALU. If there is no VCL NALU, the SEI is appended at the end. seiNALU is the
// bare NAL unit from carriage.FrameSEINALU (no length prefix).
func spliceSEIBeforeVCL(sampleData, seiNALU []byte, codec carriage.Codec) ([]byte, error) {
	nalus, err := avc.GetNalusFromSample(sampleData) // pure 4-byte-length split, codec-agnostic
	if err != nil {
		return nil, err
	}
	insertAt := len(nalus)
	for i, n := range nalus {
		if len(n) > 0 && isVCLNalu(n, codec) {
			insertAt = i
			break
		}
	}
	ordered := make([][]byte, 0, len(nalus)+1)
	ordered = append(ordered, nalus[:insertAt]...)
	ordered = append(ordered, seiNALU)
	ordered = append(ordered, nalus[insertAt:]...)

	total := 0
	for _, n := range ordered {
		total += 4 + len(n)
	}
	out := make([]byte, 0, total)
	var lenBuf [4]byte
	for _, n := range ordered {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(n)))
		out = append(out, lenBuf[:]...)
		out = append(out, n...)
	}
	return out, nil
}

// isVCLNalu reports whether a NALU (no length prefix) is a VCL (coded-slice) unit.
func isVCLNalu(nalu []byte, codec carriage.Codec) bool {
	if codec == carriage.CodecHEVC {
		return hevc.IsVideoNaluType(hevc.GetNaluType(nalu[0]))
	}
	return avc.IsVideoNaluType(avc.GetNaluType(nalu[0]))
}
