// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAdBreaks(t *testing.T) {
	cases := []struct {
		desc string
		spec string
		err  string
		want AdBreaks
	}{
		{
			desc: "single fixed break",
			spec: "30:15",
			want: AdBreaks{Breaks: []AdBreak{{OffsetS: 30, DurationS: 15}}},
		},
		{
			desc: "two fixed breaks",
			spec: "30:15,90:30",
			want: AdBreaks{Breaks: []AdBreak{{OffsetS: 30, DurationS: 15}, {OffsetS: 90, DurationS: 30}}},
		},
		{
			desc: "periodic",
			spec: "p60:20",
			want: AdBreaks{Periodic: &AdBreakPeriodic{PeriodS: 60, DurationS: 20}},
		},
		{desc: "periodic with extra breaks", spec: "p60:20,30:15", err: `svta periodic "p60:20,30:15" cannot be combined with more breaks`},
		{desc: "periodic without duration", spec: "p60", err: `svta periodic "p60" must be p<period>:<dur>`},
		{desc: "periodic bad period", spec: "px:20", err: `svta periodic "px:20": bad period`},
		{desc: "periodic bad duration", spec: "p60:0", err: `svta periodic "p60:0": bad duration`},
		{desc: "periodic duration >= period", spec: "p60:60", err: `svta periodic "p60:60": duration must be less than the period`},
		{desc: "break without duration", spec: "30", err: `svta break "30" must be <off>:<dur>`},
		{desc: "break bad offset", spec: "-1:15", err: `svta break "-1:15": bad offset`},
		{desc: "break bad duration", spec: "30:x", err: `svta break "30:x": bad duration`},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			got, err := parseAdBreaks("svta", c.spec)
			if c.err != "" {
				require.EqualError(t, err, c.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestAdBreakInstances(t *testing.T) {
	// Fixed breaks pass through unchanged, ids 1..n, regardless of now.
	fixed := AdBreaks{Breaks: []AdBreak{{OffsetS: 30, DurationS: 15}, {OffsetS: 90, DurationS: 30}}}
	insts := fixed.instances(123_456_000, 0, 60, 0)
	require.Len(t, insts, 2)
	assert.Equal(t, adBreakInst{id: 1, offsetS: 30, durS: 15}, insts[0])
	assert.Equal(t, adBreakInst{id: 2, offsetS: 90, durS: 30}, insts[1])

	// Periodic p60:20, look-ahead 60: occurrences at every minute since the epoch.
	per := AdBreaks{Periodic: &AdBreakPeriodic{PeriodS: 60, DurationS: 20}}

	// now = 1_000_000s (not in a break): the ended break at 999_960 is dropped; the
	// look-ahead covers now + 60 + 60 (one period) -> 1_000_020 and 1_000_080.
	insts = per.instances(1_000_000_000, 0, 60, 0)
	require.Len(t, insts, 2)
	assert.Equal(t, adBreakInst{id: 16668, offsetS: 1_000_020, durS: 20}, insts[0])
	assert.Equal(t, adBreakInst{id: 16669, offsetS: 1_000_080, durS: 20}, insts[1])

	// now = 1_000_030s (mid-break): the in-progress break at 1_000_020 is kept, so a
	// late joiner lands in the middle of an ad.
	insts = per.instances(1_000_030_000, 0, 60, 0)
	require.GreaterOrEqual(t, len(insts), 2)
	assert.Equal(t, adBreakInst{id: 16668, offsetS: 1_000_020, durS: 20}, insts[0],
		"in-progress break still signaled")

	// Ids and wall-clock anchoring are stable across refreshes (same id for the same minute).
	again := per.instances(1_000_035_000, 0, 60, 0)
	assert.Equal(t, insts[0].id, again[0].id)

	// availabilityStartTime in the middle of the schedule: offsets are AST-relative and
	// occurrences before the AST are dropped.
	insts = per.instances(1_000_000_000, 999_990, 60, 0)
	require.NotEmpty(t, insts)
	assert.Equal(t, adBreakInst{id: 16668, offsetS: 30, durS: 20}, insts[0],
		"1_000_020 - 999_990 = 30s after AST")

	// A look-back (one timeshift-buffer depth) keeps the signaling for breaks that have
	// already ended but are still reachable by seeking. At now = 1_000_000 with a 120 s
	// buffer, the ended breaks at 999_900 and 999_960 come back.
	insts = per.instances(1_000_000_000, 0, 60, 120)
	require.Len(t, insts, 4)
	assert.Equal(t, adBreakInst{id: 16666, offsetS: 999_900, durS: 20}, insts[0])
	assert.Equal(t, adBreakInst{id: 16669, offsetS: 1_000_080, durS: 20}, insts[3])
}

func TestAdBreakWindowAt(t *testing.T) {
	// Membership is a pure function of the segment time — a long-past break (still in the
	// timeshift buffer) keeps its slate no matter when it is requested. The event id is the
	// occurrence number since the epoch (breakStart/period + 1).
	per := AdBreaks{Periodic: &AdBreakPeriodic{PeriodS: 60, DurationS: 20}}
	endMS, id, ok := per.windowAt(999_970_000, 0) // 10s into the break [999_960s, 999_980s)
	assert.True(t, ok)
	assert.Equal(t, int64(999_980_000), endMS)
	assert.Equal(t, uint64(16667), id)      // 999_960/60 + 1
	_, _, ok = per.windowAt(999_985_000, 0) // between breaks
	assert.False(t, ok)
	endMS, id, ok = per.windowAt(60_000, 0) // exactly at a break start
	assert.True(t, ok)
	assert.Equal(t, int64(80_000), endMS)
	assert.Equal(t, uint64(2), id)     // 60/60 + 1: the 2nd occurrence since the epoch
	_, _, ok = per.windowAt(80_000, 0) // exactly at the break end
	assert.False(t, ok)

	// A break occurrence that started before the availabilityStartTime is not signaled by
	// instances (its presentationTime would be negative), so windowAt must not slate it
	// either: with astS=65 the occurrence at [60s, 80s) straddles the AST and is skipped,
	// even though the request time (70s) is inside the window and after the AST.
	_, _, ok = per.windowAt(70_000, 65)
	assert.False(t, ok)
	// instances agrees: the straddling [60s,80s) break is dropped; the first signaled
	// occurrence is the next full one at [120s,140s) with id 120/60+1 = 3 (with no
	// look-ahead beyond the one period instances always adds).
	insts := per.instances(70_000, 65, 0, 0)
	assert.Equal(t, 1, len(insts))
	assert.Equal(t, uint64(3), insts[0].id)
	// That next full occurrence is both signaled and slated, with matching event ids.
	_, id, ok = per.windowAt(130_000, 65)
	assert.True(t, ok)
	assert.Equal(t, uint64(3), id)
}
