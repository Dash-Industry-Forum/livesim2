// Copyright 2026, DASH-Industry-Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// The experimental paint-model subtitle variants. A chunk that restates what the previous
// one already said is sent as an 8-byte no-change box instead of the restatement, so the
// subtitle track can be chunked at the video chunk cadence without paying a full document
// per chunk. See the "Low-latency subtitles" section of the README.
//
// The 4CCs stpc, wvtc, ttmn, ttmb and vttn are placeholders that are NOT registered with
// MP4RA, and neither variant is standardised. They exist here to measure the gap that
// today's signalling leaves; see https://github.com/Eyevinn/paint-model-subtitles.
const (
	SUBS_STPC_PREFIX = "timestpc"
	SUBS_WVTC_PREFIX = "timewvtc"
)

// paintDependentFlags marks a sample that is not self-contained: sample_is_non_sync_sample
// = 1 and sample_depends_on = 1 (ISO/IEC 14496-12 Sec. 8.8.3.1). Both a no-change box and
// a body-only box are such samples: they mean nothing without an earlier sample of the
// same segment. This is what makes the new sample entries necessary — ISO/IEC 14496-30
// Sec. 5.6 says every stpp sample is a sync sample, which is no longer true here.
const paintDependentFlags uint32 = 1<<24 | mp4.NonSyncSampleFlags

// ttmnBox is a TTMLNoChangeBox and vttnBox a VTTNoChangeBox. Each is a complete sample of
// 8 bytes saying that what is on screen continues unchanged. They are never modified.
var (
	ttmnBox = encodeWholeSampleBox(&mp4.TtmnBox{})
	vttnBox = encodeWholeSampleBox(&mp4.VttnBox{})
)

// encodeWholeSampleBox encodes a box that is itself a complete sample.
func encodeWholeSampleBox(b mp4.Box) []byte {
	sw := bits.NewFixedSliceWriter(int(b.Size()))
	if err := b.EncodeSW(sw); err != nil {
		panic(fmt.Sprintf("cannot write %s box: %s", b.Type(), err))
	}
	return sw.Bytes()
}

// TimeSubsPaintConfig configures one generated paint-model subtitle track.
//
// The URL syntax is timesubsstpc_<langs>[;nochange=0|1][;body=0|1] and
// timesubswvtc_<langs>[;nochange=0|1], where <langs> is a comma-separated language list
// like the one timesubsstpp_ and timesubswvtt_ take.
type TimeSubsPaintConfig struct {
	Languages []string `json:"languages"`
	// NoChange sends an 8-byte no-change box for a chunk that restates what the previous
	// chunk said, instead of the restatement itself. It defaults to true, since it is the
	// reason the variant exists; nochange=0 turns it off, which leaves a track that
	// differs from the stpp or wvtt one only in its 4CC and so serves as a control.
	NoChange bool `json:"nochange"`
	// Body sends only the body element of the TTML document, in a ttmb box, for a changed
	// chunk that is not the first of its segment. The receiver splices in the head from
	// that first chunk. stpc only, and off by default: it pays only at high update rates.
	Body bool `json:"body,omitempty"`
}

// CreateTimeSubsPaintConfig parses the value of a timesubsstpc_ or timesubswvtc_ option.
// kind is the sample entry name, and is used both in errors and to reject body for wvtc,
// where the header already lives in the vttC box of the sample entry.
func CreateTimeSubsPaintConfig(kind, val string) (*TimeSubsPaintConfig, error) {
	if val == "" {
		return nil, fmt.Errorf("empty %s config", kind)
	}
	if hasExtraSpaces(val) {
		return nil, fmt.Errorf("%s config %q has extra spaces", kind, val)
	}
	cfg := &TimeSubsPaintConfig{NoChange: true}
	parts := strings.Split(val, ";")
	for _, lang := range strings.Split(parts[0], ",") {
		if lang == "" {
			return nil, fmt.Errorf("%s config %q has an empty language", kind, val)
		}
		cfg.Languages = append(cfg.Languages, lang)
	}
	for _, kv := range parts[1:] {
		key, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("%s param %q must be key=val", kind, kv)
		}
		var err error
		switch key {
		case "nochange":
			cfg.NoChange, err = parseTimeSubsPaintBool(kind, key, v)
		case "body":
			if kind != "stpc" {
				return nil, fmt.Errorf("%s does not support body: a wvtt cue carries no header", kind)
			}
			cfg.Body, err = parseTimeSubsPaintBool(kind, key, v)
		default:
			return nil, fmt.Errorf("unknown %s param %q", kind, key)
		}
		if err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func parseTimeSubsPaintBool(kind, key, v string) (bool, error) {
	switch v {
	case "1":
		return true, nil
	case "0":
		return false, nil
	}
	return false, fmt.Errorf("%s %s %q: must be 0 or 1", kind, key, v)
}

// ParseTimeSubsPaintConfig parses a paint-model subtitle option value, accumulating any
// error on the converter.
func (s *strConvAccErr) ParseTimeSubsPaintConfig(key, kind, val string) *TimeSubsPaintConfig {
	if s.err != nil {
		return nil
	}
	cfg, err := CreateTimeSubsPaintConfig(kind, val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return cfg
}

// paintNoChangeSample turns a restatement into the 8-byte no-change box of its format,
// keeping the sample's time and duration so that the track still tiles the timeline.
func paintNoChangeSample(s mp4.FullSample, prefix string) mp4.FullSample {
	data := ttmnBox
	if prefix == SUBS_WVTC_PREFIX {
		data = vttnBox
	}
	s.Data = data
	s.Size = uint32(len(data))
	s.Flags = paintDependentFlags
	return s
}

// stpcBodySample renders only the body element of the TTML document covering
// [startMS, endMS) and returns it as one sample carrying a ttmb box. The head comes from
// the first chunk of the same segment, so the sample is not self-contained.
func stpcBodySample(tt *template.Template, cues []cueItvl, startMS, endMS int, tss timeSubsSeg,
	cfg *ResponseConfig) (mp4.FullSample, error) {
	s, err := stppTimeSampleTmpl(tt, "stpctimebody.xml", cues, startMS, endMS, tss.lang, tss.nr,
		cfg.TimeSubsRegion, cfg.TimeSubsSegNr)
	if err != nil {
		return s, err
	}
	ttmb := mp4.TtmbBox{Body: string(s.Data)}
	sw := bits.NewFixedSliceWriter(int(ttmb.Size()))
	if err := ttmb.EncodeSW(sw); err != nil {
		return s, fmt.Errorf("write ttmb box: %w", err)
	}
	s.Data = sw.Bytes()
	s.Size = uint32(len(s.Data))
	s.Flags = paintDependentFlags
	return s, nil
}

func createSubtitlesStpcInitSegment(lang string, timescale uint32) *mp4.InitSegment {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(timescale, "subt", lang)
	trak := init.Moov.Trak
	_ = trak.SetStpcDescriptor("http://www.w3.org/ns/ttml", "", "")
	return init
}

func createSubtitlesWvtcInitSegment(lang string, timescale uint32) *mp4.InitSegment {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(timescale, "text", lang)
	trak := init.Moov.Trak
	_ = trak.SetWvtcDescriptor("WEBVTT")
	return init
}
