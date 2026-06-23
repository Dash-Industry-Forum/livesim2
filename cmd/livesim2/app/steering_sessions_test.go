// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSteeringRecordSegmentAndGet(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, _ := fixedClock(time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC))
	m.now = now

	_, ok := m.Get("s1")
	assert.False(t, ok, "unknown session")

	m.RecordSegment("s1", "", "alpha", "/V300/1.m4s")
	m.RecordSegment("s1", "", "alpha", "/V300/2.m4s")
	m.RecordSegment("s1", "", "beta", "/A48/3.m4s")

	s, ok := m.Get("s1")
	require.True(t, ok)
	assert.Equal(t, 2, s.SegmentCounts["alpha"])
	assert.Equal(t, 1, s.SegmentCounts["beta"])
	// The last address used and the last segment fetched are tracked.
	assert.Equal(t, "beta", s.LastLocation)
	assert.Equal(t, "/A48/3.m4s", s.LastSegment)

	// Get returns a copy: mutating it must not affect the store.
	s.SegmentCounts["alpha"] = 99
	s2, _ := m.Get("s1")
	assert.Equal(t, 2, s2.SegmentCounts["alpha"], "store unaffected by caller mutation")
}

func TestSteeringComputeAndRecordRotate(t *testing.T) {
	m := NewSteeringSessionMgr()
	// Pin the clock to a TTL boundary so the rotation is deterministic.
	now, adv := fixedClock(time.Unix(100, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"a", "b", "c"}, TTL: 10, Mode: steeringModeRotate}

	// bucket = floor(100/10) mod 3 = 10 mod 3 = 1 -> rotate one step.
	p := m.ComputeAndRecord("s1", "", cfg, "", "")
	assert.Equal(t, []string{"b", "c", "a"}, p)

	adv(10 * time.Second) // next TTL window -> bucket 2
	p = m.ComputeAndRecord("s1", "", cfg, "a", "1200000")
	assert.Equal(t, []string{"c", "a", "b"}, p)

	s, _ := m.Get("s1")
	assert.Equal(t, "rotate", s.Mode)
	assert.Equal(t, 2, s.SteeringReqCnt)
	require.Len(t, s.Events, 2)
	assert.Equal(t, "a", s.Events[1].Pathway, "client _DASH_pathway is recorded")
	assert.Equal(t, "1200000", s.Events[1].Throughput)
}

func TestVerifySteeringPoll(t *testing.T) {
	cfg := &SteeringConfig{CDNs: []string{"alpha", "beta"}, TTL: 10, Mode: steeringModeTrigger}
	cases := []struct {
		name          string
		pathway, tput string
		wantIssue     bool
		wantSubstr    string
	}{
		// _DASH_pathway is a per-pathway measurement report; its order and which entry is "active"
		// are not judged here (that is done from segment requests). Only the format is checked.
		{"single pathway + throughput", "alpha", "1200000", false, ""},
		{"multi-pathway throughput report (not faulted)", "beta,alpha", "802522000,647618000", false, ""},
		{"empty (queryBeforeStart)", "", "", false, ""},
		{"quotes trimmed", `"alpha"`, "1200000", false, ""},
		{"unknown pathway", "gamma", "1200000", true, "not a configured service location"},
		{"non-numeric throughput", "alpha", "abc", true, "non-negative integer"},
		{"negative throughput", "alpha", "-5", true, "non-negative integer"},
		{"count mismatch", "alpha,beta", "1200000", true, "entries but _DASH_throughput"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := verifySteeringPoll(cfg, c.pathway, c.tput)
			if !c.wantIssue {
				assert.Empty(t, issues, "expected a well-formed message")
				return
			}
			require.NotEmpty(t, issues)
			assert.Contains(t, strings.Join(issues, " | "), c.wantSubstr)
		})
	}
}

func TestSteeringComputeAndRecordVerifies(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, _ := fixedClock(time.Unix(105, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"alpha", "beta"}, TTL: 10, Mode: steeringModeTrigger}

	// First poll: client is on the MPD default top "alpha" -> conformant, no issues recorded.
	m.ComputeAndRecord("s1", "", cfg, "alpha", "1200000")
	s, _ := m.Get("s1")
	assert.Equal(t, 0, s.IssueCount)
	require.Len(t, s.Events, 1)
	assert.Empty(t, s.Events[0].Issues)

	// Second poll: bogus pathway + non-numeric throughput -> issues recorded on the event and rolled
	// up into the session IssueCount.
	m.ComputeAndRecord("s1", "", cfg, "gamma", "x")
	s, _ = m.Get("s1")
	assert.Greater(t, s.IssueCount, 0)
	require.Len(t, s.Events, 2)
	assert.NotEmpty(t, s.Events[1].Issues)
}

