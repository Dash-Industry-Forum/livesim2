// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/Eyevinn/hi264/pkg/encode"
	"github.com/Eyevinn/hi264/pkg/yuv"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// SGAI slate: during a Replace-event break window the main video track serves generated
// "AD BREAK <countdown>" segments instead of the normal content, so the underlying stream
// visibly is "the ad to be replaced". A player that executes the Alternative-MPD event
// shows a personalized ad pod over this window; one that does not shows the slate. The
// main MPD is untouched (single Period, continuous timeline) — per Ed.6 (example G.29-1)
// the replaced interval is signaled by the event only and needs no Period of its own.
//
// Each slate segment splices seamlessly into the existing avc1 track: the IDR and P_Skip
// frames are encoded against the representation's own SPS/PPS (taken from its init
// segment), so no init change and no parameter-set shuffle. Audio is never touched.

// Limited-range Y'CbCr colors for the slate rendering.
var (
	slateTextColor = yuv.Color{Y: 235, Cb: 128, Cr: 128} // white
	slateTextBg    = yuv.Color{Y: 16, Cb: 128, Cr: 128}  // black box behind the glyphs
	slateBgColor   = yuv.Color{Y: 32, Cb: 128, Cr: 128}  // near-black background
)

// slateBlockSize is the PlaneGrid cell size. 8 px (2×2 cells per macroblock) renders
// text through hi264's 2x font path and supports heights that are not multiples of 16
// (e.g. 360).
const slateBlockSize = 8

// slateGen generates slate frames for one video representation, reusing its SPS/PPS.
type slateGen struct {
	sps    *avc.SPS
	pps    *avc.PPS
	params encode.EncodeParams
}

// newSlateGen builds a slate generator from a representation's init segment. It returns
// an error for tracks the encoder cannot splice into (non-AVC, interlaced, or
// pic_order_cnt_type 1).
func newSlateGen(initData []byte) (*slateGen, error) {
	sr := bits.NewFixedSliceReader(initData)
	iFile, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return nil, fmt.Errorf("decode init: %w", err)
	}
	if iFile.Init == nil || len(iFile.Init.Moov.Traks) != 1 {
		return nil, fmt.Errorf("init segment must have exactly one track")
	}
	stsd := iFile.Init.Moov.Traks[0].Mdia.Minf.Stbl.Stsd
	var avcC *mp4.AvcCBox
	if vse, ok := stsd.Children[0].(*mp4.VisualSampleEntryBox); ok {
		avcC = vse.AvcC
	}
	if avcC == nil || len(avcC.SPSnalus) == 0 || len(avcC.PPSnalus) == 0 {
		return nil, fmt.Errorf("no avcC with SPS/PPS found (slate needs an AVC track)")
	}
	sps, err := avc.ParseSPSNALUnit(avcC.SPSnalus[0], true)
	if err != nil {
		return nil, fmt.Errorf("parse SPS: %w", err)
	}
	if sps.PicOrderCntType != 0 && sps.PicOrderCntType != 2 {
		return nil, fmt.Errorf("pic_order_cnt_type=%d not supported (slate needs 0 or 2)", sps.PicOrderCntType)
	}
	if !sps.FrameMbsOnlyFlag {
		return nil, fmt.Errorf("interlaced content not supported")
	}
	pps, err := avc.ParsePPSNALUnit(avcC.PPSnalus[0], map[uint32]*avc.SPS{sps.ParameterID: sps})
	if err != nil {
		return nil, fmt.Errorf("parse PPS: %w", err)
	}
	return &slateGen{
		sps: sps,
		pps: pps,
		params: encode.EncodeParams{
			Width:  int(sps.Width),
			Height: int(sps.Height),
			CABAC:  pps.EntropyCodingModeFlag,
		},
	}, nil
}

