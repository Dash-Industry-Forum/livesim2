// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/go-608/generate"
	"github.com/Eyevinn/go-608/schedule"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// cc608TargetPeriodMS is the nominal caption update period; go-608 snaps it to an
// even division of each segment (see generate.NumCues), so a 2.002s segment gets
// two ~1.001s cues, a 1.92s segment two ~0.96s cues, etc.
const cc608TargetPeriodMS = 1000

// cc608Line1Row and cc608Line2Row are the CEA-608 rows (1..15, 15 = bottom) that
// the two caption lines occupy. Near the top, so they don't collide with the
// player's control bar or the content's own bottom-of-frame overlays.
const (
	cc608Line1Row = 2
	cc608Line2Row = 3
)

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
// over segNr; keeping the content a pure function of (cueIdx, cueStartMS) is what
// lets a segment build the *next* segment's first cue without shared state.
func cc608CueContent(segNr uint32) generate.CueContentFunc {
	return func(cueIdx int, cueStartMS int64) generate.UnitCue {
		ts := time.UnixMilli(cueStartMS).UTC().Format("15:04:05.000")
		seg := fmt.Sprintf("SEG %d", segNr)
		return generate.UnitCue{Lines: []cta608.Line{
			{Row: cc608Line1Row, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: ts, Pen: cta608.Pen{Color: cta608.White}}}},
			{Row: cc608Line2Row, Align: cta608.AlignCenter, Runs: []cta608.Run{{Text: seg, Pen: cta608.Pen{Color: cta608.Yellow}}}},
		}}
	}
}

// cc608SplitEOC separates the terminating EOC command (the pop-on flip) from the
// build tokens, so the build can be transmitted ahead of the flip. A non-pop-on or
// empty sequence yields a nil eoc.
func cc608SplitEOC(toks []cta608.Token) (build, eoc []cta608.Token) {
	if n := len(toks); n > 0 {
		if c, ok := toks[n-1].(cta608.Command); ok && c.Op == cta608.EOC {
			return toks[:n-1], toks[n-1:]
		}
	}
	return toks, nil
}

// cc608PairCount returns the number of field-1 byte pairs a token sequence
// serializes to, i.e. how many frames it takes to drain at one pair per frame.
func cc608PairCount(toks []cta608.Token) int {
	if len(toks) == 0 {
		return 0
	}
	return len(cta608.Serialize(toks, cta608.SerializeOptions{
		Field: 1, Channel: 1, Doubling: cta608.DoublingOff,
	})) / 2
}

