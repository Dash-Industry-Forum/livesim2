// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type segData struct {
	data        []byte
	contentType string
}

// adjustLiveSegment adjusts a VoD segment to live parameters dependent on configuration.
func adjustLiveSegment(vodFS fs.FS, a *asset, cfg *ResponseConfig, segmentPart string, nowMS int) (segData, error) {
	var sd segData
	rawSeg, err := findRawSeg(vodFS, a, cfg, segmentPart, nowMS)
	if err != nil {
		return sd, fmt.Errorf("findMediaSegment: %w", err)
	}
	if isImage(segmentPart) {
		return segData{rawSeg.data, rawSeg.meta.rep.SegmentType()}, nil
	}
	sr := bits.NewFixedSliceReader(rawSeg.data)
	segFile, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return sd, fmt.Errorf("mp4Decode: %w", err)
	}

	if len(segFile.Segments) != 1 {
		return sd, fmt.Errorf("not 1 but %d segments", len(segFile.Segments))
	}
	seg := segFile.Segments[0]
	timeShift := rawSeg.meta.newTime - seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
	if strings.HasPrefix(rawSeg.meta.rep.Codecs, "stpp") {
		// Shift segment and TTML timestamps inside segment
		err = shiftStppTimes(seg, rawSeg.meta.timescale, timeShift, rawSeg.meta.newNr)
		if err != nil {
			return sd, fmt.Errorf("shiftStppTimes: %w", err)
		}
	} else {
		for _, frag := range seg.Fragments {
			frag.Moof.Mfhd.SequenceNumber = rawSeg.meta.newNr
			oldTime := frag.Moof.Traf.Tfdt.BaseMediaDecodeTime()
			frag.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(oldTime + timeShift)
		}
	}

	if cfg.SCTE35PerMinute != nil && rawSeg.meta.rep.ContentType == "video" {
		startTime := uint64(rawSeg.meta.newTime)
		endTime := startTime + uint64(rawSeg.meta.newDur)
		timescale := uint64(rawSeg.meta.timescale)
		emsg, err := scte35.CreateEmsgAhead(startTime, endTime, timescale, *cfg.SCTE35PerMinute)
		if err != nil {
			return sd, fmt.Errorf("insertSCTE35: %w", err)
		}
		if emsg != nil {
			seg.Fragments[0].AddEmsg(emsg)
			log.Debug().Str("asset", a.AssetPath).Str("segment", segmentPart).Msg("added SCTE-35 emsg message")
		}
	}

	out := make([]byte, seg.Size())

	sw := bits.NewFixedSliceWriterFromSlice(out)
	err = seg.EncodeSW(sw)
	if err != nil {
		return sd, fmt.Errorf("mp4Encode: %w", err)
	}
	return segData{sw.Bytes(), rawSeg.meta.rep.SegmentType()}, nil
}

func isImage(segPath string) bool {
	return path.Ext(segPath) == ".jpg"
}

// segMeta provides meta data information about a segment
type segMeta struct {
	rep       *RepData
	origTime  uint64
	newTime   uint64
	origNr    uint32
	newNr     uint32
	origDur   uint32
	newDur    uint32
	timescale uint32
}