// buildPlane renders the slate background with the text overlay (e.g. "AD BREAK\n17").
func (g *slateGen) buildPlane(text string) (*yuv.PlaneGrid, error) {
	bs := slateBlockSize
	bw := (g.params.Width + bs - 1) / bs
	bh := (g.params.Height + bs - 1) / bs
	pg := yuv.NewPlaneGrid(bw, bh, bs)
	for row := range bh {
		for col := range bw {
			pg.Y[row][col] = slateBgColor.Y
			pg.Cb[row][col] = slateBgColor.Cb
			pg.Cr[row][col] = slateBgColor.Cr
		}
	}
	// Half of the largest scale at which the text fits (8-px blocks use the 2x font).
	tw, th := yuv.TextWidth2x(text), yuv.TextHeight2x(text)
	if tw == 0 || th == 0 {
		return pg, nil
	}
	scale := max(min(bw/tw, bh/th)/2, 1)
	if err := yuv.OverlayTextOnPlane(pg, text, scale, slateTextColor, &slateTextBg); err != nil {
		return nil, fmt.Errorf("overlay text: %w", err)
	}
	return pg, nil
}

// slateFrameSpec describes one frame of a slate segment.
type slateFrameSpec struct {
	dur   uint32 // sample duration in media timescale
	size  uint32 // target sample size in bytes (the replaced sample's size); 0 = no padding
	label string // text to render; a change (or frame 0) forces an IDR
}

// samples encodes the slate frames as ready-to-mux fMP4 full samples. The first frame
// and every label change is an IDR (this is how the countdown updates); the frames in
// between are P_Skip repeats. Each frame is padded with a filler NALU up to its spec
// size (the replaced sample's size), so the slate matches the bitrate — and byte
// pattern — of the content it replaces (no ABR confusion from a sudden 3 kB segment).
// decodeStart is the tfdt time and ctoOffset the constant composition-time offset (the
// track's reorder delay) applied to every sample so the composition timeline stays
// continuous with the neighboring B-frame content.
func (g *slateGen) samples(specs []slateFrameSpec, decodeStart uint64, ctoOffset int32) ([]mp4.FullSample, error) {
	samples := make([]mp4.FullSample, 0, len(specs))
	t := decodeStart
	var frameNum uint32 // resets at each IDR
	var idrPicID uint32 // alternates between consecutive IDRs within the segment
	prevLabel := ""
	for i, spec := range specs {
		var annexB []byte
		var err error
		idr := i == 0 || spec.label != prevLabel
		if idr {
			pg, perr := g.buildPlane(spec.label)
			if perr != nil {
				return nil, perr
			}
			annexB, err = encode.GenerateIDRWithSPSPPS(g.params, g.sps, g.pps, pg, idrPicID)
			idrPicID++
			frameNum = 0
		} else {
			frameNum++
			annexB, err = encode.EncodePSkipSlice(g.sps, g.pps, frameNum, 2*frameNum, 0)
		}
		if err != nil {
			return nil, fmt.Errorf("encode slate frame %d: %w", i, err)
		}
		prevLabel = spec.label
		// Pad toward the replaced sample's size. A filler NALU needs >= 6 bytes; a
		// target below that (or below the encoded frame, which only happens for very
		// low-bitrate content) keeps the frame unpadded.
		if int(spec.size) >= len(annexB)+6 {
			if annexB, err = encode.PadSlice(annexB, int(spec.size)); err != nil {
				return nil, fmt.Errorf("pad slate frame %d: %w", i, err)
			}
		}
		data := avc.ConvertByteStreamToNaluSample(annexB)
		flags := mp4.NonSyncSampleFlags
		if idr {
			flags = mp4.SyncSampleFlags
		}
		samples = append(samples, mp4.FullSample{
			Sample: mp4.Sample{
				Flags: flags, Dur: spec.dur, Size: uint32(len(data)),
				CompositionTimeOffset: ctoOffset,
			},
			DecodeTime: t,
			Data:       data,
		})
		t += uint64(spec.dur)
	}
	return samples, nil
}

// slateGens caches one slateGen (or one failure, as nil) per "assetPath/repID".
var slateGens sync.Map

