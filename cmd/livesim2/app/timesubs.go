// Copyright 2023, DASH-Industry-Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

const (
	SUBS_STPP_PREFIX    = "timestpp"
	SUBS_WVTT_PREFIX    = "timewvtt"
	SUBS_TIME_INIT      = "init.mp4"
	SUBS_TIME_TIMESCALE = 1000
)

// redundantSampleFlags marks a sync sample whose payload repeats the preceding sample.
// It is sample_depends_on = 2 (no dependency, as all timed-text samples are sync samples)
// plus sample_has_redundancy = 1 (ISO/IEC 14496-12 Sec. 8.8.3.1). For tracks that are
// neither video, audio nor hint, 14496-12 Sec. 8.6.4 lets a receiver discard such a sample
// and add its duration to the preceding one, which is exactly "nothing changed here".
const redundantSampleFlags uint32 = mp4.SyncSampleFlags | 1<<20

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

func matchTimeSubsInitLang(cfg *ResponseConfig, segmentPart string) (prefix, lang string, ok bool, err error) {
	lang, ok = isTimeSubsInitSegment(SUBS_STPP_PREFIX, segmentPart)
	var langs []string
	if ok {
		prefix = SUBS_STPP_PREFIX
		langs = cfg.TimeSubsStpp
	}
	if !ok {
		lang, ok = isTimeSubsInitSegment(SUBS_WVTT_PREFIX, segmentPart)
		if ok {
			prefix = SUBS_WVTT_PREFIX
			langs = cfg.TimeSubsWvtt
		}
	}

	if !ok {
		return "", "", false, nil
	}

	matchingLang := slices.Contains(langs, lang)
	if !matchingLang {
		return "", lang, true, fmt.Errorf("time subs language %q does not match config: %w", lang, errNotFound)
	}
	return prefix, lang, true, nil
}

