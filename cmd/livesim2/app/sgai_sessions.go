// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"sync"
	"time"
)

// SGAI session tracking. Records the ad decisions and impression beacons seen for each
// session id so they can be inspected via the API and the live status page — useful when
// livesim2 runs as a public server where the operator cannot tail the process log.
//
// The store is bounded (maxSessions, maxEventsPerSession) and time-limited (ttl) because
// the session id is client-supplied and the server is shared: it must not grow without
// limit. State is in-memory and per-process (not shared across instances behind a load
// balancer). The design mirrors the cmaf-ingest ChannelMgr (RWMutex + map).

const (
	sgaiDefaultMaxSessions         = 2000
	sgaiDefaultMaxEventsPerSession = 200
	sgaiDefaultSessionTTL          = 30 * time.Minute
	// sgaiDedupWindow collapses an identical event re-fired for the same ad occurrence.
	// A player may re-resolve the alternative MPD on each live manifest refresh and re-fire
	// the same impression/quartile; for accurate per-view reporting each (adId,event) — and
	// each repeated decision pod — is counted once within this window. It is short enough that
	// the same ad shown again in a later break counts anew.
	sgaiDedupWindow = 25 * time.Second
)

// SgaiEventKind is the kind of recorded session event.
type SgaiEventKind string

const (
	SgaiEventDecision SgaiEventKind = "decision" // an /sgai/ads ad-decision response
	SgaiEventBeacon   SgaiEventKind = "beacon"   // an /sgai/beacon impression/tracking hit
)

// SgaiEvent is a single recorded event in a session timeline.
type SgaiEvent struct {
	Time time.Time     `json:"time" doc:"When the event was recorded (server time)"`
	Kind SgaiEventKind `json:"kind" doc:"Event kind: decision or beacon"`
	// Decision fields
	Interests string   `json:"interests,omitempty" doc:"Interest steering value(s) used for this decision (comma-separated)"`
	Pod       []string `json:"pod,omitempty" doc:"Ad pod returned (ad ids in order) for a decision"`
	// Beacon fields
	AdID  string `json:"adId,omitempty" doc:"Ad id the beacon was fired for"`
	Event string `json:"event,omitempty" doc:"Beacon event type, e.g. impression"`
	EvID  string `json:"evId,omitempty" doc:"Break/avail event id the beacon is attributed to, if any"`
	CMCD  string `json:"cmcd,omitempty" doc:"CMCD payload carried on the beacon, if any"`
}

// SgaiSession is the recorded state for one session id.
type SgaiSession struct {
	Sid         string      `json:"sid" doc:"Session id"`
	Interests   string      `json:"interests,omitempty" doc:"Most recent interest steering value(s) (comma-separated)"`
	CreatedAt   time.Time   `json:"createdAt" doc:"When the session was first seen"`
	LastSeen    time.Time   `json:"lastSeen" doc:"When the session was last active"`
	DecisionCnt int         `json:"decisionCount" doc:"Number of ad decisions made"`
	BeaconCnt   int         `json:"beaconCount" doc:"Number of beacons received"`
	Events      []SgaiEvent `json:"events" doc:"Timeline of decisions and beacons (oldest first)"`
}

// SgaiSessionMgr is a bounded, time-limited store of SGAI session activity.
type SgaiSessionMgr struct {
	mu          sync.RWMutex
	sessions    map[string]*SgaiSession
	maxSessions int
	maxEvents   int
	ttl         time.Duration
	now         func() time.Time // injectable for tests
}

// NewSgaiSessionMgr creates a session manager with the default bounds.
func NewSgaiSessionMgr() *SgaiSessionMgr {
	return &SgaiSessionMgr{
		sessions:    make(map[string]*SgaiSession),
		maxSessions: sgaiDefaultMaxSessions,
		maxEvents:   sgaiDefaultMaxEventsPerSession,
		ttl:         sgaiDefaultSessionTTL,
		now:         time.Now,
	}
}

// getOrCreate returns the session for sid, creating it if needed. Caller must hold mu.
func (m *SgaiSessionMgr) getOrCreate(sid string, ts time.Time) *SgaiSession {
	s, ok := m.sessions[sid]
	if !ok {
		s = &SgaiSession{Sid: sid, CreatedAt: ts}
		m.sessions[sid] = s
	}
	s.LastSeen = ts
	return s
}

// appendEvent adds an event to a session, trimming to maxEvents (drop oldest). Caller holds mu.
func (m *SgaiSessionMgr) appendEvent(s *SgaiSession, e SgaiEvent) {
	s.Events = append(s.Events, e)
	if m.maxEvents > 0 && len(s.Events) > m.maxEvents {
		s.Events = s.Events[len(s.Events)-m.maxEvents:]
	}
}