// slateGenFor returns the cached slate generator for a representation, building it from
// the rep's init segment on first use. A rep the generator cannot handle is cached as
// nil so the (failing) init parse does not repeat on every segment request.
func slateGenFor(vodFS fs.FS, a *asset, rep *RepData) (*slateGen, error) {
	key := a.AssetPath + "/" + rep.ID
	if v, ok := slateGens.Load(key); ok {
		if v == nil {
			return nil, fmt.Errorf("slate generation disabled for %s", key)
		}
		return v.(*slateGen), nil
	}
	initData, err := fs.ReadFile(vodFS, path.Join(a.AssetPath, rep.InitURI))
	if err == nil {
		var g *slateGen
		if g, err = newSlateGen(initData); err == nil {
			slateGens.Store(key, g)
			return g, nil
		}
	}
	slateGens.Store(key, nil)
	return nil, fmt.Errorf("no slate generator for %s: %w", key, err)
}

// breakWindowAt returns the end (in ms since the epoch) and the occurrence/event id of the
// break whose window contains the wall-clock time wallMS, or ok=false when wallMS is outside
// every break. The id matches the Replace event id the live MPD signals for that break
// (periodic: breakStart/period + 1; fixed: 1-based index — see breakInstances in sgai.go), so
// the slate, the players' ad log and the beacons all show the same event id.
// Unlike breakInstances (which windows the *signaled* events around the request time), this is
// purely a function of the asked-for time: a segment inside a past break that is still in the
// timeshift buffer must keep its slate no matter when it is requested.
func (c *SGAIConfig) breakWindowAt(wallMS int64, astS int) (int64, uint64, bool) {
	if c.Periodic != nil {
		pMS := int64(c.Periodic.PeriodS) * 1000
		dMS := int64(c.Periodic.DurationS) * 1000
		if wallMS < int64(astS)*1000 {
			return 0, 0, false
		}
		pos := wallMS % pMS
		breakStartMS := wallMS - pos
		// A periodic occurrence that starts before the availabilityStartTime is never
		// signaled (breakInstances drops t < astS, since its Event@presentationTime would be
		// negative), so it must not be slated either. Otherwise the first break straddling
		// the AST would show an "AD BREAK" countdown carrying an event id the MPD never
		// advertised, which no player could fill.
		if pos < dMS && breakStartMS >= int64(astS)*1000 {
			breakStartSec := breakStartMS / 1000
			return breakStartMS + dMS, uint64(breakStartSec/int64(c.Periodic.PeriodS)) + 1, true
		}
		return 0, 0, false
	}
	for i, b := range c.Breaks {
		startMS := (int64(astS) + int64(b.OffsetS)) * 1000
		endMS := startMS + int64(b.DurationS)*1000
		if wallMS >= startMS && wallMS < endMS {
			return endMS, uint64(i + 1), true
		}
	}
	return 0, 0, false
}

// sgaiBreakForSegment returns the end (in ms since the epoch) and the event id of the break
// whose window contains the start of the segment described by meta, or ok=false when the
// segment starts outside every break. Segment times are media times relative to the
// availabilityStartTime (cfg.StartTimeS).
func sgaiBreakForSegment(cfg *ResponseConfig, meta segMeta) (int64, uint64, bool) {
	segStartMS := int64(cfg.StartTimeS)*1000 + int64(meta.newTime)*1000/int64(meta.timescale)
	return cfg.SGAI.breakWindowAt(segStartMS, cfg.StartTimeS)
}