func TestSteeringOffPathwayFromSegments(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, adv := fixedClock(time.Unix(105, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"alpha", "beta"}, TTL: 10, Mode: steeringModeTrigger}

	// Client polls and is served top "alpha", then fetches from alpha -> conformant.
	m.ComputeAndRecord("s1", "", cfg, "alpha", "1000")
	m.RecordSegment("s1", "", "alpha", "/V300/1.m4s")
	s, _ := m.Get("s1")
	assert.False(t, s.OffPathway)
	assert.Equal(t, 0, s.IssueCount)

	// The operator switches the client to beta; the client receives it on its next poll.
	_, ok := m.Switch("s1", "beta")
	require.True(t, ok)
	m.ComputeAndRecord("s1", "", cfg, "beta", "1000") // serves [beta, alpha]; servedTop becomes beta

	// Shortly after, the client is still fetching from alpha — within the grace, not faulted (it has
	// not had time to drain its buffer and switch).
	adv(3 * time.Second)
	m.RecordSegment("s1", "", "alpha", "/V300/2.m4s")
	s, _ = m.Get("s1")
	assert.False(t, s.OffPathway, "off-pathway fetch within the grace is not faulted")
	assert.Equal(t, 0, s.IssueCount)

	// Well past the grace it is STILL fetching from alpha -> it ignored the steering decision.
	adv(steeringConvergeGrace + time.Second)
	m.RecordSegment("s1", "", "alpha", "/V300/3.m4s")
	s, _ = m.Get("s1")
	assert.True(t, s.OffPathway, "still on the wrong CDN past the grace -> off-pathway")
	assert.Equal(t, 1, s.IssueCount, "one issue per off-pathway episode")
	require.NotEmpty(t, s.Events)
	last := s.Events[len(s.Events)-1]
	assert.Equal(t, SteeringEventOffPathway, last.Kind)
	assert.NotEmpty(t, last.Issues)

	// A second off-pathway segment does not pile up more issues (one per episode).
	m.RecordSegment("s1", "", "alpha", "/V300/4.m4s")
	s, _ = m.Get("s1")
	assert.Equal(t, 1, s.IssueCount)

	// The client finally moves to beta -> back on the steered pathway, off-pathway cleared.
	m.RecordSegment("s1", "", "beta", "/V300/5.m4s")
	s, _ = m.Get("s1")
	assert.False(t, s.OffPathway, "fetching from the steered CDN clears the off-pathway state")
}

func TestSteeringGroupSharedDecision(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, _ := fixedClock(time.Unix(105, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"alpha", "beta", "gamma"}, TTL: 10, Mode: steeringModeTrigger}

	// Two sessions in group "g1": both are served the same (default) order.
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, m.ComputeAndRecord("a", "g1", cfg, "alpha", "100"))
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, m.ComputeAndRecord("b", "g1", cfg, "alpha", "200"))

	// Switching the group changes the decision for every member.
	p, ok := m.SwitchGroup("g1", "beta")
	require.True(t, ok)
	assert.Equal(t, []string{"beta", "alpha", "gamma"}, p)
	assert.Equal(t, []string{"beta", "alpha", "gamma"}, m.ComputeAndRecord("a", "g1", cfg, "alpha", "100"))
	assert.Equal(t, []string{"beta", "alpha", "gamma"}, m.ComputeAndRecord("b", "g1", cfg, "alpha", "200"))

	// Segment requests are attributed per session; the group aggregates them.
	m.RecordSegment("a", "g1", "beta", "/V300/1.m4s")
	m.RecordSegment("b", "g1", "beta", "/V300/1.m4s")
	m.RecordSegment("b", "g1", "alpha", "/V300/2.m4s")

	g, ok := m.GetGroup("g1")
	require.True(t, ok)
	assert.Equal(t, 2, g.MemberCount)
	assert.True(t, g.ManualOverride)
	assert.Equal(t, []string{"beta", "alpha", "gamma"}, g.CurrentPriority)
	assert.Equal(t, 2, g.SegmentCounts["beta"])
	assert.Equal(t, 1, g.SegmentCounts["alpha"])
	require.Len(t, g.Members, 2)

	// The list view carries the member count but omits the member list.
	groups := m.ListGroups()
	require.Len(t, groups, 1)
	assert.Equal(t, "g1", groups[0].CSID)
	assert.Equal(t, 2, groups[0].MemberCount)
	assert.Nil(t, groups[0].Members)

	// Unknown group cannot be switched.
	_, ok = m.SwitchGroup("nope", "next")
	assert.False(t, ok)

	// Clearing the group removes it and its member sessions.
	assert.True(t, m.ClearGroup("g1"))
	_, ok = m.GetGroup("g1")
	assert.False(t, ok)
	_, ok = m.Get("a")
	assert.False(t, ok, "member sessions removed with the group")
}