// RecordDecision records an /sgai/ads ad decision (pod) for a session. interests is the raw
// (possibly comma-separated) interest steering value used for the decision.
func (m *SgaiSessionMgr) RecordDecision(sid, interests string, pod []string) {
	if sid == "" {
		sid = "anon"
	}
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getOrCreate(sid, ts)
	if interests != "" {
		s.Interests = interests
	}
	podCopy := append([]string(nil), pod...)
	// Dedup a repeated decision for the same pod within the window (e.g. a player that
	// re-resolves the alternative MPD on each manifest refresh).
	if m.recentDuplicateDecision(s, podCopy, ts) {
		return
	}
	s.DecisionCnt++
	m.appendEvent(s, SgaiEvent{Time: ts, Kind: SgaiEventDecision, Interests: interests, Pod: podCopy})
	m.evictLocked(ts)
}

// RecordBeacon records an /sgai/beacon impression/tracking hit for a session. evID is the
// break/avail event id the beacon is attributed to (from ?evId=/?break=), "" if absent.
func (m *SgaiSessionMgr) RecordBeacon(sid, adID, event, cmcd, evID string) {
	if sid == "" {
		sid = "anon"
	}
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getOrCreate(sid, ts)
	// Dedup the same (adId,event) beacon re-fired for one ad occurrence within the window.
	// The break/avail id (evID) is part of the key, so the same ad shown again in a later
	// break — a distinct occurrence with a new evID — is counted anew even when the breaks
	// are closer together than the dedup window.
	if m.recentDuplicateBeacon(s, adID, event, evID, ts) {
		return
	}
	s.BeaconCnt++
	m.appendEvent(s, SgaiEvent{Time: ts, Kind: SgaiEventBeacon, AdID: adID, Event: event, EvID: evID, CMCD: cmcd})
	m.evictLocked(ts)
}

// recentDuplicateBeacon reports whether an identical beacon (same adId+event for the same
// break occurrence evID) was recorded for this session within sgaiDedupWindow. Caller must
// hold mu. When no break id is carried (evID == "") it degrades to the prior (adId,event)
// behavior, collapsing all such beacons within the window.
func (m *SgaiSessionMgr) recentDuplicateBeacon(s *SgaiSession, adID, event, evID string, ts time.Time) bool {
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if ts.Sub(e.Time) > sgaiDedupWindow {
			return false
		}
		if e.Kind == SgaiEventBeacon && e.AdID == adID && e.Event == event && e.EvID == evID {
			return true
		}
	}
	return false
}

// recentDuplicateDecision reports whether a decision for the same pod was recorded for this
// session within sgaiDedupWindow. Caller must hold mu.
func (m *SgaiSessionMgr) recentDuplicateDecision(s *SgaiSession, pod []string, ts time.Time) bool {
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if ts.Sub(e.Time) > sgaiDedupWindow {
			return false
		}
		if e.Kind == SgaiEventDecision && samePod(e.Pod, pod) {
			return true
		}
	}
	return false
}

// samePod reports whether two ad pods (ordered ad-id lists) are equal.
func samePod(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Get returns a deep copy of the session for sid (so callers can read it without the lock),
// dropping it if it has expired.
func (m *SgaiSessionMgr) Get(sid string) (*SgaiSession, bool) {
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok {
		return nil, false
	}
	if m.ttl > 0 && ts.Sub(s.LastSeen) > m.ttl {
		delete(m.sessions, sid)
		return nil, false
	}
	return s.clone(), true
}

// Clear removes all recorded sessions and returns the number removed. Useful to get a
// clean slate.
func (m *SgaiSessionMgr) Clear() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.sessions)
	m.sessions = make(map[string]*SgaiSession)
	return n
}

// ClearSession removes a single session by id, returning true if it existed.
func (m *SgaiSessionMgr) ClearSession(sid string) bool {
	if sid == "" {
		sid = "anon"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sid]; ok {
		delete(m.sessions, sid)
		return true
	}
	return false
}

// List returns summaries (no event timelines) of the live sessions, most-recent first.
func (m *SgaiSessionMgr) List() []SgaiSession {
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictLocked(ts)
	out := make([]SgaiSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		summary := *s
		summary.Events = nil // omit the timeline in the list view
		out = append(out, summary)
	}
	// most-recently-active first (simple insertion sort; the set is small)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].LastSeen.After(out[j-1].LastSeen); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// evictLocked drops expired sessions and enforces the maxSessions cap (oldest LastSeen
// first). Caller must hold mu.
func (m *SgaiSessionMgr) evictLocked(ts time.Time) {
	if m.ttl > 0 {
		for sid, s := range m.sessions {
			if ts.Sub(s.LastSeen) > m.ttl {
				delete(m.sessions, sid)
			}
		}
	}
	if m.maxSessions <= 0 {
		return
	}
	for len(m.sessions) > m.maxSessions {
		var oldestSid string
		var oldest time.Time
		first := true
		for sid, s := range m.sessions {
			if first || s.LastSeen.Before(oldest) {
				oldestSid, oldest, first = sid, s.LastSeen, false
			}
		}
		delete(m.sessions, oldestSid)
	}
}

// clone deep-copies a session (including each event's Pod slice) so it can be read and
// mutated outside the lock without affecting the store.
func (s *SgaiSession) clone() *SgaiSession {
	c := *s
	c.Events = make([]SgaiEvent, len(s.Events))
	for i, e := range s.Events {
		if e.Pod != nil {
			e.Pod = append([]string(nil), e.Pod...)
		}
		c.Events[i] = e
	}
	return &c
}