// applySGAISlate replaces the samples of a video segment with a generated
// "AD BREAK <countdown>" slate when the segment starts inside an SGAI break window.
// The countdown shows the seconds left of the break, updated with an IDR at every
// second change, counting down to zero when the live content returns. Returns the
// replacement segment, or nil when the segment is outside every break (or the rep
// cannot be slated, e.g. non-AVC) — the caller then serves the normal content.
//
// The slate keeps the original segment's exact sample timing: same sample count and
// durations, same tfdt/sequence number (already rewritten by the caller), and a constant
// composition offset equal to the original segment's reorder delay.
func applySGAISlate(vodFS fs.FS, a *asset, cfg *ResponseConfig, meta segMeta,
	seg *mp4.MediaSegment) (*mp4.MediaSegment, error) {

	breakEndMS, breakID, ok := sgaiBreakForSegment(cfg, meta)
	if !ok {
		return nil, nil
	}
	g, err := slateGenFor(vodFS, a, meta.rep)
	if err != nil {
		return nil, nil // not an error: rep cannot be slated, serve normal content
	}
	if len(seg.Fragments) != 1 {
		return nil, fmt.Errorf("slate needs exactly 1 fragment, got %d", len(seg.Fragments))
	}
	frag := seg.Fragments[0]
	trun := frag.Moof.Traf.Trun
	tfhd := frag.Moof.Traf.Tfhd
	defaultDur := uint32(0)
	if tfhd.HasDefaultSampleDuration() {
		defaultDur = tfhd.DefaultSampleDuration
	}
	if defaultDur == 0 {
		// Some packagings (e.g. low-delay assets) carry the sample duration only in the
		// init segment's trex, not in each segment's tfhd/trun. RepData.sampleDur() returns
		// that value (read from trex/tfhd at load time) so the slate can still mirror the
		// original sample timing instead of failing.
		defaultDur = meta.rep.sampleDur()
	}
	defaultSize := uint32(0)
	if tfhd.HasDefaultSampleSize() {
		defaultSize = tfhd.DefaultSampleSize
	}

	// Mirror the original sample timing and sizes (frame rate and bitrate of the
	// replaced content), and find the track's constant reorder delay: the smallest
	// composition time (relative decode time + cto) over the segment.
	segStartMS := int64(cfg.StartTimeS)*1000 + int64(meta.newTime)*1000/int64(meta.timescale)
	nrSamples := int(trun.SampleCount())
	specs := make([]slateFrameSpec, 0, nrSamples)
	dtsRel := int64(0)
	ctoOffset := int64(1<<62 - 1)
	for i := range nrSamples {
		dur := defaultDur
		size := defaultSize
		var cto int64
		if i < len(trun.Samples) {
			s := trun.Samples[i]
			if s.Dur > 0 {
				dur = s.Dur
			}
			if s.Size > 0 {
				size = s.Size
			}
			cto = int64(s.CompositionTimeOffset)
		}
		if dur == 0 {
			return nil, fmt.Errorf("cannot determine sample duration for slate")
		}
		ctoOffset = min(ctoOffset, dtsRel+cto)
		frameWallMS := segStartMS + dtsRel*1000/int64(meta.timescale)
		// Seconds left of the break at this frame: durS, ..., 2, 1 and 0 during the
		// last second — the countdown reaches zero as the live content returns.
		remainingS := max((breakEndMS-frameWallMS-1)/1000, 0)
		// Three lines: "AD BREAK", "#<event id>" (the break occurrence id, same value as the
		// players' ad log and the beacons) and the seconds-left countdown suffixed with "S".
		specs = append(specs, slateFrameSpec{dur: dur, size: size,
			label: fmt.Sprintf("AD BREAK\n#%d\n%dS", breakID, remainingS)})
		dtsRel += int64(dur)
	}

	samples, err := g.samples(specs, meta.newTime, int32(ctoOffset))
	if err != nil {
		return nil, fmt.Errorf("generate slate samples: %w", err)
	}
	newFrag, err := mp4.CreateFragment(meta.newNr, tfhd.TrackID)
	if err != nil {
		return nil, fmt.Errorf("create fragment: %w", err)
	}
	for _, s := range samples {
		newFrag.AddFullSample(s)
	}
	newSeg := mp4.NewMediaSegment()
	newSeg.Styp = seg.Styp
	// No sidx: the original one indexes the replaced payload and live segments do not need it.
	newSeg.AddFragment(newFrag)
	return newSeg, nil
}
