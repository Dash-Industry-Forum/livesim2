// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	gotsscte35 "github.com/Comcast/gots/v2/scte35"
	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/dash-mpd/xml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSCTE35Config(t *testing.T) {
	cases := []struct {
		desc  string
		val   string
		err   string
		check func(t *testing.T, c *SCTE35Config)
	}{
		{
			desc: "legacy preset 1",
			val:  "1",
			check: func(t *testing.T, c *SCTE35Config) {
				require.NotNil(t, c.Periodic)
				assert.Equal(t, AdBreakPeriodic{PeriodS: 60, DurationS: 20, OffsetsS: []int{10}}, *c.Periodic)
				assert.Equal(t, "insert", c.Cmd)
				assert.True(t, c.Emsg)
				assert.Equal(t, "off", c.MPDForm)
				assert.Equal(t, scte35DefaultLeadS, c.LeadS)
				assert.False(t, c.End, "a splice_insert with auto_return returns by itself")
				assert.False(t, c.Slate, "the legacy presets keep serving the underlying content")
			},
		},
		{
			desc: "legacy preset 3",
			val:  "3",
			check: func(t *testing.T, c *SCTE35Config) {
				require.NotNil(t, c.Periodic)
				assert.Equal(t, []int{10, 36, 46}, c.Periodic.OffsetsS)
				assert.Equal(t, 10, c.Periodic.DurationS)
			},
		},
		{
			desc: "time_signal with levels and creatives",
			val:  "p60:20@10;cmd=timesignal;seg=break,po;ads=2;adseg=dad;mpd=xml;lead=3;repeat=1;ts=1000;value=1001",
			check: func(t *testing.T, c *SCTE35Config) {
				assert.Equal(t, "timesignal", c.Cmd)
				assert.Equal(t, []string{"break", "po"}, c.Levels)
				assert.Equal(t, 2, c.AdsPerBreak)
				assert.Equal(t, "dad", c.AdLevel)
				assert.Equal(t, "xml", c.MPDForm)
				assert.Equal(t, 3, c.LeadS)
				assert.True(t, c.Repeat)
				assert.True(t, c.End, "a time_signal level is closed by its paired end descriptor")
				assert.Equal(t, uint32(1000), c.Timescale)
				assert.Equal(t, "1001", c.Value)
				assert.True(t, c.Slate, "the new grammar slates its breaks by default")
			},
		},
		{
			desc: "slate and pre-break countdown",
			val:  "p60:20@10;slate=1;pre=5",
			check: func(t *testing.T, c *SCTE35Config) {
				assert.True(t, c.Slate)
				assert.Equal(t, 5, c.PreS)
			},
		},
		{
			desc: "slate off",
			val:  "p60:20@10;slate=0",
			check: func(t *testing.T, c *SCTE35Config) {
				assert.False(t, c.Slate)
			},
		},
		{
			desc: "fixed breaks with a upid",
			val:  "30:15,90:15;cmd=timesignal;upid=0x08:abc",
			check: func(t *testing.T, c *SCTE35Config) {
				assert.Equal(t, []AdBreak{{OffsetS: 30, DurationS: 15}, {OffsetS: 90, DurationS: 15}}, c.Breaks)
				assert.Equal(t, uint8(8), c.UPIDType)
				assert.Equal(t, "abc", c.UPID)
			},
		},
		{desc: "empty", val: "", err: "empty scte35 config"},
		{desc: "spaces", val: "30:15 ", err: `scte35 config "30:15 " has extra spaces`},
		{desc: "bad break", val: "30", err: `scte35 break "30" must be <off>:<dur>`},
		{desc: "unknown param", val: "30:15;foo=1", err: `scte35 param "foo": unknown key`},
		{desc: "bad cmd", val: "30:15;cmd=splice", err: `scte35 cmd "splice": must be insert or timesignal`},
		{desc: "unknown level", val: "30:15;cmd=timesignal;seg=xyz", err: `scte35 seg "xyz": unknown segmentation level`},
		{desc: "levels in the wrong order", val: "30:15;cmd=timesignal;seg=po,break",
			err: `scte35 seg "po,break": levels must be listed outermost first, without repeats`},
		{desc: "repeated level", val: "30:15;cmd=timesignal;seg=po,po",
			err: `scte35 seg "po,po": levels must be listed outermost first, without repeats`},
		{desc: "bad mpd form", val: "30:15;mpd=json", err: `scte35 mpd "json": must be off, bin or xml`},
		{desc: "ads without timesignal", val: "30:15;ads=2", err: `scte35 ads needs cmd=timesignal`},
		{desc: "too many ads", val: "30:15;cmd=timesignal;ads=21", err: `scte35 ads "21": must be 0-20`},
		{desc: "ads longer than the break", val: "30:2;cmd=timesignal;ads=3",
			err: `scte35 ads=3 needs a break of at least 3 s, got 2 s`},
		{desc: "no carriage at all", val: "30:15;emsg=0",
			err: `scte35: emsg=0 needs mpd=bin or mpd=xml, or nothing is signaled`},
		{desc: "timesignal without levels", val: "30:15;cmd=timesignal;seg=",
			err: `scte35 cmd=timesignal needs at least one seg level or ads>0`},
		{desc: "bad upid", val: "30:15;upid=abc", err: `scte35 upid "abc": must be <type>:<value>`},
		{desc: "negative pre", val: "30:15;pre=-1", err: `scte35 pre "-1": must be >= 0`},
		{desc: "pre without slate", val: "30:15;pre=5;slate=0",
			err: "scte35 pre needs slate=1: the countdown is rendered on the video"},
		{desc: "pre longer than the gap", val: "p60:50@0;pre=15",
			err: "scte35 pre=15 does not fit in the 10 s between breaks"},
		{desc: "zero timescale", val: "30:15;ts=0", err: `scte35 ts "0": must be a positive 32-bit integer`},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			cfg, err := CreateSCTE35Config(c.val)
			if c.err != "" {
				require.EqualError(t, err, c.err)
				assert.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			c.check(t, cfg)
		})
	}
}