// cc608UnitFrames builds the per-frame CTA-608 schedule for one unit (one fragment)
// of nFrames frames starting at unitStartMS.
//
// A pop-on caption occupies two transmissions: a build (RCL + ENM + rows) written
// into non-displayed memory, and an EOC that flips it on screen. Both drain at one
// 608 pair per frame, so where the build is placed decides when the caption
// appears. go-608's generate.BuildUnitCues starts the build at its cue's first
// frame, which puts the flip ~pairs frames *into* the cue — ~0.5-0.75s of a
// one-second cue — so the caption became visible well after the time it displays.
//
// Here each cue's EOC rides its cue's first frame and its build drains over the
// frames immediately before it, so the flip coincides with the cue boundary and the
// caption is shown exactly over the interval its text names. The consequence is that
// a cue's build lives in the preceding cue's frames: the first cue's build belongs to
// the *previous* unit, and this unit's tail carries the build for the next unit's
// first cue (nextContent). Captions therefore span unit boundaries — a receiver that
// starts, seeks, or joins mid-stream gets the first EOC without the build that belongs
// to it. What it shows for that cue period depends on its decoder state: a fresh
// decoder has empty non-displayed memory and shows nothing, while one that keeps 608
// state across the discontinuity flips whatever was last preloaded and can show a
// stale caption. Either way it is correct from the next cue on. Recovering faster is a
// receiver-side matter — a player resetting its 608 state on a seek turns the stale
// case into the blank one — and not something the server can paper over, since an ENM
// ahead of the EOC would erase the build about to be flipped.
//
// Each cue is encoded with a fresh cta608.Encoder so its build is always a complete
// rebuild. That keeps every EOC paired with a build that fully describes its screen,
// which is what makes independently generated, on-demand units line up: the build in
// unit N's tail and the flip at unit N+1's first frame are produced by separate
// calls and must agree without sharing encoder state.
func cc608UnitFrames(fps float64, nFrames int, unitStartMS int64, content, nextContent generate.CueContentFunc) ([]schedule.Frame, error) {
	if nFrames <= 0 {
		return nil, fmt.Errorf("cc608: nFrames must be > 0, got %d", nFrames)
	}
	if cc := int(math.Round(600.0 / fps)); cc < 2 || cc > 31 {
		return nil, fmt.Errorf("cc608: fps %.3f yields cc_count %d outside 2..31", fps, cc)
	}
	frameDurMS := 1000.0 / fps
	unitDurMS := int64(math.Round(float64(nFrames) * frameDurMS))
	n := generate.NumCues(unitDurMS, cc608TargetPeriodMS)

	wallAt := func(i int) int64 { return unitStartMS + int64(math.Round(float64(i)*frameDurMS)) }
	// boundary(k) is the first unit-relative frame of cue k; boundary(n) == nFrames.
	boundary := func(k int) int { return int(math.Round(float64(k) * float64(nFrames) / float64(n))) }

	// Cue k flips at boundary(k). The extra entry at nFrames is the next unit's first
	// cue: its build drains this unit's tail, its EOC belongs to the next unit.
	type flip struct {
		frame int
		cue   generate.UnitCue
	}
	flips := make([]flip, 0, n+1)
	for k := 0; k < n; k++ {
		flips = append(flips, flip{boundary(k), content(k, wallAt(boundary(k)))})
	}
	flips = append(flips, flip{nFrames, nextContent(0, wallAt(nFrames))})

	sched := schedule.NewScheduler(fps, schedule.WithDoubling(cta608.DoublingOff))
	prevFlip := -1 // last frame already claimed by a flip
	for i, f := range flips {
		var enc cta608.Encoder
		build, eoc := cc608SplitEOC(enc.Apply(cta608.CaptionBlock{Lines: f.cue.Lines, Mode: cta608.PopOn}))
		buildStart := f.frame - cc608PairCount(build)
		// The first cue flips at frame 0, so its build was transmitted by the previous
		// unit and there is nothing to place here. Every other build must fit between
		// the previous flip and this one — a build that does not fit would leave an EOC
		// with nothing loaded, i.e. a caption that never appears.
		if i > 0 && len(build) > 0 {
			if buildStart <= prevFlip {
				free := f.frame - prevFlip - 1
				return nil, fmt.Errorf("cc608: cue flipping at frame %d needs %d frames of build but only %d are free "+
					"at %g fps; shorten the lines or lower the update rate", f.frame, f.frame-buildStart, free, fps)
			}
			// Pushes drain in push order (the scheduler is a FIFO gated by eligibility
			// time), so pushing the build before its EOC keeps the byte stream ordered
			// and lands the flip on f.frame exactly.
			sched.Push(schedule.TimedTokens{TimeMS: wallAt(buildStart), Field: 1, Tokens: build})
		}
		if f.frame < nFrames && len(eoc) > 0 {
			sched.Push(schedule.TimedTokens{TimeMS: wallAt(f.frame), Field: 1, Tokens: eoc})
		}
		prevFlip = f.frame
	}

	frames := make([]schedule.Frame, nFrames)
	for i := range frames {
		frames[i] = sched.Frame(wallAt(i))
	}
	return frames, nil
}

