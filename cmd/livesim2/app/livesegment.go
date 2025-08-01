// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// genLiveSegment generates a live segment from one or more VoD segments following cfg and media type
// isLast triggers insertion of lmsg compatibility brand
func genLiveSegment(log *slog.Logger, vodFS fs.FS, a *asset, cfg *ResponseConfig,
	segmentPart string, nowMS int, isLast bool) (segOut, error) {
	var so segOut

	outSeg, err := createOutSeg(vodFS, a, cfg, segmentPart, nowMS)
	if err != nil {
		return so, fmt.Errorf("createOutSeg: %w", err)
	}
	if isImage(segmentPart) {
		return outSeg, nil
	}
	if outSeg.data != nil { // Non-processed mp4-file
		meta := outSeg.meta
		contentType := meta.rep.ContentType
		sr := bits.NewFixedSliceReader(outSeg.data)
		segFile, err := mp4.DecodeFileSR(sr)
		if err != nil {
			return so, fmt.Errorf("mp4Decode: %w", err)
		}
		if len(segFile.Segments) != 1 {
			return so, fmt.Errorf("not 1 but %d segments", len(segFile.Segments))
		}
		seg := segFile.Segments[0]
		if seg.Sidx != nil {
			if len(seg.Sidxs) > 1 {
				log.Error("more than one sidx not supported", "asset", a.AssetPath, "segment", segmentPart)
				return so, fmt.Errorf("more than one sidx not supported")
			}
			if seg.Sidx.Timescale != meta.timescale {
				seg.Sidx.Timescale = meta.timescale
			}
			seg.Sidx.EarliestPresentationTime = meta.newTime
		}
		timeShift := meta.newTime - seg.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
		if strings.HasPrefix(meta.rep.Codecs, "stpp") {
			// Shift segment and TTML timestamps inside segment
			err = shiftStppTimes(seg, meta.timescale, timeShift, meta.newNr)
			if err != nil {
				return so, fmt.Errorf("shiftStppTimes: %w", err)
			}
		} else {
			for _, frag := range seg.Fragments {
				traf := frag.Moof.Traf
				tfdt := traf.Tfdt
				oldTfdtSize := tfdt.Size()
				frag.Moof.Mfhd.SequenceNumber = meta.newNr
				oldTime := tfdt.BaseMediaDecodeTime()
				tfdt.SetBaseMediaDecodeTime(oldTime + timeShift)
				newTfdtSize := tfdt.Size()
				tfdtSizeDiff := int32(newTfdtSize) - int32(oldTfdtSize)
				if tfdtSizeDiff != 0 {
					traf.Trun.DataOffset += tfdtSizeDiff
					frag.Mdat.StartPos += uint64(tfdtSizeDiff)
				}
				if traf.Saio != nil {
					if saioAfterTfdt(traf) {
						for i := range traf.Saio.Offset {
							traf.Saio.Offset[i] += int64(tfdtSizeDiff)
						}
					}
				}
			}
		}

		if cfg.SCTE35PerMinute != nil && contentType == "video" {
			startTime := uint64(meta.newTime)
			endTime := startTime + uint64(meta.newDur)
			timescale := uint64(meta.timescale)
			emsg, err := scte35.CreateEmsgAhead(startTime, endTime, timescale, *cfg.SCTE35PerMinute)
			if err != nil {
				return so, fmt.Errorf("insertSCTE35: %w", err)
			}
			if emsg != nil {
				seg.Fragments[0].AddEmsg(emsg)
				log.Debug("added SCTE-35 emsg message", "asset", a.AssetPath, "segment", segmentPart)
			}
		}
		outSeg.seg = seg
		outSeg.data = nil
	}
	if isLast && outSeg.seg.Styp != nil {
		outSeg.seg.Styp.AddCompatibleBrands([]string{"lmsg"})
	}
	return outSeg, nil
}