// TestSCTE35LegacyEmsg checks that the pre-1.14 scte35_1 stream is unchanged: one
// splice_insert emsg announcing a 20 s break 10 s after the full minute, delivered 7 s ahead
// in the segment holding the announce point, with the splice time and the event id derived
// from the break start second.
func TestSCTE35LegacyEmsg(t *testing.T) {
	cfg := NewResponseConfig()
	sc, err := CreateSCTE35Config("1")
	require.NoError(t, err)
	cfg.SCTE35 = sc

	// Segments of 2 s at 90 kHz. The break starts at 10 s, so the cue is announced at 3 s,
	// which falls in the segment covering [2 s, 4 s).
	const ts = 90000
	assert.Empty(t, scte35EmsgsForSegment(cfg, 0, 2*ts, ts), "no cue before the announce point")
	emsgs := scte35EmsgsForSegment(cfg, 2*ts, 4*ts, ts)
	require.Len(t, emsgs, 1)
	e := emsgs[0]
	assert.Equal(t, uint8(1), e.Version, "emsg v1 (SCTE 214-1 §6.7.1)")
	assert.Equal(t, scte35.SchemeIDURI, e.SchemeIDURI)
	assert.Equal(t, "", e.Value)
	assert.Equal(t, uint32(ts), e.TimeScale)
	assert.Equal(t, uint64(10*ts), e.PresentationTime, "splice time, in media time")
	assert.Equal(t, uint32(20*ts), e.EventDuration, "break duration")
	assert.Equal(t, uint32(10), e.ID, "the break start second")
	assert.Empty(t, scte35EmsgsForSegment(cfg, 4*ts, 6*ts, ts), "delivered once")

	sis := decodeSCTE35(t, e.MessageData)
	assert.Equal(t, gotsscte35.SpliceCommandType(gotsscte35.SpliceInsert), sis.Command())
	assert.Equal(t, uint64(10*scte35.TimescaleHz), uint64(sis.PTS()), "splice time on the 90 kHz clock")
}

