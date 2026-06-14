// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSGAIConfig(t *testing.T) {
	cases := []struct {
		name  string
		val   string
		ok    bool
		check func(t *testing.T, c *SGAIConfig)
	}{
		{name: "single break defaults", val: "30:15", ok: true, check: func(t *testing.T, c *SGAIConfig) {
			require.Len(t, c.Breaks, 1)
			assert.Equal(t, SGAIBreak{OffsetS: 30, DurationS: 15}, c.Breaks[0])
			assert.Equal(t, defaultSGAIResolveOffsetS, c.ResolveOffsetS)
			assert.Equal(t, defaultSGAIAdEndpoint, c.AdEndpoint)
			assert.True(t, c.Clip)
			assert.True(t, c.ExecuteOnce)
			assert.Nil(t, c.SkipAfterS)
			assert.Equal(t, int32(0), c.NoJump)
		}},
		{name: "all params", val: "30:15;skipafter=5;nojump=2;clip=1;once=0;resolve=10;ep=/x/ads", ok: true,
			check: func(t *testing.T, c *SGAIConfig) {
				require.NotNil(t, c.SkipAfterS)
				assert.Equal(t, 5, *c.SkipAfterS)
				assert.Equal(t, int32(2), c.NoJump)
				assert.True(t, c.Clip)
				assert.False(t, c.ExecuteOnce)
				assert.Equal(t, 10, c.ResolveOffsetS)
				assert.Equal(t, "/x/ads", c.AdEndpoint)
			}},
		{name: "multiple breaks", val: "30:15,90:30", ok: true, check: func(t *testing.T, c *SGAIConfig) {
			require.Len(t, c.Breaks, 2)
			assert.Equal(t, SGAIBreak{OffsetS: 90, DurationS: 30}, c.Breaks[1])
		}},
		{name: "periodic", val: "p60:20", ok: true, check: func(t *testing.T, c *SGAIConfig) {
			require.NotNil(t, c.Periodic)
			assert.Equal(t, 60, c.Periodic.PeriodS)
			assert.Equal(t, 20, c.Periodic.DurationS)
			assert.Empty(t, c.Breaks)
		}},
		{name: "periodic with params", val: "p30:10;skipafter=5", ok: true, check: func(t *testing.T, c *SGAIConfig) {
			require.NotNil(t, c.Periodic)
			assert.Equal(t, 30, c.Periodic.PeriodS)
			require.NotNil(t, c.SkipAfterS)
			assert.Equal(t, 5, *c.SkipAfterS)
		}},
		{name: "periodic dur >= period", val: "p20:20", ok: false},
		{name: "periodic zero period", val: "p0:5", ok: false},
		{name: "periodic zero dur", val: "p60:0", ok: false},
		{name: "periodic missing dur", val: "p60", ok: false},
		{name: "periodic mixed with fixed breaks", val: "p60:20,30:15", ok: false},
		{name: "empty", val: "", ok: false},
		{name: "missing dur", val: "30", ok: false},
		{name: "zero dur", val: "30:0", ok: false},
		{name: "negative offset", val: "-1:15", ok: false},
		{name: "bad nojump", val: "30:15;nojump=3", ok: false},
		{name: "unknown param", val: "30:15;foo=bar", ok: false},
		{name: "extra spaces", val: "30:15; nojump=2", ok: false},
		{name: "clip=0 accepted (renders clip=false)", val: "30:15;clip=0", ok: true,
			check: func(t *testing.T, c *SGAIConfig) { assert.False(t, c.Clip) }},
		{name: "clip=2 rejected", val: "30:15;clip=2", ok: false},
		{name: "ep protocol-relative rejected", val: "30:15;ep=//evil.com", ok: false},
		{name: "ep no leading slash rejected", val: "30:15;ep=evil", ok: false},
		{name: "ep with userinfo rejected", val: "30:15;ep=/a@evil.com", ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := CreateSGAIConfig(c.val)
			if !c.ok {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if c.check != nil {
				c.check(t, cfg)
			}
		})
	}
}

