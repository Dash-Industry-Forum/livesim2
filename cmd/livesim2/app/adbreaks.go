// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Ad-break schedules shared by the ad-signaling features (sgai_ and svta_). A schedule is
// either a fixed list of breaks at given offsets from the availabilityStartTime, or a
// recurring (periodic) break anchored to the wall clock. Two views of a schedule are needed:
// instances() lists the occurrences to signal in an MPD at a given time, and windowAt() tells
// whether one specific media time falls inside a break (used to render the AD BREAK slate).

// AdBreak is a single ad break expressed relative to the availabilityStartTime.
type AdBreak struct {
	// OffsetS is the break presentation time in seconds from the period (AST) start.
	OffsetS int `json:"OffsetS"`
	// DurationS is the active window and the maximum ad-pod duration in seconds.
	DurationS int `json:"DurationS"`
}

// AdBreakPeriodic describes recurring ad breaks: a break of DurationS starts at every
// wall-clock multiple of PeriodS since the epoch (e.g. PeriodS=60 means every start of a
// minute, UTC). The anchoring is wall-clock, not availabilityStartTime, so all sessions
// share the same break schedule and a viewer may join in the middle of a break.
//
// OffsetsS moves the breaks inside the cycle and allows more than one per cycle: with
// PeriodS=60, DurationS=10 and OffsetsS=[10,40] a 10s break starts at 10s and at 40s after
// every full minute. An empty OffsetsS means a single break at the start of the cycle.
type AdBreakPeriodic struct {
	PeriodS   int   `json:"PeriodS"`
	DurationS int   `json:"DurationS"`
	OffsetsS  []int `json:"OffsetsS,omitempty"`
}

// offsets returns the in-cycle break offsets, defaulting to a single one at the cycle start.
func (p *AdBreakPeriodic) offsets() []int {
	if len(p.OffsetsS) == 0 {
		return []int{0}
	}
	return p.OffsetsS
}

// AdBreaks is an ad-break schedule: either a fixed list or a periodic recurrence.
// It is embedded in the feature configurations (SGAIConfig, SVTAConfig), so its fields
// appear directly in their JSON.
type AdBreaks struct {
	Breaks   []AdBreak        `json:"Breaks,omitempty"`   // fixed breaks (offset from AST)
	Periodic *AdBreakPeriodic `json:"Periodic,omitempty"` // recurring breaks (mutually exclusive with Breaks)
}

// parseAdBreaks parses the break part of an ad-signaling URL option value.
//
// Grammar: <off>:<dur>[,<off>:<dur>...] | p<period>:<dur>
//
// Examples: "30:15,90:15" => two 15 s breaks, 30 s and 90 s into the stream.
// "p60:20" => a 20 s break at every start of a (UTC) minute, recurring forever.
func parseAdBreaks(opt, spec string) (AdBreaks, error) {
	var ab AdBreaks
	if pSpec, ok := strings.CutPrefix(spec, "p"); ok {
		// Periodic: p<period>:<dur>[@<off>,...] — breaks of <dur> at the given offsets in
		// every wall-clock cycle of <period>, or at the cycle start when no offset is given.
		body, offSpec, hasOffsets := strings.Cut(pSpec, "@")
		per, dur, ok := strings.Cut(body, ":")
		if !ok {
			return ab, fmt.Errorf("%s periodic %q must be p<period>:<dur>[@<off>,...]", opt, spec)
		}
		perS, err := strconv.Atoi(per)
		if err != nil || perS <= 0 {
			return ab, fmt.Errorf("%s periodic %q: bad period", opt, spec)
		}
		durS, err := strconv.Atoi(dur)
		if err != nil || durS <= 0 {
			return ab, fmt.Errorf("%s periodic %q: bad duration", opt, spec)
		}
		if durS >= perS {
			return ab, fmt.Errorf("%s periodic %q: duration must be less than the period", opt, spec)
		}
		var offsets []int
		if hasOffsets {
			for os := range strings.SplitSeq(offSpec, ",") {
				off, err := strconv.Atoi(os)
				if err != nil || off < 0 {
					return ab, fmt.Errorf("%s periodic %q: bad offset %q", opt, spec, os)
				}
				if len(offsets) > 0 && off < offsets[len(offsets)-1]+durS {
					return ab, fmt.Errorf("%s periodic %q: offsets must increase by at least the duration", opt, spec)
				}
				offsets = append(offsets, off)
			}
			if len(offsets) == 0 {
				return ab, fmt.Errorf("%s periodic %q: empty offset list", opt, spec)
			}
			if last := offsets[len(offsets)-1]; last+durS > perS {
				return ab, fmt.Errorf("%s periodic %q: last break does not fit in the period", opt, spec)
			}
		}
		ab.Periodic = &AdBreakPeriodic{PeriodS: perS, DurationS: durS, OffsetsS: offsets}
		return ab, nil
	}
	for bs := range strings.SplitSeq(spec, ",") {
		off, dur, ok := strings.Cut(bs, ":")
		if !ok {
			return ab, fmt.Errorf("%s break %q must be <off>:<dur>", opt, bs)
		}
		offS, err := strconv.Atoi(off)
		if err != nil || offS < 0 {
			return ab, fmt.Errorf("%s break %q: bad offset", opt, bs)
		}
		durS, err := strconv.Atoi(dur)
		if err != nil || durS <= 0 {
			return ab, fmt.Errorf("%s break %q: bad duration", opt, bs)
		}
		ab.Breaks = append(ab.Breaks, AdBreak{OffsetS: offS, DurationS: durS})
	}
	return ab, nil
}