// TestSCTE35CuePointHierarchy checks the nested signaling of SCTE 35 Figure 5: every level
// opening at the same instant travels in one time_signal, the creatives are numbered, and the
// closing message carries the end descriptors with the event ids of their starts.
func TestSCTE35CuePointHierarchy(t *testing.T) {
	sc, err := CreateSCTE35Config("p60:20@10;cmd=timesignal;seg=break,po;ads=2")
	require.NoError(t, err)
	b := adBreakInst{id: 7, offsetS: 10, durS: 20}
	cps := sc.cuePoints(b, 0)
	require.Len(t, cps, 3, "open, creative boundary, close")

	assert.Equal(t, int64(10), cps[0].atS)
	assert.Equal(t, int64(20), cps[1].atS)
	assert.Equal(t, int64(30), cps[2].atS)
	assert.Equal(t, []uint64{10, 20, 30}, []uint64{cps[0].id, cps[1].id, cps[2].id},
		"one id per message, the second of its splice point")
	assert.Equal(t, 20, cps[0].durS, "the opening message advertises the break duration")
	assert.Equal(t, 0, cps[2].durS, "a closing message has no duration (SCTE 214-1 §6.7.2.1)")

	open := cps[0].cue
	require.Len(t, open.Levels, 3)
	assert.Equal(t, []uint8{m.SegTypeBreakStart, m.SegTypeProviderPlacementOpportunityStart,
		m.SegTypeProviderAdvertisementStart}, levelTypes(open.Levels))
	assert.Equal(t, uint64(20*scte35.TimescaleHz), open.Levels[0].DurationPTS, "break duration")
	assert.Equal(t, uint64(10*scte35.TimescaleHz), open.Levels[2].DurationPTS, "creative duration")
	assert.Equal(t, uint8(1), open.Levels[2].Num)
	assert.Equal(t, uint8(2), open.Levels[2].Expected)

	mid := cps[1].cue
	assert.Equal(t, []uint8{m.SegTypeProviderAdvertisementEnd, m.SegTypeProviderAdvertisementStart},
		levelTypes(mid.Levels), "the first creative ends where the second starts")
	assert.Equal(t, open.Levels[2].EventID, mid.Levels[0].EventID, "the pair shares its event id")

	closing := cps[2].cue
	assert.Equal(t, []uint8{m.SegTypeProviderAdvertisementEnd, m.SegTypeProviderPlacementOpportunityEnd,
		m.SegTypeBreakEnd}, levelTypes(closing.Levels), "levels close innermost first")
	assert.Equal(t, open.Levels[0].EventID, closing.Levels[2].EventID, "Break start/end share their id")
	assert.Equal(t, open.Levels[1].EventID, closing.Levels[1].EventID, "PO start/end share their id")
	assert.Equal(t, mid.Levels[1].EventID, closing.Levels[0].EventID, "creative 2 start/end share their id")
	for _, l := range closing.Levels {
		assert.Zero(t, l.DurationPTS, "only the opening descriptor carries the duration")
	}
}

// TestSCTE35EmsgRepeat checks that repeat=1 puts the cue in every segment of the lead window,
// with the same emsg id, which SCTE 214-1 §6.7.3 item 6 lets a client discard after the first.
func TestSCTE35EmsgRepeat(t *testing.T) {
	cfg := NewResponseConfig()
	sc, err := CreateSCTE35Config("p60:20@10;lead=6;repeat=1")
	require.NoError(t, err)
	cfg.SCTE35 = sc

	const ts = 90000
	var ids []uint32
	for segStart := uint64(0); segStart < 12*ts; segStart += 2 * ts {
		for _, e := range scte35EmsgsForSegment(cfg, segStart, segStart+2*ts, ts) {
			ids = append(ids, e.ID)
			assert.Equal(t, uint64(10*ts), e.PresentationTime)
		}
	}
	assert.Equal(t, []uint32{10, 10, 10}, ids, "the segments starting at 4, 6 and 8 s")
}

func TestAddSCTE35Events(t *testing.T) {
	cases := []struct {
		desc       string
		val        string
		wantScheme string
		check      func(t *testing.T, ev *m.EventType)
	}{
		{
			desc:       "binary form",
			val:        "30:15;mpd=bin",
			wantScheme: m.SCTE35SchemeIdXMLBin,
			check: func(t *testing.T, ev *m.EventType) {
				require.NotNil(t, ev.Signal)
				require.NotNil(t, ev.Signal.Binary, "xml+bin carries <Signal><Binary>")
				assert.Nil(t, ev.Signal.SpliceInfoSection)
				assert.NotEmpty(t, ev.Signal.Binary.Value)
			},
		},
		{
			desc:       "xml form",
			val:        "30:15;mpd=xml;cmd=timesignal;seg=po",
			wantScheme: m.SCTE35SchemeIdXML,
			check: func(t *testing.T, ev *m.EventType) {
				require.NotNil(t, ev.Signal)
				sis := ev.SpliceInfo()
				require.NotNil(t, sis, "2013:xml may carry the parsed SpliceInfoSection")
				require.NotNil(t, sis.TimeSignal)
				require.Len(t, sis.SegmentationDescriptors, 1)
				assert.Equal(t, uint8(m.SegTypeProviderPlacementOpportunityStart),
					*sis.SegmentationDescriptors[0].SegmentationTypeId)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			cfg := NewResponseConfig()
			sc, err := CreateSCTE35Config(c.val)
			require.NoError(t, err)
			cfg.SCTE35 = sc

			period := &m.Period{Id: "P0"}
			// 25 s in: the break at 30 s has been announced (lead 7 s), so it is signaled.
			addSCTE35Events(period, cfg, 25_000)
			require.Len(t, period.EventStreams, 1)
			es := period.EventStreams[0]
			assert.Equal(t, m.AnyURI(c.wantScheme), es.SchemeIdUri)
			require.NotNil(t, es.Timescale)
			assert.Equal(t, uint32(90000), *es.Timescale)
			require.NotEmpty(t, es.Events)
			ev := es.Events[0]
			assert.Equal(t, uint64(30*90000), ev.PresentationTime)
			require.NotNil(t, ev.Duration)
			assert.Equal(t, uint64(15*90000), *ev.Duration)
			require.NotNil(t, ev.Id)
			assert.Equal(t, uint64(30), *ev.Id)
			c.check(t, ev)
		})
	}
}

