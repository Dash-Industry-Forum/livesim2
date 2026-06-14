// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedClock returns a now() function whose value can be advanced by the test.
func fixedClock(start time.Time) (func() time.Time, func(d time.Duration)) {
	cur := start
	return func() time.Time { return cur }, func(d time.Duration) { cur = cur.Add(d) }
}

func TestSgaiSessionRecordAndGet(t *testing.T) {
	m := NewSgaiSessionMgr()
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	_, ok := m.Get("alice")
	assert.False(t, ok, "unknown session")

	m.RecordDecision("alice", "sports", []string{"ad1", "ad2", "ad0"})
	adv(time.Second)
	m.RecordBeacon("alice", "ad1", "impression", "sid=alice", "29689640")

	s, ok := m.Get("alice")
	require.True(t, ok)
	assert.Equal(t, "alice", s.Sid)
	assert.Equal(t, "sports", s.Interests)
	assert.Equal(t, 1, s.DecisionCnt)
	assert.Equal(t, 1, s.BeaconCnt)
	require.Len(t, s.Events, 2)
	assert.Equal(t, SgaiEventDecision, s.Events[0].Kind)
	assert.Equal(t, []string{"ad1", "ad2", "ad0"}, s.Events[0].Pod)
	assert.Equal(t, SgaiEventBeacon, s.Events[1].Kind)
	assert.Equal(t, "ad1", s.Events[1].AdID)
	assert.Equal(t, "impression", s.Events[1].Event)
	assert.Equal(t, "29689640", s.Events[1].EvID, "break/avail event id is recorded")

	// Get returns a copy: mutating it must not affect the store.
	s.Events[0].Pod[0] = "MUT"
	s2, _ := m.Get("alice")
	assert.Equal(t, "ad1", s2.Events[0].Pod[0], "store unaffected by caller mutation")
}

func TestSgaiSessionBeaconDedup(t *testing.T) {
	m := NewSgaiSessionMgr()
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	// A player re-firing the same impression within the window is counted once.
	m.RecordBeacon("alice", "ad2", "impression", "", "")
	adv(2 * time.Second)
	m.RecordBeacon("alice", "ad2", "impression", "", "") // duplicate
	adv(1 * time.Second)
	m.RecordBeacon("alice", "ad2", "firstQuartile", "", "") // different event, counts

	s, _ := m.Get("alice")
	assert.Equal(t, 2, s.BeaconCnt, "duplicate impression collapsed, quartile counted")

	// The same ad shown again in a later break (outside the window) counts anew.
	adv(sgaiDedupWindow + time.Second)
	m.RecordBeacon("alice", "ad2", "impression", "", "")
	s, _ = m.Get("alice")
	assert.Equal(t, 3, s.BeaconCnt, "re-shown ad counts again outside the dedup window")
}

func TestSgaiSessionBeaconDedupAcrossBreaks(t *testing.T) {
	m := NewSgaiSessionMgr()
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	// The same ad+event for the same break occurrence (evID) is collapsed within the window.
	m.RecordBeacon("alice", "ad2", "impression", "", "100")
	adv(2 * time.Second)
	m.RecordBeacon("alice", "ad2", "impression", "", "100") // re-fire of break 100
	s, _ := m.Get("alice")
	assert.Equal(t, 1, s.BeaconCnt, "re-fire for the same break occurrence collapsed")

	// The same ad+event for a different break occurrence counts, even within the window —
	// closely-spaced breaks (period < dedup window) must not undercount distinct impressions.
	adv(2 * time.Second)
	m.RecordBeacon("alice", "ad2", "impression", "", "101") // distinct break 101
	s, _ = m.Get("alice")
	assert.Equal(t, 2, s.BeaconCnt, "distinct break occurrence counts within the window")
}

func TestSgaiSessionDecisionDedup(t *testing.T) {
	m := NewSgaiSessionMgr()
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	m.RecordDecision("alice", "", []string{"ad2", "ad0", "ad1"})
	adv(2 * time.Second)
	m.RecordDecision("alice", "", []string{"ad2", "ad0", "ad1"}) // re-resolution, same pod
	s, _ := m.Get("alice")
	assert.Equal(t, 1, s.DecisionCnt, "repeated same-pod decision within window collapsed")

	// A genuinely different pod is a new decision.
	adv(1 * time.Second)
	m.RecordDecision("alice", "", []string{"ad0", "ad1", "ad2"})
	s, _ = m.Get("alice")
	assert.Equal(t, 2, s.DecisionCnt)
}

func TestSgaiSessionEmptySidBecomesAnon(t *testing.T) {
	m := NewSgaiSessionMgr()
	m.RecordBeacon("", "ad0", "impression", "", "")
	_, ok := m.Get("anon")
	assert.True(t, ok, "empty sid is stored under 'anon'")
}

func TestSgaiSessionMaxEventsTrim(t *testing.T) {
	m := NewSgaiSessionMgr()
	m.maxEvents = 3
	for i := range 10 {
		m.RecordBeacon("alice", fmt.Sprintf("ad%d", i), "impression", "", "")
	}
	s, _ := m.Get("alice")
	require.Len(t, s.Events, 3, "trimmed to maxEvents")
	assert.Equal(t, "ad7", s.Events[0].AdID, "oldest dropped, newest kept")
	assert.Equal(t, "ad9", s.Events[2].AdID)
	assert.Equal(t, 10, s.BeaconCnt, "count is not trimmed")
}

func TestSgaiSessionTTLEviction(t *testing.T) {
	m := NewSgaiSessionMgr()
	m.ttl = 10 * time.Minute
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	m.RecordDecision("alice", "", []string{"ad0"})
	adv(11 * time.Minute)
	_, ok := m.Get("alice")
	assert.False(t, ok, "expired session is dropped on Get")
}

func TestSgaiSessionMaxSessionsCap(t *testing.T) {
	m := NewSgaiSessionMgr()
	m.maxSessions = 2
	m.ttl = 0 // disable TTL for this test
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	m.RecordDecision("a", "", []string{"ad0"}) // oldest
	adv(time.Second)
	m.RecordDecision("b", "", []string{"ad0"})
	adv(time.Second)
	m.RecordDecision("c", "", []string{"ad0"}) // evicts "a"

	_, okA := m.Get("a")
	_, okB := m.Get("b")
	_, okC := m.Get("c")
	assert.False(t, okA, "oldest-LastSeen session evicted at cap")
	assert.True(t, okB)
	assert.True(t, okC)
}

func TestSgaiSessionListOrderAndNoTimelines(t *testing.T) {
	m := NewSgaiSessionMgr()
	now, adv := fixedClock(time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC))
	m.now = now

	m.RecordDecision("alice", "", []string{"ad0"})
	adv(time.Second)
	m.RecordDecision("bob", "news", []string{"ad1"})

	list := m.List()
	require.Len(t, list, 2)
	assert.Equal(t, "bob", list[0].Sid, "most-recently-active first")
	assert.Equal(t, "alice", list[1].Sid)
	assert.Nil(t, list[0].Events, "list omits event timelines")
	assert.Equal(t, "news", list[0].Interests)
}