// empty reports whether the schedule has no breaks at all.
func (a *AdBreaks) empty() bool {
	return len(a.Breaks) == 0 && a.Periodic == nil
}

// equal reports whether two schedules describe the same breaks.
func (a *AdBreaks) equal(b *AdBreaks) bool {
	if (a.Periodic == nil) != (b.Periodic == nil) || len(a.Breaks) != len(b.Breaks) {
		return false
	}
	if a.Periodic != nil {
		if a.Periodic.PeriodS != b.Periodic.PeriodS || a.Periodic.DurationS != b.Periodic.DurationS {
			return false
		}
		if !slices.Equal(a.Periodic.offsets(), b.Periodic.offsets()) {
			return false
		}
	}
	return slices.Equal(a.Breaks, b.Breaks)
}

// adBreakInst is one concrete break occurrence to signal in the MPD.
type adBreakInst struct {
	id      uint64
	offsetS int64 // break start in seconds relative to the availabilityStartTime
	durS    int
}

// instances returns the break occurrences to signal at wall-clock time nowMS (ms since epoch)
// for a stream with availabilityStartTime astS (s since epoch). Fixed breaks are all signaled,
// unchanged across refreshes. For a periodic schedule the occurrences start at every wall-clock
// multiple of PeriodS since the epoch (e.g. every start of a minute for p60) and the list is
// windowed: the look-ahead is lookAheadS plus one period, and breaks that ended more than
// lookBackS seconds ago are dropped (so a break in progress is always kept, and a lookBackS of
// one timeshift-buffer depth keeps the signaling for breaks still reachable by seeking).
// The id is the occurrence number since the epoch, so it is stable across refreshes and unique
// per break (it keys the sgai @uri ?break= and the execution-delta state).
func (a *AdBreaks) instances(nowMS, astS, lookAheadS, lookBackS int) []adBreakInst {
	if a.Periodic == nil {
		out := make([]adBreakInst, 0, len(a.Breaks))
		for i, b := range a.Breaks {
			out = append(out, adBreakInst{id: uint64(i + 1), offsetS: int64(b.OffsetS), durS: b.DurationS})
		}
		return out
	}
	p, d := a.Periodic.PeriodS, a.Periodic.DurationS
	offs := a.Periodic.offsets()
	earliestS := nowMS/1000 - lookBackS
	horizonS := nowMS/1000 + lookAheadS + p
	// Earliest cycle that may still hold a relevant break. Offsets only move breaks later
	// within their cycle, so the last one of a cycle ends at cycleStart+maxOffset+d.
	k := max((earliestS-d-offs[len(offs)-1])/p, 0)
	var out []adBreakInst
	for t := k * p; t <= horizonS; t += p {
		for i, o := range offs {
			startS := t + o
			if startS+d <= earliestS || startS < astS || startS > horizonS {
				continue // out of the window, or before the availability start
			}
			out = append(out, adBreakInst{
				id:      uint64(t/p)*uint64(len(offs)) + uint64(i) + 1,
				offsetS: int64(startS - astS),
				durS:    d,
			})
		}
	}
	return out
}

