// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/rs/zerolog/log"
)

const (
	SUBS_STPP_PREFIX    = "timestpp"
	SUBS_WVTT_PREFIX    = "timewvtt"
	SUBS_TIME_INIT      = "init.mp4"
	SUBS_TIME_TIMESCALE = 1000
)

func timeSubsSegmentParts(prefix, segmentPart string) (lang string, segment string, ok bool) {
	rep, seg, ok := strings.Cut(segmentPart, "/")
	if !ok {
		return "", "", false
	}
	pfx, lang, ok := strings.Cut(rep, "-")
	if !ok {
		return "", "", false
	}
	if pfx != prefix {
		return "", "", false
	}
	return lang, seg, true
}

func isTimeSubsInitSegment(prefix, segmentPart string) (lang string, ok bool) {
	lang, seg, ok := timeSubsSegmentParts(prefix, segmentPart)
	if !ok {
		return "", false
	}
	if seg == SUBS_TIME_INIT {
		return lang, true
	}
	return "", false
}

func writeTimeSubsInitSegment(w http.ResponseWriter, cfg *ResponseConfig, a *asset, segmentPart string) (bool, error) {
	prefix := ""
	var langs []string
	lang, ok := isTimeSubsInitSegment(SUBS_STPP_PREFIX, segmentPart)
	if ok {
		prefix = SUBS_STPP_PREFIX
		langs = cfg.TimeSubsStpp
	} else {
		lang, ok = isTimeSubsInitSegment(SUBS_WVTT_PREFIX, segmentPart)
		if ok {
			prefix = SUBS_WVTT_PREFIX
			langs = cfg.TimeSubsWvtt
		}
	}

	if prefix == "" {
		return false, nil
	}

	matchingLang := false
	for _, mpdLang := range langs {
		if mpdLang == lang {
			matchingLang = true
			break
		}
	}
	if !matchingLang {
		return true, fmt.Errorf("time subs language %q does not match config: %w", lang, errNotFound)
	}
	init := createTimeSubsInitSegment(prefix, lang, SUBS_TIME_TIMESCALE)
	w.Header().Set("Content-Type", "application/mp4")
	w.Header().Set("Content-Length", strconv.Itoa(int(init.Size())))
	err := init.Encode(w)
	if err != nil {
		log.Error().Err(err).Msg("write init response")
		return true, err
	}
	return true, nil
}

func createTimeSubsInitSegment(prefix, lang string, timescale uint32) *mp4.InitSegment {
	switch prefix {
	case SUBS_STPP_PREFIX:
		return createSubtitlesStppInitSegment(lang, timescale)
	default: //SUBS_WVTT_PREFIX:
		return createSubtitlesWvttInitSegment(lang, timescale)
	}
}

func createSubtitlesStppInitSegment(lang string, timescale uint32) *mp4.InitSegment {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(timescale, "stpp", lang)
	trak := init.Moov.Trak
	schemaLocation := ""
	auxiliaryMimeType := ""
	_ = trak.SetStppDescriptor("http://www.w3.org/ns/ttml", schemaLocation, auxiliaryMimeType)
	return init
}

// StppTimeData is information for creating an stpp media segment.
type StppTimeData struct {
	Lang   string
	Region int
	Cues   []StppTimeCue
}

// StppTimeCue is cue information to put in template.
type StppTimeCue struct {
	Id    string
	Begin string
	End   string
	Msg   string
}

// writeTimeStppMediaSegment return true and tries to write a stpp time subtitle segment if URL matches
func writeTimeSubsMediaSegment(w http.ResponseWriter, cfg *ResponseConfig, a *asset, segmentPart string, nowMS int, tt *template.Template) (bool, error) {
	prefix := ""
	var langs []string
	lang, seg, ok := timeSubsSegmentParts(SUBS_STPP_PREFIX, segmentPart)
	if ok {
		prefix = SUBS_STPP_PREFIX
		langs = cfg.TimeSubsStpp
	} else {
		lang, seg, ok = timeSubsSegmentParts(SUBS_WVTT_PREFIX, segmentPart)
		if ok {
			prefix = SUBS_WVTT_PREFIX
			langs = cfg.TimeSubsWvtt
		}
	}

	if prefix == "" {
		return false, nil
	}
	matchingLang := false
	for _, mpdLang := range langs {
		if mpdLang == lang {
			matchingLang = true
			break
		}
	}
	if !matchingLang {
		return true, fmt.Errorf("time subs language %q does not match config: %w", lang, errNotFound)
	}
	nrStr, ext, ok := strings.Cut(seg, ".")
	if !ok {
		return true, fmt.Errorf("bad URL: %w", errNotFound)
	}
	if ext != "m4s" {
		return true, fmt.Errorf("bad seg extension %s: %w", ext, errNotFound)
	}
	nrOrTime, err := strconv.Atoi(nrStr)
	if err != nil {
		return true, fmt.Errorf("bad seg nr %s: %w", nrStr, errNotFound)
	}
	// Must validate that nrOrTime is within valid range
	// This is done by looking up a corresponding video segment.
	// That segments also gives the right time range

	refSegMeta, err := a.getRefSegMeta(nrOrTime, cfg, SUBS_TIME_TIMESCALE, nowMS)
	if err != nil {
		return true, fmt.Errorf("getRefSegMeta: %w", err)
	}

	log.Debug().Uint32("nr", refSegMeta.newNr).Msg("segMeta")
	baseMediaDecodeTime := rep2SubsTime(refSegMeta.newTime, int(refSegMeta.timescale))
	dur := uint32(rep2SubsTime(uint64(refSegMeta.newDur), int(refSegMeta.timescale)))

	utcTimeMS := baseMediaDecodeTime + uint64(cfg.StartTimeS*SUBS_TIME_TIMESCALE)
	var mediaSeg *mp4.MediaSegment
	switch prefix {
	case SUBS_STPP_PREFIX:
		mediaSeg, err = createSubtitlesStppMediaSegment(refSegMeta.newNr, baseMediaDecodeTime, dur, lang, utcTimeMS,
			tt, cfg.TimeSubsDurMS, cfg.TimeSubsRegion)
	default: // SUBS_WVTT_PREFIX
		mediaSeg, err = createSubtitlesWvttMediaSegment(refSegMeta.newNr, baseMediaDecodeTime, dur, lang, utcTimeMS,
			tt, cfg.TimeSubsDurMS, cfg.TimeSubsRegion)
	}
	if err != nil {
		return true, fmt.Errorf("createSubtitleStppMediaSegment: %w", err)
	}
	w.Header().Set("Content-Type", "application/mp4")
	w.Header().Set("Content-Length", strconv.Itoa(int(mediaSeg.Size())))
	err = mediaSeg.Encode(w)
	if err != nil {
		log.Error().Err(err).Msg("write media segment response")
		return true, fmt.Errorf("mediaSeg: %w", err)
	}
	return true, nil
}