func TestSteeringComputeAndRecordManual(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, _ := fixedClock(time.Unix(105, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"a", "b"}, TTL: 10, Mode: steeringModeTrigger, Default: "a"}

	// Manual mode ignores the wall clock: priority stays at the default order across polls.
	assert.Equal(t, []string{"a", "b"}, m.ComputeAndRecord("s1", "", cfg, "", ""))
	assert.Equal(t, []string{"a", "b"}, m.ComputeAndRecord("s1", "", cfg, "", ""))
}

func TestSteeringSwitch(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, _ := fixedClock(time.Unix(105, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"a", "b", "c"}, TTL: 10, Mode: steeringModeTrigger}

	// Unknown session cannot be switched.
	_, ok := m.Switch("nope", "next")
	assert.False(t, ok)

	m.ComputeAndRecord("s1", "", cfg, "", "") // establishes the session with CDNs a,b,c

	// "next" advances one step.
	p, ok := m.Switch("s1", "next")
	require.True(t, ok)
	assert.Equal(t, []string{"b", "c", "a"}, p)
	// The pinned order is now returned by subsequent polls (manual override).
	assert.Equal(t, []string{"b", "c", "a"}, m.ComputeAndRecord("s1", "", cfg, "", ""))

	// A named target moves it to the front.
	p, ok = m.Switch("s1", "c")
	require.True(t, ok)
	assert.Equal(t, []string{"c", "b", "a"}, p)

	// An unknown target is rejected.
	_, ok = m.Switch("s1", "zzz")
	assert.False(t, ok)
}

func TestSteeringSwitchOverridesRotate(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, adv := fixedClock(time.Unix(100, 0).UTC())
	m.now = now
	cfg := &SteeringConfig{CDNs: []string{"a", "b"}, TTL: 10, Mode: steeringModeRotate}

	m.ComputeAndRecord("s1", "", cfg, "", "")
	p, ok := m.Switch("s1", "a")
	require.True(t, ok)
	assert.Equal(t, []string{"a", "b"}, p)

	// Even though rotate mode would rotate at the next TTL window, the manual override holds.
	adv(10 * time.Second)
	assert.Equal(t, []string{"a", "b"}, m.ComputeAndRecord("s1", "", cfg, "", ""))
}

func TestSteeringSessionEviction(t *testing.T) {
	m := NewSteeringSessionMgr()
	m.maxSessions = 2
	now, adv := fixedClock(time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC))
	m.now = now

	m.RecordSegment("s1", "", "a", "")
	adv(time.Second)
	m.RecordSegment("s2", "", "a", "")
	adv(time.Second)
	m.RecordSegment("s3", "", "a", "") // exceeds the cap -> oldest (s1) evicted

	_, ok := m.Get("s1")
	assert.False(t, ok, "oldest session evicted by the cap")
	_, ok = m.Get("s3")
	assert.True(t, ok)

	// TTL eviction: advance past the TTL and s3 should be gone.
	adv(steeringSessionTTL + time.Minute)
	_, ok = m.Get("s3")
	assert.False(t, ok, "session expired by TTL")
}

func TestSteeringListAndClear(t *testing.T) {
	m := NewSteeringSessionMgr()
	now, adv := fixedClock(time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC))
	m.now = now
	m.RecordSegment("s1", "", "a", "")
	adv(time.Second)
	m.RecordSegment("s2", "", "b", "")

	list := m.List()
	require.Len(t, list, 2)
	assert.Equal(t, "s2", list[0].Sid, "most-recently-active first")
	assert.Nil(t, list[0].Events, "list omits the timeline")

	assert.True(t, m.ClearSession("s1"))
	assert.False(t, m.ClearSession("s1"))
	assert.Equal(t, 1, m.Clear())
	assert.Empty(t, m.List())
}