func writeTimeSubsInitSegment(w http.ResponseWriter, cfg *ResponseConfig, segmentPart string) (bool, error) {
	prefix, lang, ok, err := matchTimeSubsInitLang(cfg, segmentPart)
	if !ok {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	init := createTimeSubsInitSegment(prefix, lang, SUBS_TIME_TIMESCALE)
	w.Header().Set("Content-Type", "application/mp4")
	w.Header().Set("Content-Length", strconv.Itoa(int(init.Size())))
	err = init.Encode(w)
	if err != nil {
		slog.Error("write init response", "error", err)
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
	init.AddEmptyTrack(timescale, "subt", lang)
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
// End is empty for a cue that is still active at the end of the fragment,
// in which case no end attribute is written.
type StppTimeCue struct {
	Id    string
	Begin string
	End   string
	Msg   string
}

// timeSubsSeg identifies one generated time subtitle segment on the media timeline.
type timeSubsSeg struct {
	prefix     string // SUBS_STPP_PREFIX or SUBS_WVTT_PREFIX
	lang       string
	nr         uint32 // segment number
	startMS    int    // segment start on the media timeline (SUBS_TIME_TIMESCALE)
	durMS      int    // segment duration (SUBS_TIME_TIMESCALE)
	utcStartMS int    // UTC time corresponding to startMS
}

// endMS returns the end of the segment on the media timeline.
func (t timeSubsSeg) endMS() int {
	return t.startMS + t.durMS
}

// matchTimeSubsMediaSegment parses segmentPart and, if it addresses a generated time
// subtitle media segment, returns where that segment sits on the media timeline.
// isTimeSubs is true as soon as the representation prefix matches, so that a bad
// language or segment number is reported as an error rather than falling through
// to the normal asset segment handling.
func matchTimeSubsMediaSegment(cfg *ResponseConfig, a *asset, segmentPart string, nowMS int) (
	tss timeSubsSeg, isTimeSubs bool, err error) {
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
		return tss, false, nil
	}
	matchingLang := slices.Contains(langs, lang)
	if !matchingLang {
		return tss, true, fmt.Errorf("time subs language %q does not match config: %w", lang, errNotFound)
	}
	nrStr, ext, ok := strings.Cut(seg, ".")
	if !ok {
		return tss, true, fmt.Errorf("bad URL: %w", errNotFound)
	}
	if ext != "m4s" {
		return tss, true, fmt.Errorf("bad seg extension %s: %w", ext, errNotFound)
	}
	nrOrTime, err := strconv.Atoi(nrStr)
	if err != nil {
		return tss, true, fmt.Errorf("bad seg nr %s: %w", nrStr, errNotFound)
	}
	// Must validate that nrOrTime is within valid range.
	// This is done by looking up a corresponding video segment.
	// That segment also gives the right time range.
	refSegMeta, err := a.getRefSegMeta(nrOrTime, cfg, nowMS)
	if err != nil {
		return tss, true, fmt.Errorf("getRefSegMeta: %w", err)
	}
	slog.Debug("segMeta", "nr", refSegMeta.newNr)
	startMS := int(rep2SubsTime(refSegMeta.newTime, int(refSegMeta.timescale)))
	durMS := int(rep2SubsTime(uint64(refSegMeta.newDur), int(refSegMeta.timescale)))
	return timeSubsSeg{
		prefix:     prefix,
		lang:       lang,
		nr:         refSegMeta.newNr,
		startMS:    startMS,
		durMS:      durMS,
		utcStartMS: startMS + cfg.StartTimeS*SUBS_TIME_TIMESCALE,
	}, true, nil
}

// writeTimeSubsMediaSegment returns true and writes a complete generated time subtitle
// segment if the URL matches. Chunked (low-latency) delivery is handled by
// writeChunkedTimeSubsSegment instead.
func writeTimeSubsMediaSegment(w http.ResponseWriter, cfg *ResponseConfig, a *asset, segmentPart string, nowMS int,
	tt *template.Template, isLast bool) (bool, error) {
	tss, isTimeSubs, err := matchTimeSubsMediaSegment(cfg, a, segmentPart, nowMS)
	if !isTimeSubs {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	mediaSeg, err := createTimeSubsMediaSegment(tss, cfg, tt)
	if err != nil {
		return true, fmt.Errorf("createTimeSubsMediaSegment: %w", err)
	}
	if isLast {
		mediaSeg.Styp.AddCompatibleBrands([]string{"lmsg"})
	}
	length := int(mediaSeg.Size())
	w.Header().Set("Content-Type", "application/mp4")
	w.Header().Set("Content-Length", strconv.Itoa(length))
	sw := bits.NewFixedSliceWriter(length)
	err = mediaSeg.EncodeSW(sw)
	if err != nil {
		slog.Error("generate media segment", "error", err)
		return true, fmt.Errorf("mediaSegGen: %w", err)
	}
	_, err = w.Write(sw.Bytes())
	if err != nil {
		slog.Error("write media segment response", "error", err)
		return true, fmt.Errorf("mediaSegSend: %w", err)
	}
	return true, nil
}

// createTimeSubsMediaSegment creates a complete time subtitle segment with one fragment.
func createTimeSubsMediaSegment(tss timeSubsSeg, cfg *ResponseConfig, tt *template.Template) (*mp4.MediaSegment, error) {
	seg := mp4.NewMediaSegment()
	frag, err := mp4.CreateFragment(tss.nr, subsTrackID)
	if err != nil {
		return nil, err
	}
	seg.AddFragment(frag)
	cues := calcCueItvls(tss.startMS, tss.durMS, tss.utcStartMS, cfg.TimeSubsDurMS)
	samples, err := timeSubsSamples(tss, cues, tss.startMS, tss.endMS(), cfg, tt)
	if err != nil {
		return nil, err
	}
	for _, s := range samples {
		frag.AddFullSample(s)
	}
	return seg, nil
}

// genTimeSubsChunks splits a generated time subtitle segment into chunks with the same
// duration as the video chunks, so that subtitles can be delivered at the same low
// latency as the media they belong to. The last chunk is shorter if the segment duration
// is not a multiple of the chunk duration.
//
// Samples that restate what the preceding sample already said are byte-identical (see
// timeSubsSamples) and are marked with redundantSampleFlags. The first sample of the
// segment is never marked, since it is what a client tuning in at the segment boundary
// has to decode.
func genTimeSubsChunks(tss timeSubsSeg, cfg *ResponseConfig, tt *template.Template, isLast bool) ([]chunk, error) {
	if cfg.ChunkDurS == nil || *cfg.ChunkDurS <= 0 {
		return nil, fmt.Errorf("chunking requested but no chunk duration configured")
	}
	chunkDurMS := int(math.Round(*cfg.ChunkDurS * SUBS_TIME_TIMESCALE))
	if chunkDurMS <= 0 {
		return nil, fmt.Errorf("chunk duration %.3fs is shorter than 1ms", *cfg.ChunkDurS)
	}
	if chunkDurMS > tss.durMS {
		chunkDurMS = tss.durMS
	}
	cues := calcCueItvls(tss.startMS, tss.durMS, tss.utcStartMS, cfg.TimeSubsDurMS)
	nrChunks := (tss.durMS + chunkDurMS - 1) / chunkDurMS
	chunks := make([]chunk, 0, nrChunks)
	var prevData []byte
	for i := range nrChunks {
		start := tss.startMS + i*chunkDurMS
		end := min(start+chunkDurMS, tss.endMS())
		var styp *mp4.StypBox
		if i == 0 {
			styp = mp4.CreateStyp()
			if isLast {
				styp.AddCompatibleBrands([]string{"lmsg"})
			}
		}
		chk := createChunk(styp, subsTrackID, tss.nr)
		samples, err := timeSubsSamples(tss, cues, start, end, cfg, tt)
		if err != nil {
			return nil, err
		}
		for _, s := range samples {
			if bytes.Equal(prevData, s.Data) {
				s.Flags = redundantSampleFlags
			}
			prevData = s.Data
			chk.frag.AddFullSample(s)
		}
		chk.dur = uint64(end - start)
		chunks = append(chunks, chk)
	}
	return chunks, nil
}

// timeSubsSamples returns the samples covering [startMS, endMS) of the segment described
// by tss. The interval is either the whole segment or one chunk of it.
func timeSubsSamples(tss timeSubsSeg, cues []cueItvl, startMS, endMS int, cfg *ResponseConfig,
	tt *template.Template) ([]mp4.FullSample, error) {
	switch tss.prefix {
	case SUBS_STPP_PREFIX:
		s, err := stppTimeSample(tt, cues, startMS, endMS, tss.lang, tss.nr, cfg.TimeSubsRegion)
		if err != nil {
			return nil, err
		}
		return []mp4.FullSample{s}, nil
	default: // SUBS_WVTT_PREFIX
		return wvttTimeSamples(cues, startMS, endMS, tss.lang, tss.nr, cfg.TimeSubsRegion), nil
	}
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
// startMS and endMS are the true times of the cue and are not clipped to the
// segment or fragment that carries it.
type cueItvl struct {
	startMS, endMS, utcS int
}

// overlaps tells whether the cue is on screen at some point during [startMS, endMS).
func (c cueItvl) overlaps(startMS, endMS int) bool {
	return c.startMS < endMS && c.endMS > startMS
}

// calcCueItvls returns all cue intervals that overlap the interval of length durMS
// starting at media time startMS, which corresponds to UTC time utcStartMS.
// All times are in milliseconds.
//
// The cues repeat with a period of cueDurMS rounded up to a full second, and each starts
// on a UTC second that is a multiple of that period. The returned times are the true
// times of the cues: a cue that starts before startMS or ends after startMS+durMS keeps
// its own begin and end, and it is up to the caller to decide what to write out.
func calcCueItvls(startMS, durMS, utcStartMS, cueDurMS int) []cueItvl {
	itvls := make([]cueItvl, 0, 2)

	diff := startMS - utcStartMS
	utcEndMS := utcStartMS + durMS

	cueFullS := int(math.Ceil(float64(cueDurMS) * 0.001))
	if cueFullS <= 0 {
		return itvls
	}
	cueFullMS := cueFullS * 1000

	// First cue period starting at or before the start of the interval.
	firstS := utcStartMS / cueFullMS * cueFullS
	for utcS := firstS; utcS*1000 < utcEndMS; utcS += cueFullS {
		ci := cueItvl{
			utcS:    utcS,
			startMS: utcS*1000 + diff,
			endMS:   utcS*1000 + cueDurMS + diff,
		}
		if !ci.overlaps(startMS, startMS+durMS) {
			continue
		}
		itvls = append(itvls, ci)
	}
	return itvls
}

// stppTimeSample renders the TTML document that covers [startMS, endMS) and returns it
// as one sample with that duration.
//
// Following the paint model, a cue keeps its true begin time even when that lies before
// the start of the fragment (allowed by ISO/IEC 14496-30 Sec. 5.9(1), and named as the same
// element recurring in adjacent samples in Sec. 5.9(2)), and is given an end time only in the
// fragment where it ends. An unchanged cue is therefore restated
// byte for byte, which is what lets genTimeSubsChunks mark the restatement as redundant.
func stppTimeSample(tt *template.Template, cues []cueItvl, startMS, endMS int, lang string, nr uint32,
	region int) (mp4.FullSample, error) {
	stppd := StppTimeData{
		Lang:   lang,
		Region: region,
		Cues:   make([]StppTimeCue, 0, len(cues)),
	}
	for i, ci := range cues {
		if !ci.overlaps(startMS, endMS) {
			continue
		}
		cue := StppTimeCue{
			Id:    fmt.Sprintf("%d-%d", nr, i),
			Begin: msToTTMLTime(ci.startMS),
			Msg:   makeStppMessage(lang, ci.utcS*1000, int(nr)),
		}
		if ci.endMS <= endMS {
			cue.End = msToTTMLTime(ci.endMS)
		}
		stppd.Cues = append(stppd.Cues, cue)
	}
	data := make([]byte, 0, 1024)
	buf := bytes.NewBuffer(data)
	err := tt.ExecuteTemplate(buf, "stpptime.xml", stppd)
	if err != nil {
		return mp4.FullSample{}, fmt.Errorf("execute stpp template: %w", err)
	}
	sampleData := buf.Bytes()
	return mp4.FullSample{
		Sample: mp4.Sample{
			Flags: mp4.SyncSampleFlags,
			Dur:   uint32(endMS - startMS),
			Size:  uint32(len(sampleData)),
		},
		DecodeTime: uint64(startMS),
		Data:       sampleData,
	}, nil
}

func rep2SubsTime(repTime uint64, timescale int) uint64 {
	return uint64(math.Round(float64(repTime*SUBS_TIME_TIMESCALE) / float64(timescale)))
}