// injectCC608 splices in-band CTA-608 caption SEI into a unit's video samples in
// place. It builds the per-frame caption schedule (a UTC clock + the segment number,
// updated ~every second) with cc608UnitFrames, then inserts the resulting per-frame
// SEI NALU before the first VCL NALU of each sample, updating Data and Size. samples
// are the video track's FullSamples in decode order; fps and unitStartMS give the
// caption timing; segNr is this unit's segment number and nextSegNr the segment
// number of the unit that follows (the same segment for a non-final fragment).
//
// The schedule is presentation-ordered but samples arrive in decode order. Since
// receivers reassemble cc_data by presentation time (PTS) — dash.js, hls.js and
// Shaka all sort caption pairs by PTS — the k-th caption frame must ride the k-th
// sample in *presentation* order. With B-frames (decode order != presentation order)
// a naive frames[i]->samples[i] mapping permutes the CEA-608 byte stream and garbles
// the caption.
func injectCC608(samples []mp4.FullSample, fps float64, unitStartMS int64, segNr, nextSegNr uint32, codec carriage.Codec) error {
	if len(samples) == 0 {
		return nil
	}
	// cc608UnitFrames validates the frame rate and returns an error (never panics)
	// if it is out of the CEA-608 range.
	frames, err := cc608UnitFrames(fps, len(samples), unitStartMS, cc608CueContent(segNr), cc608CueContent(nextSegNr))
	if err != nil {
		return fmt.Errorf("cc608 build cues: %w", err)
	}
	if len(frames) != len(samples) {
		return fmt.Errorf("cc608: got %d frames for %d samples", len(frames), len(samples))
	}
	// order[k] = decode-order index of the k-th sample in presentation order.
	order := make([]int, len(samples))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return samples[order[a]].PresentationTime() < samples[order[b]].PresentationTime()
	})
	for k, idx := range order {
		f := frames[k]
		seiNALU := carriage.FrameSEINALU(f.Field1, f.Field2, f.CCCount, codec)
		newData, err := spliceSEIBeforeVCL(samples[idx].Data, seiNALU, codec)
		if err != nil {
			return fmt.Errorf("cc608 splice sample %d: %w", idx, err)
		}
		samples[idx].Data = newData
		samples[idx].Size = uint32(len(newData))
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

// applyCC608 injects in-band CTA-608 captions into a decoded video segment in
// place. For each fragment it reads the full samples, splices the per-frame SEI
// (via injectCC608), and writes the grown samples back into the fragment's trun
// sizes and mdat. It is only called for clear (non-DRM, non-pre-encrypted) video,
// so there is no senc/saio to adjust; and since only per-sample size *values*
// change, the moof size is unchanged and trun.DataOffset / mdat.StartPos stay
// valid (mirroring the tfdt-shift bookkeeping in genLiveSegment).
//
// Each fragment is one caption unit. Because a cue's build is transmitted ahead of
// its flip (see cc608UnitFrames), a fragment also carries the build for the first
// cue of whatever follows it: the next fragment of this segment, or — for the last
// fragment — the first cue of the next segment, which is why the next unit's segment
// number is passed down.
func applyCC608(seg *mp4.MediaSegment, meta segMeta, cfg *ResponseConfig) error {
	rep := meta.rep
	codec, ok := cc608CodecFor(rep.Codecs)
	if !ok {
		return fmt.Errorf("cc608: codec %q is not AVC or HEVC", rep.Codecs)
	}
	if rep.initSeg == nil || rep.initSeg.Moov == nil || rep.initSeg.Moov.Mvex == nil {
		return fmt.Errorf("cc608: missing init/trex for representation %q", rep.ID)
	}
	trex := rep.initSeg.Moov.Mvex.Trex
	for i, frag := range seg.Fragments {
		samples, err := frag.GetFullSamples(trex)
		if err != nil {
			return fmt.Errorf("cc608 getFullSamples: %w", err)
		}
		if len(samples) == 0 {
			continue
		}
		fps, err := cc608FPS(rep, samples)
		if err != nil {
			return err
		}
		fragStart := frag.Moof.Traf.Tfdt.BaseMediaDecodeTime()
		unitStartMS := int64(cfg.StartTimeS)*1000 + int64(fragStart)*1000/int64(meta.timescale)
		// The unit after the last fragment is the next segment; earlier fragments are
		// followed by another fragment of this segment, which keeps the same number.
		nextSegNr := meta.newNr
		if i == len(seg.Fragments)-1 {
			nextSegNr = meta.newNr + 1
		}
		if err := injectCC608(samples, fps, unitStartMS, meta.newNr, nextSegNr, codec); err != nil {
			return err
		}
		if err := writeBackCC608Samples(frag, samples); err != nil {
			return err
		}
	}
	// Injection grows each fragment, so a segment index (sidx) that references the
	// fragments by size would go stale. genLiveSegment's other mutations are
	// size-invariant, so this is the only place that must fix it. No-op unless the
	// stored segment carries a sidx with one reference per fragment.
	if seg.Sidx != nil && len(seg.Sidx.SidxRefs) == len(seg.Fragments) {
		for i, frag := range seg.Fragments {
			seg.Sidx.SidxRefs[i].ReferencedSize = uint32(frag.Moof.Size() + frag.Mdat.Size())
		}
	}
	return nil
}

// cc608FPS derives the video frame rate from the representation, reusing the
// shared RepData.sampleDur() and falling back to the first sample's duration.
func cc608FPS(rep *RepData, samples []mp4.FullSample) (float64, error) {
	durTicks := rep.sampleDur()
	if durTicks == 0 && len(samples) > 0 {
		durTicks = samples[0].Dur
	}
	if durTicks == 0 || rep.MediaTimescale == 0 {
		return 0, fmt.Errorf("cc608: cannot determine fps (timescale=%d, sampleDur=%d)", rep.MediaTimescale, durTicks)
	}
	return float64(rep.MediaTimescale) / float64(durTicks), nil
}

// writeBackCC608Samples writes grown samples back into a fragment: it updates each
// trun per-sample size and rebuilds the mdat from the samples' data. The samples
// must already be the injected ones (new, non-aliased Data).
func writeBackCC608Samples(frag *mp4.Fragment, samples []mp4.FullSample) error {
	trun := frag.Moof.Traf.Trun
	if !trun.HasSampleSize() {
		return fmt.Errorf("cc608: trun without per-sample sizes is not supported")
	}
	if len(trun.Samples) != len(samples) {
		return fmt.Errorf("cc608: trun has %d samples but got %d", len(trun.Samples), len(samples))
	}
	total := 0
	for i := range samples {
		total += len(samples[i].Data)
	}
	data := make([]byte, 0, total)
	for i := range samples {
		data = append(data, samples[i].Data...)
		trun.Samples[i].Size = samples[i].Size
	}
	frag.Mdat.Data = data
	return nil
}