// TestSCTE35ClosingEventDuration checks that the message closing a break is written with
// duration="0" rather than with no @duration at all. DASH gives an absent @duration the
// meaning "unknown", while SCTE 214-1 §6.7.2.1 item 2 wants a closing event to say zero.
func TestSCTE35ClosingEventDuration(t *testing.T) {
	cfg := NewResponseConfig()
	sc, err := CreateSCTE35Config("30:15;cmd=timesignal;seg=po;mpd=bin")
	require.NoError(t, err)
	cfg.SCTE35 = sc

	period := &m.Period{Id: "P0"}
	// 40 s in: the break at 30 s is open and its closing cue at 45 s is announced (lead 7 s).
	addSCTE35Events(period, cfg, 40_000)
	require.Len(t, period.EventStreams, 1)
	events := period.EventStreams[0].Events
	require.Len(t, events, 2, "the opening and the closing message")

	require.NotNil(t, events[1].Duration)
	assert.Equal(t, uint64(0), *events[1].Duration, "a closing event has a known, zero duration")

	out, err := xml.Marshal(events[1])
	require.NoError(t, err)
	assert.Contains(t, string(out), ` duration="0"`, "the zero must reach the MPD")
}

// TestAddSCTE35EventsAnnounce checks that an MPD event appears one lead time before its
// splice point, the same moment the inband cue is delivered.
func TestAddSCTE35EventsAnnounce(t *testing.T) {
	cfg := NewResponseConfig()
	sc, err := CreateSCTE35Config("30:15;mpd=bin;lead=7")
	require.NoError(t, err)
	cfg.SCTE35 = sc

	early := &m.Period{Id: "P0"}
	addSCTE35Events(early, cfg, 22_000)
	assert.Empty(t, early.EventStreams, "not announced yet at 22 s")

	announced := &m.Period{Id: "P0"}
	addSCTE35Events(announced, cfg, 23_000)
	require.Len(t, announced.EventStreams, 1)
	assert.Len(t, announced.EventStreams[0].Events, 1)
}

func TestVerifyAdSignaling(t *testing.T) {
	cases := []struct {
		desc   string
		scte35 string
		sgai   string
		err    string
	}{
		{desc: "same schedule", scte35: "p60:20;cmd=timesignal", sgai: "p60:20"},
		{desc: "different schedules", scte35: "p60:20", sgai: "p30:10",
			err: "sgai and scte35 must use the same ad-break schedule"},
		{desc: "different offsets", scte35: "p60:20@10", sgai: "p60:20",
			err: "sgai and scte35 must use the same ad-break schedule"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			cfg := NewResponseConfig()
			var err error
			cfg.SCTE35, err = CreateSCTE35Config(c.scte35)
			require.NoError(t, err)
			cfg.SGAI, err = CreateSGAIConfig(c.sgai)
			require.NoError(t, err)
			err = verifyAdSignaling(cfg)
			if c.err != "" {
				require.EqualError(t, err, c.err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func levelTypes(levels []scte35.Level) []uint8 {
	out := make([]uint8, 0, len(levels))
	for _, l := range levels {
		out = append(out, l.TypeID)
	}
	return out
}

func decodeSCTE35(t *testing.T, sis []byte) gotsscte35.SCTE35 {
	t.Helper()
	parsed, err := gotsscte35.NewSCTE35(append([]byte{0x00}, sis...))
	require.NoError(t, err)
	return parsed
}
