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
	"strconv"
	"text/template"
	"time"

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
	seg, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return sd, fmt.Errorf("mp4Decode: %w", err)
	}

	timeShift := rawSeg.meta.newTime - seg.Segments[0].Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
	for _, frag := range seg.Segments[0].Fragments {
		frag.Moof.Mfhd.SequenceNumber = rawSeg.meta.newNr
		oldTime := frag.Moof.Traf.Tfdt.BaseMediaDecodeTime()
		frag.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(oldTime + timeShift)
	}

	// TODO in future.
	// In case of stpp subtitles, we need to shift the time stamps inside the segment with
	// the same amount as we shift the segment.

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

// findSegMetaFromTime finds the proper segMeta if media time is OK.
// Otherwise error message like TooEarly or Gone.
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
	if idx == len(rep.segments) {
		return segMeta{}, fmt.Errorf("no matching segment")
	}
	seg := rep.segments[idx]
	if seg.startTime != uint64(timeAfterWrap) {
		return segMeta{}, fmt.Errorf("segment time mismatch %d <-> %d", timeAfterWrap, seg.startTime)
	}

	// Check interval validity
	segAvailTimeS := float64(int(seg.endTime)+wrapTime+mediaRef) / float64(rep.MediaTimescale)
	nowS := float64(nowMS) * 0.001
	err := CheckTimeValidity(segAvailTimeS, nowS, float64(*cfg.TimeShiftBufferDepthS), cfg.getAvailabilityTimeOffsetS())
	if err != nil {
		return segMeta{}, err
	}

	return segMeta{
		rep:       rep,
		origTime:  seg.startTime,
		newTime:   time,
		origNr:    seg.nr,
		newNr:     uint32(cfg.getStartNr() + idx + nrWraps*len(rep.segments)),
		origDur:   uint32(seg.endTime - seg.startTime),
		newDur:    uint32(seg.endTime - seg.startTime),
		timescale: uint32(rep.MediaTimescale),
	}, nil
}

// CheckTimeValidity checks if availTimeS is a valid time given current time and parameters.
// Returns errors if too early, or too late. availabilityTimeOffset < 0 signals always available.
func CheckTimeValidity(availTimeS, nowS, timeShiftBufferDepthS, availabilityTimeOffsetS float64) error {
	if availabilityTimeOffsetS < 0 {
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
	wrapLen := len(rep.segments)
	startNr := cfg.getStartNr()
	nrWraps := (int(nr) - startNr) / wrapLen
	relNr := int(nr) - nrWraps*wrapLen
	wrapDur := a.LoopDurMS * rep.MediaTimescale / 1000
	wrapTime := nrWraps * wrapDur
	seg := rep.segments[relNr]
	segTime := wrapTime + int(seg.startTime)
	mediaRef := cfg.StartTimeS * rep.MediaTimescale // TODO. Add period offset

	// Check interval validity
	segAvailTimeS := float64(int(seg.endTime)+wrapTime+mediaRef) / float64(rep.MediaTimescale)
	nowS := float64(nowMS) * 0.001
	err := CheckTimeValidity(segAvailTimeS, nowS, float64(*cfg.TimeShiftBufferDepthS), cfg.getAvailabilityTimeOffsetS())
	if err != nil {
		return segMeta{}, err
	}

	return segMeta{
		rep:       rep,
		origTime:  seg.startTime,
		newTime:   uint64(segTime),
		origNr:    seg.nr,
		newNr:     nr,
		origDur:   uint32(seg.endTime - seg.startTime),
		newDur:    uint32(seg.endTime - seg.startTime),
		timescale: uint32(rep.MediaTimescale),
	}, nil
}

func writeInitSegment(w http.ResponseWriter, cfg *ResponseConfig, vodFS fs.FS, a *asset, segmentPart string) (isInit bool, err error) {
	isTimeSubsInit, err := writeTimeSubsInitSegment(w, cfg, a, segmentPart)
	if isTimeSubsInit {
		return true, err
	}
	for _, rep := range a.Reps {
		if segmentPart == rep.initURI {
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
			return r, fmt.Errorf("unknown liveMPDtype")
		}
		if err != nil {
			return r, err
		}
		segPath = path.Join(a.AssetPath, replaceTimeAndNr(rep.mediaURI, r.meta.origTime, r.meta.origNr))
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

	// Some part of the segment should be available, so we need to deliver that part and then return the
	// rest as time passes.
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
