// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"encoding/json"
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSVTAConfig(t *testing.T) {
	cases := []struct {
		desc  string
		val   string
		err   string
		check func(t *testing.T, c *SVTAConfig)
	}{
		{
			desc: "single break with defaults",
			val:  "30:15",
			check: func(t *testing.T, c *SVTAConfig) {
				require.Len(t, c.Breaks, 1)
				assert.Equal(t, AdBreak{OffsetS: 30, DurationS: 15}, c.Breaks[0])
				assert.Equal(t, 1, c.AdsPerBreak)
				assert.Equal(t, svtaDefaultTimescale, c.Timescale)
				assert.Nil(t, c.SkipOffsetS)
				assert.False(t, c.ClickThrough)
				assert.False(t, c.Verification)
				assert.False(t, c.Pod)
			},
		},
		{
			desc: "periodic with all params",
			val:  "p60:20;ads=2;skip=5;click=1;verif=1;pod=1;ts=1000",
			check: func(t *testing.T, c *SVTAConfig) {
				require.NotNil(t, c.Periodic)
				assert.Equal(t, AdBreakPeriodic{PeriodS: 60, DurationS: 20}, *c.Periodic)
				assert.Equal(t, 2, c.AdsPerBreak)
				require.NotNil(t, c.SkipOffsetS)
				assert.Equal(t, 5, *c.SkipOffsetS)
				assert.True(t, c.ClickThrough)
				assert.True(t, c.Verification)
				assert.True(t, c.Pod)
				assert.Equal(t, uint32(1000), c.Timescale)
			},
		},
		{desc: "empty", val: "", err: "empty svta config"},
		{desc: "spaces", val: "30:15 ", err: `svta config "30:15 " has extra spaces`},
		{desc: "bad break", val: "30", err: `svta break "30" must be <off>:<dur>`},
		{desc: "param without value", val: "30:15;ads", err: `svta param "ads" must be key=val`},
		{desc: "unknown param", val: "30:15;foo=1", err: `unknown svta param "foo"`},
		{desc: "ads too low", val: "30:15;ads=0", err: `svta ads "0": must be 1-20`},
		{desc: "ads too high", val: "30:15;ads=21", err: `svta ads "21": must be 1-20`},
		{desc: "negative skip", val: "30:15;skip=-1", err: `svta skip "-1": must be >= 0`},
		{desc: "bad click", val: "30:15;click=yes", err: `svta click "yes": must be 0 or 1`},
		{desc: "zero timescale", val: "30:15;ts=0", err: `svta ts "0": must be a positive 32-bit integer`},
		{desc: "negative timescale", val: "30:15;ts=-1", err: `svta ts "-1": must be a positive 32-bit integer`},
		{desc: "timescale overflowing uint32", val: "30:15;ts=4294967296",
			err: `svta ts "4294967296": must be a positive 32-bit integer`},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			cfg, err := CreateSVTAConfig(c.val)
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

// svtaEventPayloads parses the JSON node data of every Event in the SVTA EventStream.
func svtaEventPayloads(t *testing.T, es *m.EventStreamType) []svtaEnvelope {
	t.Helper()
	out := make([]svtaEnvelope, 0, len(es.Events))
	for _, ev := range es.Events {
		var env svtaEnvelope
		require.NoError(t, json.Unmarshal([]byte(ev.Value), &env), "Event node data must be JSON")
		out = append(out, env)
	}
	return out
}

func TestAddSVTAAdCreativeEvents(t *testing.T) {
	cfg := NewResponseConfig()
	cfg.Host = "https://example.com"
	cfg.SteerSessionID = "alice"
	svtaCfg, err := CreateSVTAConfig("30:15;ads=2;skip=5;click=1;verif=1")
	require.NoError(t, err)
	cfg.SVTA = svtaCfg

	period := &m.Period{Id: "P0"}
	mpd := &m.MPD{Periods: []*m.Period{period}}
	addSVTAAdCreativeEvents(mpd, period, cfg, 0)

	require.Len(t, period.EventStreams, 1)
	es := period.EventStreams[0]
	assert.Equal(t, m.AnyURI(SVTAAdCreativeScheme), es.SchemeIdUri)
	require.NotNil(t, es.Timescale)
	assert.Equal(t, uint32(90000), *es.Timescale)
	require.Len(t, es.Events, 2, "one Event per creative")

	// The 15 s break is split into two 7.5 s creatives, back to back from 30 s.
	assert.Equal(t, uint64(30*90000), es.Events[0].PresentationTime)
	assert.Equal(t, uint64(7500*90), es.Events[0].Duration)
	assert.Equal(t, uint64(37500*90), es.Events[1].PresentationTime)
	assert.Equal(t, uint64(7500*90), es.Events[1].Duration)
	require.NotNil(t, es.Events[0].Id)
	assert.Equal(t, uint64(1001), *es.Events[0].Id)
	assert.Equal(t, uint64(1002), *es.Events[1].Id)

	envs := svtaEventPayloads(t, es)
	first := envs[0]
	assert.Equal(t, 2, first.Version)
	assert.Equal(t, "slot", first.Type)
	require.Len(t, first.Payload, 1, "one slot per Event, starting at the Event time")
	slot := first.Payload[0]
	assert.Equal(t, "linear", slot.Type)
	assert.Equal(t, 0.0, slot.Start)
	assert.Equal(t, 7.5, slot.Duration)
	require.Len(t, slot.Identifiers, 1)
	assert.Equal(t, svtaAdIDScheme, slot.Identifiers[0].Scheme)
	assert.Equal(t, "LSIM101H", slot.Identifiers[0].Value)
	require.NotNil(t, slot.SkipOffset)
	assert.Equal(t, 5.0, *slot.SkipOffset)
	assert.Equal(t, "https://example.com/", slot.ClickThrough)
	require.Len(t, slot.Verifications, 1)
	assert.Equal(t, svtaVerificationVendor, slot.Verifications[0].Vendor)
	assert.Equal(t, "https://example.com/static/svta_verification.js", slot.Verifications[0].Resource)

	// The tracking events are the VAST names Shaka maps, each pointing at the beacon
	// endpoint with the session and break ids baked in.
	gotTypes := make([]string, 0, len(slot.Tracking))
	for _, tr := range slot.Tracking {
		gotTypes = append(gotTypes, tr.Type)
		require.Len(t, tr.URLs, 1)
	}
	// The timeline points come first, then the offset-less interaction events, then the
	// click tracking added by click=1.
	assert.Equal(t, []string{"impression", "start", "firstQuartile", "midpoint", "thirdQuartile",
		"complete", "pause", "resume", "clickTracking"}, gotTypes)
	for _, tr := range slot.Tracking {
		if tr.Type == "pause" || tr.Type == "resume" {
			assert.Nil(t, tr.Offset, "%s must have no offset: its timing follows the event type", tr.Type)
		}
	}
	assert.Equal(t, "https://example.com/sgai/beacon/svta-ad1/impression?evId=1&sid=alice", slot.Tracking[0].URLs[0])
	assert.Equal(t, "https://example.com/sgai/beacon/svta-ad2/impression?evId=1&sid=alice",
		envs[1].Payload[0].Tracking[0].URLs[0])

	// The serialized MPD carries the scheme and the (entity-escaped) JSON node data.
	var buf bytes.Buffer
	_, err = mpd.Write(&buf, "  ", true)
	require.NoError(t, err)
	xmlStr := buf.String()
	for _, want := range []string{
		`schemeIdUri="urn:svta:advertising-wg:ad-creative-signaling"`,
		`timescale="90000"`,
		`presentationTime="2700000"`,
		`duration="675000"`,
		`id="1001"`,
		`&#34;version&#34;:2`,
		`&#34;type&#34;:&#34;linear&#34;`,
	} {
		assert.Contains(t, xmlStr, want)
	}
}

func TestAddSVTAAdCreativeEventsPeriodic(t *testing.T) {
	cfg := NewResponseConfig()
	cfg.Host = "https://example.com"
	svtaCfg, err := CreateSVTAConfig("p60:20;pod=1")
	require.NoError(t, err)
	cfg.SVTA = svtaCfg

	period := &m.Period{Id: "P0"}
	mpd := &m.MPD{Periods: []*m.Period{period}}
	// now = 1_000_000 s: with a 60 s timeshift buffer the ended break at 999_960 is kept,
	// and the look-ahead reaches 1_000_020 and 1_000_080.
	addSVTAAdCreativeEvents(mpd, period, cfg, 1_000_000_000)

	require.Len(t, period.EventStreams, 1)
	es := period.EventStreams[0]
	// 3 breaks x (1 pod event + 1 creative event).
	require.Len(t, es.Events, 6)
	envs := svtaEventPayloads(t, es)
	assert.Equal(t, "pod", envs[0].Type)
	assert.Equal(t, "slot", envs[1].Type)
	assert.Equal(t, 20.0, envs[0].Payload[0].Duration)
	podTracking := envs[0].Payload[0].Tracking
	require.Len(t, podTracking, 2)
	assert.Equal(t, "podStart", podTracking[0].Type)
	assert.Equal(t, "podEnd", podTracking[1].Type)
	require.NotNil(t, podTracking[1].Offset)
	assert.Equal(t, 20.0, *podTracking[1].Offset)

	// Ids are stable across refreshes and unique: pod id b*1000, creative id b*1000+slot.
	require.NotNil(t, es.Events[0].Id)
	assert.Equal(t, *es.Events[0].Id+1, *es.Events[1].Id)

	// Beyond the timeshift buffer and the look-ahead, nothing is signaled before the AST.
	cfg2 := NewResponseConfig()
	cfg2.StartTimeS = 2_000_000
	cfg2.SVTA = svtaCfg
	period2 := &m.Period{Id: "P0"}
	mpd2 := &m.MPD{Periods: []*m.Period{period2}}
	addSVTAAdCreativeEvents(mpd2, period2, cfg2, 1_000_000_000)
	assert.Empty(t, period2.EventStreams)
}