func TestAddSGAIReplaceEvents(t *testing.T) {
	cfg := NewResponseConfig()
	cfg.Host = "https://example.com"
	skip := 5
	cfg.SGAI = &SGAIConfig{
		Breaks:         []SGAIBreak{{OffsetS: 30, DurationS: 15}},
		AdEndpoint:     "/sgai/ads",
		ResolveOffsetS: 60,
		SkipAfterS:     &skip,
		NoJump:         2,
		Clip:           true,
		ExecuteOnce:    true,
	}
	period := &m.Period{Id: "P0"}
	mpd := &m.MPD{Periods: []*m.Period{period}}

	addSGAIReplaceEvents(mpd, period, cfg, 0)

	require.Len(t, period.EventStreams, 1)
	es := period.EventStreams[0]
	assert.Equal(t, AlternativeMPDReplaceScheme, string(es.SchemeIdUri))
	require.NotNil(t, es.Timescale)
	assert.Equal(t, uint32(90000), *es.Timescale)

	require.Len(t, es.Events, 1)
	ev := es.Events[0]
	assert.Equal(t, uint64(2700000), ev.PresentationTime) // 30s * 90000
	assert.Equal(t, uint64(1350000), ev.Duration)         // 15s * 90000
	require.NotNil(t, ev.Id)
	assert.Equal(t, uint64(1), *ev.Id)

	rp := ev.ReplacePresentation
	require.NotNil(t, rp)
	require.NotNil(t, rp.AlternativeMPDEventType, "embedded pointer must be allocated")
	assert.Equal(t, "https://example.com/sgai/ads?break=1&dur=15", rp.Uri)
	assert.Equal(t, uint64(1350000), rp.MaxDuration)
	assert.True(t, rp.ExecuteOnce)
	assert.Equal(t, int32(2), rp.NoJump)
	require.NotNil(t, rp.Clip)
	assert.True(t, *rp.Clip)

	require.Len(t, es.RequestParam, 1)
	assert.Equal(t, "altmpd", es.RequestParam[0].IncludeInRequests)
	assert.True(t, es.RequestParam[0].UseMPDUrlQuery)

	require.Len(t, mpd.EssentialProperties, 1)
	assert.Equal(t, UrlParam2025SchemeIdUri, string(mpd.EssentialProperties[0].SchemeIdUri))

	// Marshal via dash-mpd's own writer (the path livesim2 uses) so Duration renders as
	// ISO-8601. Guards against the embedded-pointer attributes being dropped and confirms
	// RequestParam serializes in the default DASH namespace.
	var buf bytes.Buffer
	_, err := mpd.Write(&buf, "  ", true)
	require.NoError(t, err)
	xmlStr := buf.String()
	for _, want := range []string{
		`schemeIdUri="urn:mpeg:dash:event:alternativeMPD:replace:2025"`,
		`timescale="90000"`,
		`presentationTime="2700000"`,
		`<ReplacePresentation`,
		`uri="https://example.com/sgai/ads?break=1&amp;dur=15"`,
		`earliestResolutionTimeOffset="5400000"`, // v0.15.1: decimal, not 5.4e+06
		`clip="true"`,                            // v0.15.1: *bool renders explicitly
		`maxDuration="1350000"`,
		`executeOnce="true"`,
		`noJump="2"`,
		`skipAfter="PT5S"`,
		`includeInRequests="altmpd"`,
	} {
		assert.Contains(t, xmlStr, want)
	}
}