// findSegMetaFromTime finds the proper segMeta if media time is OK, or returns error.
// Time-related errors are TooEarly or Gone.
// time is measured relative to period start + presentationTimeOffset (PTO).
// Period start is in turn relative to startTime (availabilityStartTime)
// For now, period and PTO are both zero, but the startTime may be non-zero.
func findSegMetaFromTime(a *asset, rep *RepData, time uint64, cfg *ResponseConfig, nowMS int) (segMeta, error) {
	mediaRef := cfg.StartTimeS * rep.MediaTimescale // TODO. Add period + PTO
	wrapDur := a.LoopDurMS * rep.MediaTimescale / 1000
	nrWraps := int(time) / wrapDur
	wrapTime := nrWraps * wrapDur
	timeAfterWrap := int(time) - wrapTime
	idx := rep.findSegmentIndexFromTime(uint64(timeAfterWrap))
	if idx == len(rep.Segments) {
		return segMeta{}, fmt.Errorf("no matching segment")
	}
	seg := rep.Segments[idx]
	if seg.StartTime != uint64(timeAfterWrap) {
		return segMeta{}, fmt.Errorf("segment time mismatch %d <-> %d", timeAfterWrap, seg.StartTime)
	}

	// Check interval validity
	segAvailTimeS := float64(int(seg.EndTime)+wrapTime+mediaRef) / float64(rep.MediaTimescale)
	nowS := float64(nowMS) * 0.001
	err := CheckTimeValidity(segAvailTimeS, nowS, float64(*cfg.TimeShiftBufferDepthS), cfg.getAvailabilityTimeOffsetS())
	if err != nil {
		return segMeta{}, err
	}

	return segMeta{
		rep:       rep,
		origTime:  seg.StartTime,
		newTime:   time,
		origNr:    seg.Nr,
		newNr:     uint32(cfg.getStartNr() + idx + nrWraps*len(rep.Segments)),
		origDur:   uint32(seg.EndTime - seg.StartTime),
		newDur:    uint32(seg.EndTime - seg.StartTime),
		timescale: uint32(rep.MediaTimescale),
	}, nil
}

// CheckTimeValidity checks if availTimeS is a valid time given current time and parameters.
// Returns errors if too early, or too late. availabilityTimeOffset < 0 signals always available.
func CheckTimeValidity(availTimeS, nowS, timeShiftBufferDepthS, availabilityTimeOffsetS float64) error {
	if availabilityTimeOffsetS == +math.Inf(1) {
		return nil // Infinite availability time offset
	}

	// Valid interval [nowRel-cfg.tsbd, nowRel) where end-time must be used
	if availabilityTimeOffsetS > 0 {
		availTimeS -= availabilityTimeOffsetS
	}
	if availTimeS > nowS {
		return newErrTooEarly(int(math.Round((availTimeS - nowS) * 1000.0)))
	}
	if availTimeS < nowS-(timeShiftBufferDepthS+timeShiftBufferDepthMarginS) {
		return errGone
	}
	return nil
}

// findSegMetaFromNr returns segMeta if segment is available.
func findSegMetaFromNr(a *asset, rep *RepData, nr uint32, cfg *ResponseConfig, nowMS int) (segMeta, error) {
	wrapLen := len(rep.Segments)
	startNr := cfg.getStartNr()
	nrWraps := (int(nr) - startNr) / wrapLen
	relNr := int(nr) - nrWraps*wrapLen
	wrapDur := a.LoopDurMS * rep.MediaTimescale / 1000
	wrapTime := nrWraps * wrapDur
	seg := rep.Segments[relNr]
	segTime := wrapTime + int(seg.StartTime)
	mediaRef := cfg.StartTimeS * rep.MediaTimescale // TODO. Add period offset

	// Check interval validity
	segAvailTimeS := float64(int(seg.EndTime)+wrapTime+mediaRef) / float64(rep.MediaTimescale)
	nowS := float64(nowMS) * 0.001
	err := CheckTimeValidity(segAvailTimeS, nowS, float64(*cfg.TimeShiftBufferDepthS), cfg.getAvailabilityTimeOffsetS())
	if err != nil {
		return segMeta{}, err
	}

	return segMeta{
		rep:       rep,
		origTime:  seg.StartTime,
		newTime:   uint64(segTime),
		origNr:    seg.Nr,
		newNr:     nr,
		origDur:   uint32(seg.EndTime - seg.StartTime),
		newDur:    uint32(seg.EndTime - seg.StartTime),
		timescale: uint32(rep.MediaTimescale),
	}, nil
}

