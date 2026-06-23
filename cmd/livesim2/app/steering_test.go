// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSteeringConfig(t *testing.T) {
	cases := []struct {
		name  string
		val   string
		ok    bool
		check func(t *testing.T, c *SteeringConfig)
	}{
		{name: "defaults", val: "alpha,beta", ok: true, check: func(t *testing.T, c *SteeringConfig) {
			assert.Equal(t, []string{"alpha", "beta"}, c.CDNs)
			assert.Equal(t, steeringDefaultTTLS, c.TTL)
			assert.Equal(t, steeringModeTrigger, c.Mode, "trigger is the default mode")
			assert.False(t, c.QueryBeforeStart)
			assert.Equal(t, "", c.Default)
		}},
		{name: "all params", val: "a,b,c;ttl=20;mode=trigger;qbs=1;default=b", ok: true, check: func(t *testing.T, c *SteeringConfig) {
			assert.Equal(t, []string{"a", "b", "c"}, c.CDNs)
			assert.Equal(t, 20, c.TTL)
			assert.Equal(t, steeringModeTrigger, c.Mode)
			assert.True(t, c.QueryBeforeStart)
			assert.Equal(t, "b", c.Default)
		}},
		{name: "empty", val: "", ok: false},
		{name: "single location", val: "alpha", ok: false},
		{name: "bad ttl", val: "a,b;ttl=0", ok: false},
		{name: "bad mode", val: "a,b;mode=cycle", ok: false},
		{name: "bad qbs", val: "a,b;qbs=2", ok: false},
		{name: "unknown param", val: "a,b;foo=1", ok: false},
		{name: "default not in list", val: "a,b;default=c", ok: false},
		{name: "duplicate locations", val: "a,a", ok: false},
		{name: "bad location char", val: "a,b/c", ok: false},
		{name: "param missing eq", val: "a,b;ttl", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := CreateSteeringConfig(tc.val)
			if !tc.ok {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tc.check(t, c)
		})
	}
}

func TestSteeringDefaultOrder(t *testing.T) {
	c := &SteeringConfig{CDNs: []string{"a", "b", "c"}}
	assert.Equal(t, []string{"a", "b", "c"}, c.defaultOrder())
	c.Default = "c"
	assert.Equal(t, []string{"c", "a", "b"}, c.defaultOrder(), "default moved to front, order otherwise kept")
}

func TestSteeringRotatePriority(t *testing.T) {
	c := &SteeringConfig{CDNs: []string{"a", "b", "c"}, TTL: 10}
	// bucket = floor(unix/TTL) mod N; the priority rotates one step each TTL window.
	assert.Equal(t, []string{"a", "b", "c"}, c.rotatePriority(0))
	assert.Equal(t, []string{"a", "b", "c"}, c.rotatePriority(9))
	assert.Equal(t, []string{"b", "c", "a"}, c.rotatePriority(10))
	assert.Equal(t, []string{"c", "a", "b"}, c.rotatePriority(20))
	assert.Equal(t, []string{"a", "b", "c"}, c.rotatePriority(30), "wraps around after N windows")
}

func TestSteerTokenFromParts(t *testing.T) {
	parts := []string{"", "livesim2", "start_0", "steer_alpha,beta;ttl=20", "testpic_2s", "Manifest.mpd"}
	assert.Equal(t, "steer_alpha,beta;ttl=20", steerTokenFromParts(parts))
	assert.Equal(t, "", steerTokenFromParts([]string{"", "livesim2", "testpic_2s", "Manifest.mpd"}))
}

func TestSteeringBaseURLAndServerURL(t *testing.T) {
	cfg := &ResponseConfig{
		Host:     "https://h.example",
		URLParts: []string{"", "livesim2", "steer_alpha,beta;ttl=20", "testpic_2s", "Manifest.mpd"},
	}
	// The cdn_/sid_ tokens are injected right after /livesim2, the rest of the path is kept.
	assert.Equal(t,
		"https://h.example/livesim2/cdn_alpha/sid_s1/steer_alpha,beta;ttl=20/testpic_2s/",
		steeringBaseURL(cfg, "alpha", "s1"))
	assert.Equal(t,
		"https://h.example/steering/steer_alpha,beta;ttl=20?sessionId=s1",
		steeringServerURL(cfg.Host, "steer_alpha,beta;ttl=20", "s1"))
}

func TestSteeringServerPathWithGroup(t *testing.T) {
	// Without a group token, the steering path is just the steer_ token.
	assert.Equal(t, "steer_alpha,beta;ttl=20",
		steeringServerPath([]string{"", "livesim2", "steer_alpha,beta;ttl=20", "testpic_2s", "Manifest.mpd"}))
	// With a csid_ group token, it precedes the steer_ token so the client re-polls under the group.
	assert.Equal(t, "csid_groupA/steer_alpha,beta;ttl=20",
		steeringServerPath([]string{"", "livesim2", "csid_groupA", "steer_alpha,beta;ttl=20", "testpic_2s", "Manifest.mpd"}))

	// The csid_ token also rides along on the per-CDN BaseURL (it is part of the kept path parts).
	cfg := &ResponseConfig{
		Host:     "https://h.example",
		URLParts: []string{"", "livesim2", "csid_groupA", "steer_alpha,beta;ttl=20", "testpic_2s", "Manifest.mpd"},
	}
	assert.Equal(t,
		"https://h.example/livesim2/cdn_alpha/sid_s1/csid_groupA/steer_alpha,beta;ttl=20/testpic_2s/",
		steeringBaseURL(cfg, "alpha", "s1"))
}

func TestAddContentSteering(t *testing.T) {
	cfg := &ResponseConfig{
		Host:           "https://h.example",
		URLParts:       []string{"", "livesim2", "steer_alpha,beta;ttl=20;qbs=1;default=beta", "testpic_2s", "Manifest.mpd"},
		SteerSessionID: "s1",
	}
	var err error
	cfg.Steer, err = CreateSteeringConfig("alpha,beta;ttl=20;qbs=1;default=beta")
	require.NoError(t, err)

	mpd := m.NewMPD("dynamic")
	period := m.NewPeriod()
	addContentSteering(mpd, period, cfg)

	require.NotNil(t, mpd.ContentSteering)
	assert.Equal(t, "beta alpha", mpd.ContentSteering.DefaultServiceLocation, "default moved to front")
	assert.True(t, mpd.ContentSteering.QueryBeforeStart)
	assert.Equal(t, "https://h.example/steering/steer_alpha,beta;ttl=20;qbs=1;default=beta?sessionId=s1",
		mpd.ContentSteering.Value)

	require.Len(t, period.BaseURLs, 2)
	assert.Equal(t, "alpha", period.BaseURLs[0].ServiceLocation)
	assert.Equal(t, "https://h.example/livesim2/cdn_alpha/sid_s1/steer_alpha,beta;ttl=20;qbs=1;default=beta/testpic_2s/",
		string(period.BaseURLs[0].Value))
	assert.Equal(t, "beta", period.BaseURLs[1].ServiceLocation)
}