func TestSGAIBreakInstances(t *testing.T) {
	// Fixed breaks pass through unchanged, ids 1..n, regardless of now.
	fixed := &SGAIConfig{Breaks: []SGAIBreak{{OffsetS: 30, DurationS: 15}, {OffsetS: 90, DurationS: 30}}}
	insts := fixed.breakInstances(123_456_000, 0)
	require.Len(t, insts, 2)
	assert.Equal(t, sgaiBreakInst{id: 1, offsetS: 30, durS: 15}, insts[0])
	assert.Equal(t, sgaiBreakInst{id: 2, offsetS: 90, durS: 30}, insts[1])

	// Periodic p60:20, resolve 60: occurrences at every minute since the epoch.
	per := &SGAIConfig{Periodic: &SGAIPeriodic{PeriodS: 60, DurationS: 20}, ResolveOffsetS: 60}

	// now = 1_000_000s (not in a break): the ended break at 999_960 is dropped; the
	// look-ahead covers now + 60 (resolve) + 60 (one period) -> 1_000_020 and 1_000_080.
	insts = per.breakInstances(1_000_000_000, 0)
	require.Len(t, insts, 2)
	assert.Equal(t, sgaiBreakInst{id: 16668, offsetS: 1_000_020, durS: 20}, insts[0])
	assert.Equal(t, sgaiBreakInst{id: 16669, offsetS: 1_000_080, durS: 20}, insts[1])

	// now = 1_000_030s (mid-break): the in-progress break at 1_000_020 is kept, so a
	// late joiner lands in the middle of an ad.
	insts = per.breakInstances(1_000_030_000, 0)
	require.GreaterOrEqual(t, len(insts), 2)
	assert.Equal(t, sgaiBreakInst{id: 16668, offsetS: 1_000_020, durS: 20}, insts[0],
		"in-progress break still signaled")

	// Ids and wall-clock anchoring are stable across refreshes (same id for the same minute).
	again := per.breakInstances(1_000_035_000, 0)
	assert.Equal(t, insts[0].id, again[0].id)

	// availabilityStartTime in the middle of the schedule: offsets are AST-relative and
	// occurrences before the AST are dropped.
	insts = per.breakInstances(1_000_000_000, 999_990)
	require.NotEmpty(t, insts)
	assert.Equal(t, sgaiBreakInst{id: 16668, offsetS: 30, durS: 20}, insts[0],
		"1_000_020 - 999_990 = 30s after AST")
}

func TestAddSGAIReplaceEventsPeriodic(t *testing.T) {
	cfg := NewResponseConfig()
	cfg.Host = "https://example.com"
	cfg.SGAI = &SGAIConfig{
		Periodic:       &SGAIPeriodic{PeriodS: 60, DurationS: 20},
		AdEndpoint:     "/sgai/ads",
		ResolveOffsetS: 60,
		Clip:           true,
		ExecuteOnce:    true,
	}
	period := &m.Period{Id: "P0"}
	mpd := &m.MPD{Periods: []*m.Period{period}}

	// now = 90s after epoch (AST = epoch): minute 1 (60-80s) is over, so events for
	// minutes 2 and 3 (120s, 180s) are signaled within the 90+60+60 horizon.
	addSGAIReplaceEvents(mpd, period, cfg, 90_000)

	require.Len(t, period.EventStreams, 1)
	es := period.EventStreams[0]
	require.Len(t, es.Events, 2)
	ev := es.Events[0]
	assert.Equal(t, uint64(120*90000), ev.PresentationTime)
	assert.Equal(t, uint64(20*90000), ev.Duration)
	require.NotNil(t, ev.Id)
	assert.Equal(t, uint64(3), *ev.Id) // 120/60 + 1: occurrence number since the epoch
	assert.Equal(t, "https://example.com/sgai/ads?break=3&dur=20", ev.ReplacePresentation.Uri)
	assert.Equal(t, uint64(180*90000), es.Events[1].PresentationTime)
	assert.Equal(t, uint64(4), *es.Events[1].Id)

	// The execution-delta state references the first signaled occurrence.
	require.Len(t, es.RequestParam, 1)
	assert.Contains(t, es.RequestParam[0].QueryTemplate, "execution-delta#3$")
}

func TestSGAIRejectedWithMultiPeriod(t *testing.T) {
	// sgai must not be combined with periods/xlink/etp/insertad.
	_, err := processURLCfg("/livesim2/sgai_30:15/periods_4/asset/Manifest.mpd", 0)
	assert.Error(t, err)
}