// saioAfterTfdt saio box comes after tfdt in traf
func saioAfterTfdt(traf *mp4.TrafBox) bool {
	tfdtIndex := -1
	saioIndex := -1
	for i, c := range traf.Children {
		switch c.Type() {
		case "saio":
			saioIndex = i
		case "tfdt":
			tfdtIndex = i
		}
	}
	if tfdtIndex == -1 || saioIndex == -1 {
		return false
	}
	return saioIndex > tfdtIndex
}

func isImage(segPath string) bool {
	return path.Ext(segPath) == ".jpg"
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

// segMeta provides meta data information about a segment.
// For audio it may be a combination of several segments and sampleNrOffset may be non-zero.
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

// findRefSegMetaFromTime finds the proper segMeta if media time is OK for rep, or returns error.
func findRefSegMetaFromTime(a *asset, rep *RepData, time uint64, cfg *ResponseConfig, nowMS int) (segMeta, error) {
	var sm segMeta
	if rep.ConstantSampleDuration == nil || *rep.ConstantSampleDuration == 0 {
		return sm, fmt.Errorf("no constant sample duration")
	}
	sampleDur := *rep.ConstantSampleDuration
	if time%uint64(sampleDur) != 0 {
		return sm, fmt.Errorf("time must be multiple of sample duration")
	}
	refRep := a.refRep
	refTotDur := uint64(refRep.duration())
	nrSegs := uint64(len(refRep.Segments))
	refTime := time * uint64(refRep.MediaTimescale) / uint64(rep.MediaTimescale)
	nrWraps := refTime / refTotDur
	wrapTime := nrWraps * refTotDur
	wrapNr := nrWraps * nrSegs
	refTimeAfterWrap := refTime - nrWraps*refTotDur
	var refStartTime, refEndTime uint64
	var refOutNr uint32
	relNr := refTimeAfterWrap / refTotDur
	for {
		endTime := refRep.Segments[relNr].EndTime
		if endTime < refTimeAfterWrap || relNr == 0 {
			break
		}
		relNr--
	}
	for {
		seg := refRep.Segments[relNr]
		if seg.EndTime > refTimeAfterWrap {
			refOutNr = uint32(uint64(relNr)+wrapNr) + uint32(cfg.getStartNr())
			refStartTime = wrapTime + seg.StartTime
			refEndTime = wrapTime + seg.EndTime
			break
		}
		relNr++
	}
	if refEndTime == 0 {
		return sm, fmt.Errorf("no matching reference segment")
	}
	dur := uint32(refRep.Segments[relNr].EndTime - refRep.Segments[relNr].StartTime)

	// Check interval validity
	segAvailTimeS := float64(refEndTime) / float64(refRep.MediaTimescale)
	nowS := float64(nowMS) * 0.001
	err := CheckTimeValidity(segAvailTimeS, nowS, float64(*cfg.TimeShiftBufferDepthS), cfg.getAvailabilityTimeOffsetS())
	if err != nil {
		return segMeta{}, err
	}

	return segMeta{
		rep:       refRep,
		origTime:  refRep.Segments[relNr].StartTime,
		newTime:   refStartTime,
		origNr:    refRep.Segments[relNr].Nr,
		newNr:     refOutNr,
		origDur:   dur,
		newDur:    dur,
		timescale: uint32(refRep.MediaTimescale),
	}, nil
}

// calcSegmentAvailabilityTime calculates the availability time in ms for a segment given the segment number.
func calcSegmentAvailabilityTime(a *asset, rep *RepData, nr uint32, cfg *ResponseConfig) (int64, error) {
	wrapLen := len(rep.Segments)
	startNr := cfg.getStartNr()
	nrAfterStart := int(nr) - startNr
	nrWraps := nrAfterStart / wrapLen
	relNr := nrAfterStart - nrWraps*wrapLen
	wrapDur := a.LoopDurMS * rep.MediaTimescale / 1000
	wrapTime := nrWraps * wrapDur
	seg := rep.Segments[relNr]
	mediaRef := cfg.StartTimeS * rep.MediaTimescale // TODO. Add period offset

	// Check interval validity
	segAvailTimeS := float64(int(seg.EndTime)+wrapTime+mediaRef) / float64(rep.MediaTimescale)
	ato := cfg.getAvailabilityTimeOffsetS()
	if ato == +math.Inf(1) {
		return int64(cfg.StartTimeS) * 1000, nil
	}
	segAvailTimeS -= ato
	milliSeconds := int64(segAvailTimeS * 1_000)
	return milliSeconds, nil
}

// findSegMetaFromNr returns segMeta if segment is available.
func findSegMetaFromNr(a *asset, rep *RepData, nr uint32, cfg *ResponseConfig, nowMS int) (segMeta, error) {
	wrapLen := len(rep.Segments)
	startNr := cfg.getStartNr()
	nrAfterStart := int(nr) - startNr
	nrWraps := nrAfterStart / wrapLen
	relNr := nrAfterStart - nrWraps*wrapLen
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

type initMatch struct {
	isInit bool
	init   []byte
	rep    *RepData
}

func writeInitSegment(log *slog.Logger, w http.ResponseWriter, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	a *asset, segmentPart string) (isInit bool, err error) {
	isTimeSubsInit, err := writeTimeSubsInitSegment(w, cfg, segmentPart)
	if isTimeSubsInit {
		return true, err
	}
	match, err := matchInit(segmentPart, cfg, drmCfg, a)
	if err != nil {
		return false, fmt.Errorf("getInitBytes: %w", err)
	}
	if !match.isInit {
		return false, nil
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(match.init)))
	w.Header().Set("Content-Type", match.rep.SegmentType())
	_, err = w.Write(match.init)
	if err != nil {
		log.Error("writing response", "error", err)
		return false, err
	}
	return true, nil
}

func matchInit(segmentPart string, cfg *ResponseConfig, drmCfg *drm.DrmConfig, a *asset) (initMatch, error) {
	var im initMatch
	for _, rep := range a.Reps {
		if segmentPart == rep.InitURI {
			im.init = rep.initBytes
			if rep.encData == nil { // pre-encrypted or subtitle track
				im.isInit = true
				im.rep = rep
				return im, nil
			}
			if cfg.DRM != "" {
				switch cfg.DRM {
				case "eccp-cenc", "eccp-cbcs":
					scheme := strings.TrimPrefix(cfg.DRM, "eccp-")
					im.init = rep.encData.initEnc[scheme].initRaw
				default:
					// Here we should encrypt the raw init segment (and possibly add PSSH boxes)
					drmCfg, ok := drmCfg.Map[cfg.DRM]
					if !ok {
						return im, fmt.Errorf("drm configuration %q not found", cfg.DRM)
					}
					keyData, err := drmCfg.CPIXData.GetContentKey(rep.ContentType)
					if err != nil {
						return im, fmt.Errorf("get content key: %w", err)
					}
					scheme := keyData.CommonEncryptionScheme
					kid := sliceToId16(keyData.KeyID)
					iv := keyData.ExplicitIV
					_, initSeg, err := genEncInit(rep.initBytes, kid, iv, scheme)
					if err != nil {
						return im, fmt.Errorf("genEncInit: %w", err)
					}
					sw := bits.NewFixedSliceWriter(int(initSeg.Size()))
					err = initSeg.EncodeSW(sw)
					if err != nil {
						return im, fmt.Errorf("encode init segment: %w", err)
					}
					im.init = sw.Bytes()
				}
			}
			im.rep = rep
			im.isInit = true
			return im, nil
		}
	}
	return im, nil
}

func writeLiveSegment(log *slog.Logger, w http.ResponseWriter, cfg *ResponseConfig, drmCfg *drm.DrmConfig, vodFS fs.FS,
	a *asset, segmentPart string, nowMS int, tt *template.Template, isLast bool) error {
	log.Debug("writeLiveSegment", "segmentPart", segmentPart)
	isTimeSubsMedia, err := writeTimeSubsMediaSegment(w, cfg, a, segmentPart, nowMS, tt, isLast)
	if isTimeSubsMedia {
		return err
	}
	outSeg, err := genLiveSegment(log, vodFS, a, cfg, segmentPart, nowMS, isLast)
	if err != nil {
		return fmt.Errorf("convertToLive: %w", err)
	}
	var data []byte
	if outSeg.seg != nil {
		if cfg.DRM != "" {
			frags := outSeg.seg.Fragments
			err := encryptFrags(log, cfg, drmCfg, outSeg.meta.rep, frags)
			if err != nil {
				return fmt.Errorf("encryptFrags: %w", err)
			}
		}
		sw := bits.NewFixedSliceWriter(int(outSeg.seg.Size()))
		err = outSeg.seg.EncodeSW(sw)
		if err != nil {
			log.Error("write live segment response", "error", err)
			return err
		}
		data = sw.Bytes()
	} else {
		data = outSeg.data
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Type", outSeg.meta.rep.SegmentType())
	nrWritten := 0
	for {
		n, err := w.Write(data[nrWritten:])
		if err != nil {
			log.Error("write live segment response", "error", err)
			return err
		}
		nrWritten += n
		if nrWritten == len(data) {
			break
		}
	}

	return nil
}

func encryptFrags(log *slog.Logger, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	rp *RepData, frags []*mp4.Fragment) error {
	var ipd *mp4.InitProtectData
	var key, kid, iv []byte
	var scheme string
	ed := rp.encData
	switch cfg.DRM {
	case "eccp-cenc", "eccp-cbcs":
		scheme = strings.TrimPrefix(cfg.DRM, "eccp-")
		ipd = ed.initEnc[scheme].pd
		key = ed.key[:]
		iv = ed.iv[:]
	default: //  cfg.DRM != ""
		dd, ok := drmCfg.Map[cfg.DRM]
		if !ok {
			return fmt.Errorf("drm configuration %q not found", cfg.DRM)
		}
		keyData, err := dd.CPIXData.GetContentKey(rp.ContentType)
		if err != nil {
			return fmt.Errorf("get content key for %s: %w", rp.ContentType, err)
		}
		scheme = keyData.CommonEncryptionScheme
		ipdStart := *ed.initEnc[scheme].pd
		ipd = &ipdStart
		tenc := *ipd.Tenc
		tenc.DefaultKID = keyData.KeyID
		tenc.DefaultConstantIV = keyData.ExplicitIV
		iv = keyData.ExplicitIV
		ipd.Tenc = &tenc
		key = keyData.Key
	}
	log.Debug("encrypting with DRM", "scheme", scheme, "kid", hex.EncodeToString(kid), "iv", hex.EncodeToString(iv))
	for i, f := range frags {
		err := mp4.EncryptFragment(f, key, iv, ipd)
		if err != nil {
			return fmt.Errorf("encrypt fragment %d: %w", i, err)
		}
	}
	return nil
}

type segOut struct {
	seg  *mp4.MediaSegment
	data []byte // Mainly used for image out
	meta segMeta
}

// findRepAndSegmentID finds the rep and segment ID (nr or time) for a given cfg and live segment request.
func findRepAndSegmentID(a *asset, segmentPart string) (r *RepData, segID int, err error) {
	for _, rep := range a.Reps {
		mParts := rep.mediaRegexp.FindStringSubmatch(segmentPart)
		if mParts == nil {
			continue
		}
		if len(mParts) != 2 {
			return nil, -1, fmt.Errorf("bad segment match")
		}
		segID, err = strconv.Atoi(mParts[1])
		if err != nil {
			return nil, -1, err
		}
		return rep, segID, nil
	}
	return nil, -1, errNotFound
}

// createOutSeg from the corresponding VoD segments. Checks time as well.
func createOutSeg(vodFS fs.FS, a *asset, cfg *ResponseConfig, segmentPart string, nowMS int) (segOut, error) {
	var so segOut
	rep, segID, err := findRepAndSegmentID(a, segmentPart)
	if err != nil {
		return so, fmt.Errorf("findRepAndSegmentID: %w", err)
	}

	if rep.ContentType == "audio" && !rep.PreEncrypted {
		so, err = createAudioSegment(vodFS, a, cfg, segmentPart, nowMS, rep, segID)
		if err != nil {
			return so, fmt.Errorf("createAudioSegment: %w", err)
		}
		return so, nil
	}

	switch cfg.getRepType(segmentPart) {
	case segmentNumber, timeLineNumber:
		nr := uint32(segID)
		if nr < uint32(cfg.getStartNr()) {
			return so, errNotFound
		}
		so.meta, err = findSegMetaFromNr(a, rep, nr, cfg, nowMS)
	case timeLineTime:
		time := uint64(segID)
		so.meta, err = findSegMetaFromTime(a, rep, time, cfg, nowMS)
	default:
		return so, fmt.Errorf("unknown liveMPD type")
	}
	if err != nil {
		return so, err
	}
	segPath := path.Join(a.AssetPath, replaceTimeAndNr(rep.MediaURI, so.meta.origTime, so.meta.origNr))
	so.data, err = fs.ReadFile(vodFS, segPath)
	if err != nil {
		return so, fmt.Errorf("read segment: %w", err)
	}
	return so, nil
}

func findSegMeta(a *asset, cfg *ResponseConfig, segmentPart string, nowMS int) (segMeta, error) {
	var sm segMeta
	rep, segID, err := findRepAndSegmentID(a, segmentPart)
	if err != nil {
		return sm, fmt.Errorf("findRepAndSegmentID: %w", err)
	}

	if rep.ContentType == "audio" {
		sm, err := findRefSegMeta(a, cfg, segmentPart, nowMS, rep, segID)
		if err != nil {
			return sm, fmt.Errorf("findRefSegMeta: %w", err)
		}
		return sm, nil
	} else {
		switch cfg.getRepType(segmentPart) {
		case segmentNumber, timeLineNumber:
			nr := uint32(segID)
			if nr < uint32(cfg.getStartNr()) {
				return sm, errNotFound
			}
			sm, err = findSegMetaFromNr(a, rep, nr, cfg, nowMS)
		case timeLineTime:
			time := uint64(segID)
			sm, err = findSegMetaFromTime(a, rep, time, cfg, nowMS)
		default:
			return sm, fmt.Errorf("unknown liveMPD type")
		}
		if err != nil {
			return sm, err
		}
	}
	return sm, nil
}

func createAudioSegment(vodFS fs.FS, a *asset, cfg *ResponseConfig, segmentPart string, nowMS int,
	rep *RepData, segID int) (segOut, error) {
	refRep := a.refRep
	refTimescale := uint64(refRep.MediaTimescale)

	refMeta, err := findRefSegMeta(a, cfg, segmentPart, nowMS, rep, segID)
	if err != nil {
		return segOut{}, fmt.Errorf("findRefSegMeta: %w", err)
	}

	recipe := calcAudioSegRecipe(refMeta.newNr,
		refMeta.newTime,
		refMeta.newTime+uint64(refMeta.newDur),
		uint64(refRep.duration()),
		refTimescale, rep)
	var so segOut
	so.seg, err = createAudioSeg(vodFS, a, recipe)
	if err != nil {
		return so, fmt.Errorf("createAudioSeg: %w", err)
	}
	so.meta = segMeta{
		rep:       rep,
		newTime:   recipe.startTime,
		newDur:    uint32(recipe.endTime - recipe.startTime),
		newNr:     recipe.segNr,
		timescale: uint32(rep.MediaTimescale),
	}

	return so, nil
}

// findRefSegMeta finds the reference track meta data given other following track like audio
func findRefSegMeta(a *asset, cfg *ResponseConfig, segmentPart string, nowMS int, rep *RepData, segID int) (segMeta, error) {
	var refMeta segMeta
	var err error
	switch cfg.getRepType(segmentPart) {
	case segmentNumber, timeLineNumber:
		outSegNr := uint32(segID)
		refMeta, err = findSegMetaFromNr(a, a.refRep, outSegNr, cfg, nowMS)
		if err != nil {
			return refMeta, fmt.Errorf("findSegMetaFromNr from reference: %w", err)
		}
	case timeLineTime:
		time := int(segID)
		refMeta, err = findRefSegMetaFromTime(a, rep, uint64(time), cfg, nowMS)
		if err != nil {
			return refMeta, fmt.Errorf("findSegMetaFromNr from reference: %w", err)
		}
	default:
		return refMeta, fmt.Errorf("unknown liveMPD type")
	}
	return refMeta, nil
}

// prepareChunks generates a live segment, chunks it, and encrypts it if needed.
func prepareChunks(log *slog.Logger, vodFS fs.FS, a *asset, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	segmentPart string, nowMS int, isLast bool, chunkIndex *int) (segOut, []chunk, error) {

	so, err := genLiveSegment(log, vodFS, a, cfg, segmentPart, nowMS, isLast)
	if err != nil {
		return so, nil, fmt.Errorf("convertToLive: %w", err)
	}
	if isImage(segmentPart) {
		return so, nil, nil
	}
	if so.seg == nil {
		return so, nil, fmt.Errorf("no segment data for chunked segment")
	}

	rep := so.meta.rep
	seg := so.seg

	// Calculate chunk duration in media timescale units
	var chunkDur int
	if cfg.ChunkDurS != nil && *cfg.ChunkDurS > 0 {
		// Use explicit chunk duration from URL parameter
		chunkDur = int(*cfg.ChunkDurS * float64(rep.MediaTimescale))
	} else {
		// Fallback: fragment/chunk duration is segment_duration-availabilityTimeOffset
		chunkDur = (a.SegmentDurMS - int(cfg.AvailabilityTimeOffsetS*1000)) * int(rep.MediaTimescale) / 1000
	}
	chunks, err := chunkSegment(rep.initSeg, seg, so.meta, chunkDur, chunkIndex)
	if err != nil {
		return so, nil, fmt.Errorf("chunkSegment: %w", err)
	}
	if cfg.DRM != "" {
		frags := make([]*mp4.Fragment, len(chunks))
		for i, chk := range chunks {
			frags[i] = chk.frag
		}
		err := encryptFrags(log, cfg, drmCfg, rep, frags)
		if err != nil {
			return so, nil, fmt.Errorf("encryptFrags: %w", err)
		}
	}
	return so, chunks, nil
}

func setHeaders(w http.ResponseWriter, so segOut, segmentPart string) error {
	w.Header().Set("Content-Type", so.meta.rep.SegmentType())
	if isImage(segmentPart) {
		w.Header().Set("Content-Length", strconv.Itoa(len(so.data)))
		_, err := w.Write(so.data)
		if err != nil {
			return fmt.Errorf("could not write image segment: %w", err)
		}
		return nil
	}
	return nil
}

// writeChunkedSegment splits a segment into chunks and send them as they become available timewise.
//
// nowMS servers as reference for the current time and can be set to any value. Media time will
// be incremented with respect to nowMS.
func writeChunkedSegment(ctx context.Context, log *slog.Logger, w http.ResponseWriter, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	vodFS fs.FS, a *asset, segmentPart string, nowMS int, isLast bool) error {

	log.Debug("writeChunkedSegment", "segmentPart", segmentPart)

	so, chunks, err := prepareChunks(log, vodFS, a, cfg, drmCfg, segmentPart, nowMS, isLast, nil)
	if err != nil {
		return err
	}

	err = setHeaders(w, so, segmentPart)
	if err != nil {
		return err
	}
	if isImage(segmentPart) {
		return nil
	}
	rep := so.meta.rep

	startUnixMS := unixMS()
	chunkAvailTime := int(so.meta.newTime) + cfg.StartTimeS*int(rep.MediaTimescale)
	for _, chk := range chunks {
		chunkAvailTime += int(chk.dur)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		chunkAvailMS := chunkAvailTime * 1000 / int(rep.MediaTimescale)
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

func writeSubSegment(ctx context.Context, log *slog.Logger, w http.ResponseWriter, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	vodFS fs.FS, a *asset, segmentPart string, subSegmentPart string, nowMS int, isLast bool) error {

	log.Debug("writeSubSegment", "segmentPart", segmentPart, "subSegmentPart", subSegmentPart)

	chunkIndex, err := strconv.Atoi(subSegmentPart)
	if err != nil {
		return fmt.Errorf("bad chunk index: %w", err)
	}
	if chunkIndex < 0 {
		return fmt.Errorf("non-positive chunk index: %d", chunkIndex)
	}

	so, chunk, err := prepareChunks(log, vodFS, a, cfg, drmCfg, segmentPart, nowMS, isLast, &chunkIndex)
	if err != nil {
		return err
	}

	err = setHeaders(w, so, segmentPart)
	if err != nil {
		return err
	}
	if isImage(segmentPart) {
		return nil
	}
	rep := so.meta.rep

	chunkAvailTime := int(so.meta.newTime) + cfg.StartTimeS*int(rep.MediaTimescale)

	if len(chunk) != 1 {
		return fmt.Errorf("get chunk %d: expected 1 chunk, got %d", chunkIndex, len(chunk))
	}

	chk := chunk[0]

	chunkAvailTime += int(chk.dur)

	if ctx.Err() != nil {
		return ctx.Err()
	}

	chunkAvailMS := chunkAvailTime * 1000 / int(rep.MediaTimescale)
	if chunkAvailMS < nowMS {
		err = writeChunk(w, chk)
		if err != nil {
			return fmt.Errorf("writeChunk: %w", err)
		}
		return nil
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
func chunkSegment(init *mp4.InitSegment, seg *mp4.MediaSegment, segMeta segMeta, chunkDur int, chunkIndex *int) ([]chunk, error) {
	trex := init.Moov.Mvex.Trex
	fs := make([]mp4.FullSample, 0, 32)
	for _, f := range seg.Fragments {
		ff, err := f.GetFullSamples(trex)
		if err != nil {
			return nil, err
		}
		fs = append(fs, ff...)
	}
	chunks := make([]chunk, 0, segMeta.newDur/uint32(chunkDur))
	trackID := init.Moov.Trak.Tkhd.TrackID
	ch := createChunk(seg.Styp, trackID, segMeta.newNr)
	chunkNr := 1
	var totalDur = 0
	sampleDecodeTime := segMeta.newTime
	var thisChunkDur uint32 = 0
	for i := range fs {
		fs[i].DecodeTime = sampleDecodeTime
		ch.frag.AddFullSample(fs[i])
		dur := fs[i].Dur
		sampleDecodeTime += uint64(dur)
		thisChunkDur += dur
		totalDur += int(dur)
		if totalDur >= chunkDur*chunkNr {
			ch.dur = uint64(thisChunkDur)
			if chunkIndex == nil {
				chunks = append(chunks, ch)
			} else if (chunkNr - 1) == *chunkIndex {
				chunks = append(chunks, ch)
				return chunks, nil
			}
			ch = createChunk(nil, trackID, segMeta.newNr)
			thisChunkDur = 0
			chunkNr++
		}
	}
	if thisChunkDur > 0 {
		ch.dur = uint64(thisChunkDur)
		if chunkIndex == nil {
			chunks = append(chunks, ch)
		} else if (chunkNr - 1) == *chunkIndex {
			chunks = append(chunks, ch)
		}
	}

	if chunkIndex != nil && len(chunks) == 0 {
		return nil, fmt.Errorf("chunk %d not found", *chunkIndex)
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