// makeSttpMessage makes a message for an stpptime cue.
func makeStppMessage(lang string, utcMS, segNr int) string {
	t := time.UnixMilli(int64(utcMS))
	utc := t.UTC().Format(time.RFC3339)
	return fmt.Sprintf("%s<br/>%s # %d", utc, lang, segNr)
}

// msToTTMLTime returns a time that can be used in TTML.
func msToTTMLTime(ms int) string {
	hours := ms / 3600_000
	ms %= 3600_000
	minutes := ms / 60_000
	ms %= 60_000
	seconds := ms / 1_000
	ms %= 1_000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, ms)
}

// cueItvl with media times and what utcSecond to convey.
type cueItvl struct {
	startMS, endMS, utcS int
}

// calcCueItvls calculates all times in milliseconds.
func calcCueItvls(segStart, segDur, utcStart, cueDur int) []cueItvl {
	itvls := make([]cueItvl, 0, 2)

	diff := segStart - utcStart
	utcEndMS := utcStart + segDur

	cueFullS := int(math.Ceil(float64(cueDur) * 0.001))
	cueFullMS := cueFullS * 1000

	for utcS := utcStart / cueFullMS; utcS <= (utcStart+segDur)/cueFullMS; utcS += cueFullS {
		cueStartMS := utcS * 1000
		if cueStartMS == utcEndMS {
			break
		}
		ci := cueItvl{
			utcS:    utcS,
			startMS: cueStartMS,
			endMS:   cueStartMS + cueDur,
		}
		if ci.startMS < utcStart {
			ci.startMS = utcStart
		}
		if utcEndMS < ci.endMS {
			ci.endMS = utcEndMS
		}
		ci.startMS += diff
		ci.endMS += diff
		itvls = append(itvls, ci)
	}
	return itvls
}

func createSubtitlesStppMediaSegment(nr uint32, baseMediaDecodeTime uint64, dur uint32, lang string, utcTimeMS uint64,
	tt *template.Template, timeSubsDurMS, region int) (*mp4.MediaSegment, error) {
	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(nr, 1)
	if err != nil {
		return nil, err
	}
	seg.AddFragment(frag)
	cueItvls := calcCueItvls(int(baseMediaDecodeTime), int(dur), int(utcTimeMS), timeSubsDurMS)
	stppd := StppTimeData{
		Lang:   lang,
		Region: region,
		Cues:   make([]StppTimeCue, 0, len(cueItvls)),
	}
	for i, ci := range cueItvls {
		cue := StppTimeCue{
			Id:    fmt.Sprintf("%d-%d", nr, i),
			Begin: msToTTMLTime(ci.startMS),
			End:   msToTTMLTime(ci.endMS),
			Msg:   makeStppMessage(lang, ci.utcS*1000, int(nr)),
		}
		stppd.Cues = append(stppd.Cues, cue)
	}
	data := make([]byte, 0, 1024)
	buf := bytes.NewBuffer(data)

	err = tt.ExecuteTemplate(buf, "stpptime.xml", stppd)
	if err != nil {
		return nil, fmt.Errorf("execute stpp template: %w", err)
	}
	sampleData := buf.Bytes()
	s := mp4.Sample{
		Flags: mp4.SyncSampleFlags,
		Dur:   dur,
		Size:  uint32(len(sampleData)),
	}
	fs := mp4.FullSample{
		Sample:     s,
		DecodeTime: baseMediaDecodeTime,
		Data:       sampleData,
	}
	frag.AddFullSample(fs)
	return seg, nil
}

func rep2SubsTime(repTime uint64, timescale int) uint64 {
	return uint64(math.Round(float64(repTime*SUBS_TIME_TIMESCALE) / float64(timescale)))
}