// windowAt returns the end (in ms since the epoch) and the occurrence/event id of the break
// whose window contains wallMS, or ok=false when wallMS is outside every break. The id matches
// the event id signaled for that break (periodic: breakStart/period + 1; fixed: 1-based index —
// see instances), so the slate, the players' ad log and the beacons all show the same event id.
// Unlike instances (which windows the *signaled* events around the request time), this is purely
// a function of the asked-for time: a segment inside a past break that is still in the timeshift
// buffer must keep its slate no matter when it is requested.
func (a *AdBreaks) windowAt(wallMS int64, astS int) (int64, uint64, bool) {
	if a.Periodic != nil {
		pMS := int64(a.Periodic.PeriodS) * 1000
		dMS := int64(a.Periodic.DurationS) * 1000
		offs := a.Periodic.offsets()
		if wallMS < int64(astS)*1000 {
			return 0, 0, false
		}
		cycleStartMS := wallMS - wallMS%pMS
		// A break never spans a cycle boundary (parseAdBreaks requires the last one to fit),
		// so only the cycle holding wallMS can contain it.
		for i, o := range offs {
			startMS := cycleStartMS + int64(o)*1000
			// An occurrence starting before the availabilityStartTime is never signaled
			// (instances drops it, since its Event@presentationTime would be negative), so
			// it must not be slated either. Otherwise the first break straddling the AST
			// would show an "AD BREAK" countdown carrying an event id the MPD never
			// advertised, which no player could fill.
			if wallMS >= startMS && wallMS < startMS+dMS && startMS >= int64(astS)*1000 {
				cycle := uint64(cycleStartMS / pMS)
				return startMS + dMS, cycle*uint64(len(offs)) + uint64(i) + 1, true
			}
		}
		return 0, 0, false
	}
	for i, b := range a.Breaks {
		startMS := (int64(astS) + int64(b.OffsetS)) * 1000
		endMS := startMS + int64(b.DurationS)*1000
		if wallMS >= startMS && wallMS < endMS {
			return endMS, uint64(i + 1), true
		}
	}
	return 0, 0, false
}

// nextBreakWithin returns the start (in ms since the epoch) and the occurrence id of the
// next break starting within withinS seconds after wallMS, or ok=false when none is that
// close. It looks strictly forward, so a break that has already started is not reported —
// that one is windowAt's business. Used for the pre-break countdown.
func (a *AdBreaks) nextBreakWithin(wallMS int64, astS, withinS int) (int64, uint64, bool) {
	if withinS <= 0 {
		return 0, 0, false
	}
	horizonMS := wallMS + int64(withinS)*1000
	astMS := int64(astS) * 1000
	if a.Periodic != nil {
		pMS := int64(a.Periodic.PeriodS) * 1000
		offs := a.Periodic.offsets()
		cycleStartMS := wallMS - wallMS%pMS
		// A break never spans a cycle boundary, so the next one starts in this cycle or
		// in the one after it.
		for _, cycleMS := range []int64{cycleStartMS, cycleStartMS + pMS} {
			for i, o := range offs {
				startMS := cycleMS + int64(o)*1000
				if startMS <= wallMS || startMS > horizonMS || startMS < astMS {
					continue
				}
				return startMS, uint64(cycleMS/pMS)*uint64(len(offs)) + uint64(i) + 1, true
			}
		}
		return 0, 0, false
	}
	for i, b := range a.Breaks {
		startMS := astMS + int64(b.OffsetS)*1000
		if startMS > wallMS && startMS <= horizonMS {
			return startMS, uint64(i + 1), true
		}
	}
	return 0, 0, false
}

// adBreaksFor returns the ad-break schedule in effect for a request, or nil when the stream
// has no ad breaks. sgai_ and svta_ are mutually exclusive (see verifyAndFillConfig), so at
// most one of them is set.
func adBreaksFor(cfg *ResponseConfig) *AdBreaks {
	switch {
	case cfg.SGAI != nil:
		return &cfg.SGAI.AdBreaks
	case cfg.SVTA != nil:
		return &cfg.SVTA.AdBreaks
	case cfg.SCTE35 != nil && cfg.SCTE35.Slate:
		return &cfg.SCTE35.AdBreaks
	default:
		return nil
	}
}