func writeInitSegment(w http.ResponseWriter, cfg *ResponseConfig, vodFS fs.FS, a *asset, segmentPart string) (isInit bool, err error) {
	isTimeSubsInit, err := writeTimeSubsInitSegment(w, cfg, a, segmentPart)
	if isTimeSubsInit {
		return true, err
	}
	for _, rep := range a.Reps {
		if segmentPart == rep.InitURI {
			w.Header().Set("Content-Length", strconv.Itoa(len(rep.initBytes)))

			w.Header().Set("Content-Type", rep.SegmentType())
			_, err := w.Write(rep.initBytes)
			if err != nil {
				log.Error().Err(err).Msg("writing response")
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

func writeLiveSegment(w http.ResponseWriter, cfg *ResponseConfig, vodFS fs.FS, a *asset, segmentPart string, nowMS int, tt *template.Template) error {
	isTimeSubsMedia, err := writeTimeSubsMediaSegment(w, cfg, a, segmentPart, nowMS, tt)
	if isTimeSubsMedia {
		return err
	}
	segData, err := adjustLiveSegment(vodFS, a, cfg, segmentPart, nowMS)
	if err != nil {
		return fmt.Errorf("convertToLive: %w", err)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(segData.data)))
	w.Header().Set("Content-Type", segData.contentType)
	_, err = w.Write(segData.data)
	if err != nil {
		log.Error().Err(err).Msg("write init response")
		return err
	}
	return nil
}

type rawSeg struct {
	data []byte
	meta segMeta
}

func findRawSeg(vodFS fs.FS, a *asset, cfg *ResponseConfig, segmentPart string, nowMS int) (rawSeg, error) {
	var r rawSeg
	var segPath string
	for _, rep := range a.Reps {
		mParts := rep.mediaRegexp.FindStringSubmatch(segmentPart)
		if mParts == nil {
			continue
		}
		if len(mParts) != 2 {
			return r, fmt.Errorf("bad segment match")
		}
		idNr, err := strconv.Atoi(mParts[1])
		if err != nil {
			return r, err
		}

		switch cfg.getRepType(segmentPart) {
		case segmentNumber, timeLineNumber:
			nr := uint32(idNr)
			r.meta, err = findSegMetaFromNr(a, rep, nr, cfg, nowMS)
		case timeLineTime:
			time := uint64(idNr)
			r.meta, err = findSegMetaFromTime(a, rep, time, cfg, nowMS)
		default:
			return r, fmt.Errorf("unknown liveMPD type")
		}
		if err != nil {
			return r, err
		}
		segPath = path.Join(a.AssetPath, replaceTimeAndNr(rep.MediaURI, r.meta.origTime, r.meta.origNr))
		break // segPath found
	}
	if segPath == "" {
		return r, errNotFound
	}
	var err error
	r.data, err = fs.ReadFile(vodFS, segPath)
	if err != nil {
		return r, fmt.Errorf("read segment: %w", err)
	}
	return r, nil
}

// writeChunkedSegment splits a segment into chunks and send them as they become available timewise.
//
// nowMS servers as reference for the current time and can be set to any value. Media time will
// be incremented with respect to nowMS.
func writeChunkedSegment(ctx context.Context, w http.ResponseWriter, log *zerolog.Logger, cfg *ResponseConfig,
	vodFS fs.FS, a *asset, segmentPart string, nowMS int) error {

	// Need initial segment meta data
	rawSeg, err := findRawSeg(vodFS, a, cfg, segmentPart, nowMS)
	if err != nil {
		return fmt.Errorf("findSegment: %w", err)
	}
	meta := rawSeg.meta
	w.Header().Set("Content-Type", meta.rep.SegmentType())
	if isImage(segmentPart) {
		w.Header().Set("Content-Length", strconv.Itoa(len(rawSeg.data)))
		_, err = w.Write(rawSeg.data)
		return fmt.Errorf("could not write image segment: %w", err)
	}

	sr := bits.NewFixedSliceReader(rawSeg.data)
	seg, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return fmt.Errorf("mp4Decode: %w", err)
	}

	// Some part of the segment should be available, and is delivered directly.
	// The rest are returned HTTP chunks as time passes.
	// In general, we should extract all the samples and build a new one with the right fragment duration.
	// That fragment/chunk duration is segment_duration-availabilityTimeOffset.
	chunkDur := (a.SegmentDurMS - int(cfg.AvailabilityTimeOffsetS*1000)) * int(meta.timescale) / 1000
	chunks, err := chunkSegment(meta.rep.initSeg, seg, meta, chunkDur)
	if err != nil {
		return fmt.Errorf("chunkSegment: %w", err)
	}
	startUnixMS := unixMS()
	chunkAvailTime := int(meta.newTime) + cfg.StartTimeS*int(meta.timescale)
	for _, chk := range chunks {
		chunkAvailTime += int(chk.dur)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		chunkAvailMS := chunkAvailTime * 1000 / int(meta.timescale)
		if chunkAvailMS < nowMS {
			err = writeChunk(w, chk)
			if err != nil {
				return fmt.Errorf("writeChunk: %w", err)
			}
			continue
		}
		nowUpdateMS := unixMS() - startUnixMS + nowMS
		if chunkAvailMS < nowUpdateMS {
			err = writeChunk(w, chk)
			if err != nil {
				return fmt.Errorf("writeChunk: %w", err)
			}
			continue
		}
		sleepMS := chunkAvailMS - nowUpdateMS
		time.Sleep(time.Duration(sleepMS * 1_000_000))
		err = writeChunk(w, chk)
		if err != nil {
			return fmt.Errorf("writeChunk: %w", err)
		}
	}
	return nil
}

func unixMS() int {
	return int(time.Now().UnixMilli())
}

type chunk struct {
	styp *mp4.StypBox
	frag *mp4.Fragment
	dur  uint64 // in media timescale
}

func createChunk(styp *mp4.StypBox, trackID, seqNr uint32) chunk {
	frag, _ := mp4.CreateFragment(seqNr, trackID)
	return chunk{
		styp: styp,
		frag: frag,
		dur:  0,
	}
}

// chunkSegment splits a segment into chunks of specified duration.
// The first chunk gets an styp box if one is available in the incoming segment.
func chunkSegment(init *mp4.InitSegment, seg *mp4.File, segMeta segMeta, chunkDur int) ([]chunk, error) {
	if len(seg.Segments) != 1 {
		return nil, fmt.Errorf("not 1 but %d segments", len(seg.Segments))
	}
	trex := init.Moov.Mvex.Trex
	s := seg.Segments[0]
	fs := make([]mp4.FullSample, 0, 32)
	for _, f := range s.Fragments {
		ff, err := f.GetFullSamples(trex)
		if err != nil {
			return nil, err
		}
		fs = append(fs, ff...)
	}
	chunks := make([]chunk, 0, segMeta.newDur/uint32(chunkDur))
	trackID := init.Moov.Trak.Tkhd.TrackID
	ch := createChunk(s.Styp, trackID, segMeta.newNr)
	chunkNr := 1
	var accChunkDur uint32 = 0
	var totalDur int = 0
	sampleDecodeTime := segMeta.newTime
	var thisChunkDur uint32 = 0
	for i := range fs {
		fs[i].DecodeTime = sampleDecodeTime
		ch.frag.AddFullSample(fs[i])
		dur := fs[i].Dur
		sampleDecodeTime += uint64(dur)
		accChunkDur += dur
		thisChunkDur += dur
		totalDur += int(dur)
		if totalDur >= chunkDur*chunkNr {
			ch.dur = uint64(thisChunkDur)
			chunks = append(chunks, ch)
			ch = createChunk(nil, trackID, segMeta.newNr)
			thisChunkDur = 0
			chunkNr++
		}
	}
	if thisChunkDur > 0 {
		ch.dur = uint64(chunkDur)
		chunks = append(chunks, ch)
	}

	return chunks, nil
}

func writeChunk(w http.ResponseWriter, chk chunk) error {
	if chk.styp != nil {
		err := chk.styp.Encode(w)
		if err != nil {
			return err
		}
	}
	err := chk.frag.Encode(w)
	if err != nil {
		return err
	}
	flusher := w.(http.Flusher)
	flusher.Flush()
	return nil
}

// Ptr returns a pointer to a value of any type
func Ptr[T any](v T) *T {
	return &v
}

// shiftStppTime shifts the baseMediaDecodeTime and the TTML timestamps in an stpp segment.
// Both stpp text and stpp image with embedded images as sub samples are supported.
// Note that other "timestamps" that matches the pattern hh:mm:ss[.mmm] will also be shifted.
func shiftStppTimes(seg *mp4.MediaSegment, timescale uint32, timeShift uint64, newNr uint32) error {
	nrFrags := len(seg.Fragments)
	if nrFrags != 1 {
		return fmt.Errorf("not 1 but %d fragments", nrFrags)
	}
	frag := seg.Fragments[0]
	frag.Moof.Mfhd.SequenceNumber = newNr
	traf := frag.Moof.Traf
	oldTime := traf.Tfdt.BaseMediaDecodeTime()
	traf.Tfdt.SetBaseMediaDecodeTime(oldTime + timeShift)
	samples, err := frag.GetFullSamples(nil)
	if err != nil {
		return fmt.Errorf("getFullSamples: %w", err)
	}
	if len(samples) != 1 {
		return fmt.Errorf("not 1 but %d samples in stpp file", len(samples))
	}
	data := samples[0].Data
	timeShiftMS := uint64(math.Round(float64(timeShift) / float64(timescale) * 1000.0))
	var subs *mp4.SubsBox
	for _, c := range traf.Children {
		if c.Type() == "subs" {
			subs = c.(*mp4.SubsBox)
		}
	}
	var newData []byte
	if subs != nil {
		if len(subs.Entries) != 1 {
			return fmt.Errorf("not 1 but %d subs entries in stpp file", len(subs.Entries))
		}
		ttmlSize := subs.Entries[0].SubSamples[0].SubsampleSize
		ttmlData := data[:ttmlSize]
		newTTMLData, err := shiftTTMLTimestamps(ttmlData, timeShiftMS)
		if err != nil {
			return fmt.Errorf("shiftTTMLTimestamps: %w", err)
		}
		subs.Entries[0].SubSamples[0].SubsampleSize = uint32(len(newTTMLData))
		newData = append(newTTMLData, data[ttmlSize:]...)
	} else {
		newData, err = shiftTTMLTimestamps(data, timeShiftMS)
		if err != nil {
			return fmt.Errorf("shiftTTMLTimestamps: %w", err)
		}
	}
	newSize := uint32(len(newData))
	tfhd := frag.Moof.Traf.Tfhd
	if tfhd.HasDefaultSampleSize() {
		tfhd.DefaultSampleSize = newSize
	}
	trun := frag.Moof.Traf.Trun
	if trun.HasSampleSize() {
		trun.Samples[0].Size = newSize
	}
	mdat := frag.Mdat
	mdat.Data = []byte(newData)
	return nil
}

var timeExp = regexp.MustCompile(`(?P<hours>\d\d+):(?P<minutes>\d\d):(?P<seconds>\d\d)(?P<milliseconds>\.\d\d\d)?`)

// shiftTTMLTimestamps shifts the begin and end timestamps in a TTML file.
func shiftTTMLTimestamps(data []byte, timeShiftMS uint64) ([]byte, error) {
	str := string(data)
	idxMatches := timeExp.FindAllStringIndex(str, -1)
	if len(idxMatches) == 0 {
		return data, nil
	}
	b := strings.Builder{}
	b.WriteString(str[:idxMatches[0][0]])
	for i, idxPair := range idxMatches {
		if i > 0 {
			b.WriteString(str[idxMatches[i-1][1]:idxPair[0]])
		}
		newTimestamp := shiftTimestamp(str[idxPair[0]:idxPair[1]], timeShiftMS)
		b.WriteString(newTimestamp)
	}
	b.WriteString(str[idxMatches[len(idxMatches)-1][1]:])
	return []byte(b.String()), nil
}

// shiftTimestamp shifts a timestamp of the form hh:mm:ss.mmm by milliseconds.
// The millisecond part of timestamp is optional, but will always be present in the output.
func shiftTimestamp(timestamp string, timeshiftMS uint64) string {
	match := timeExp.FindStringSubmatch(timestamp)
	hours, _ := strconv.Atoi(match[1])
	minutes, _ := strconv.Atoi(match[2])
	seconds, _ := strconv.Atoi(match[3])
	milliseconds := 0
	if match[4] != "" {
		milliseconds, _ = strconv.Atoi(match[4][1:]) // Skip the fraction "." start
	}
	totalMS := uint64(hours)*3600000 + uint64(minutes)*60000 + uint64(seconds)*1000 + uint64(milliseconds)
	newTotalMS := totalMS + timeshiftMS
	newHours := newTotalMS / 3600000
	newMinutes := (newTotalMS % 3600000) / 60000
	newSeconds := (newTotalMS % 60000) / 1000
	newMilliseconds := newTotalMS % 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", newHours, newMinutes, newSeconds, newMilliseconds)
}
