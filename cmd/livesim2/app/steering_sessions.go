// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Content-Steering session tracking. Records, per client-supplied session id, how many segment
// requests each "CDN" (service location) has served and the steering polls/switches seen, so the
// per-endpoint request distribution and the current pathway priority can be inspected via the API
// and the live status page — useful when livesim2 runs as a public server where the operator
// cannot tail the process log.
//
// Sessions may be grouped under a content-steering group id (csid): the steering decision (pinned
// priority + manual override) is then owned by the group and shared by all members, so one switch
// moves every member, while segment counts, poll timelines and the _DASH_pathway verification stay
// per-session. A session with no csid is a group of one and owns its own decision (as before).
//
// The store is bounded (maxSessions) and time-limited (ttl) because the session id is
// client-supplied and the server is shared: it must not grow without limit. State is in-memory
// and per-process. The design mirrors SgaiSessionMgr (RWMutex + map).

const (
	steeringDefaultMaxSessions = 2000
	steeringDefaultMaxEvents   = 200
	steeringSessionTTL         = 30 * time.Minute
	// steeringConvergeGrace is how long a client is given to move to a newly-served top pathway
	// before a "client ignored steering" mismatch is reported. It absorbs the normal lag between
	// the server changing the steered CDN and the client fetching the new manifest and switching.
	steeringConvergeGrace = 10 * time.Second
)

// SteeringEventKind is the kind of recorded steering-session event.
type SteeringEventKind string

const (
	SteeringEventSteering   SteeringEventKind = "steering"   // a steering-manifest poll
	SteeringEventSwitch     SteeringEventKind = "switch"     // an API-triggered priority change
	SteeringEventOffPathway SteeringEventKind = "offPathway" // client still fetching from a non-steered CDN past the grace
)

// SteeringEvent is a single recorded event in a session timeline.
type SteeringEvent struct {
	Time       time.Time         `json:"time" doc:"When the event was recorded (server time)"`
	Kind       SteeringEventKind `json:"kind" doc:"Event kind: steering or switch"`
	Priority   []string          `json:"priority" doc:"PATHWAY-PRIORITY returned (steering) or set (switch)"`
	Pathway    string            `json:"pathway,omitempty" doc:"Client _DASH_pathway value observed on a steering poll"`
	Throughput string            `json:"throughput,omitempty" doc:"Client _DASH_throughput value observed on a steering poll"`
	//nolint:lll
	Issues []string `json:"issues,omitempty" doc:"Conformance problems found verifying the client _DASH_pathway/_DASH_throughput message (empty if well-formed and following steering)"`
}

// SteeringSession is the recorded state for one session id.
type SteeringSession struct {
	Sid             string         `json:"sid" doc:"Session id"`
	CSID            string         `json:"csid,omitempty" doc:"Content-steering group id this session belongs to (empty = group of one)"`
	CreatedAt       time.Time      `json:"createdAt" doc:"When the session was first seen"`
	LastSeen        time.Time      `json:"lastSeen" doc:"When the session was last active"`
	CDNs            []string       `json:"cdns" doc:"Configured service locations, in declared order"`
	Mode            string         `json:"mode" doc:"Steering mode: rotate or trigger"`
	SegmentCounts   map[string]int `json:"segmentCounts" doc:"Number of segment requests served per service location"`
	SteeringReqCnt  int            `json:"steeringRequestCount" doc:"Number of steering-manifest polls received"`
	CurrentPriority []string       `json:"currentPriority" doc:"Most recent PATHWAY-PRIORITY served"`
	ManualOverride  bool           `json:"manualOverride" doc:"Whether a switch API call has pinned the priority"`
	LastPolledAt    time.Time      `json:"lastPolledAt,omitzero" doc:"When the client last fetched the steering manifest"`
	LastLocation    string         `json:"lastLocation,omitempty" doc:"Service location (CDN) of the most recent segment request"`
	LastSegment     string         `json:"lastSegment,omitempty" doc:"Path of the most recent segment the client fetched"`
	//nolint:lll
	OffPathway bool `json:"offPathway,omitempty" doc:"Whether the client is still fetching from a non-steered CDN past the convergence grace (i.e. ignoring the steering decision)"`
	//nolint:lll
	IssueCount int             `json:"issueCount" doc:"Total conformance issues: malformed client messages plus off-pathway episodes (0 = conformant)"`
	Events     []SteeringEvent `json:"events" doc:"Timeline of steering polls, switches and off-pathway detections (oldest first)"`

	// servedTop is the top PATHWAY-PRIORITY entry currently served to the client and servedTopSince
	// is when it was first served (i.e. when the client received it); together they drive the
	// convergence grace for the segment-based off-pathway check. Unexported: internal state.
	servedTop      string
	servedTopSince time.Time
}

// SteeringGroup is the shared steering decision for a content-steering group (csid): a set of
// sessions that switch together. The decision fields (CurrentPriority/ManualOverride/Mode/CDNs) are
// stored; the aggregate fields (MemberCount/SegmentCounts/SteeringRequestCount/IssueCount and, in
// the detail view, Members) are summed over the group's member sessions when the group is read.
type SteeringGroup struct {
	CSID            string         `json:"csid" doc:"Content-steering group id"`
	CreatedAt       time.Time      `json:"createdAt" doc:"When the group was first seen"`
	LastSeen        time.Time      `json:"lastSeen" doc:"When the group's decision was last served or changed"`
	CDNs            []string       `json:"cdns" doc:"Configured service locations, in declared order"`
	Mode            string         `json:"mode" doc:"Steering mode: rotate or trigger"`
	CurrentPriority []string       `json:"currentPriority" doc:"Current shared PATHWAY-PRIORITY served to every member"`
	ManualOverride  bool           `json:"manualOverride" doc:"Whether a group switch has pinned the priority"`
	MemberCount     int            `json:"memberCount" doc:"Number of sessions in the group"`
	SegmentCounts   map[string]int `json:"segmentCounts" doc:"Segment requests per service location, summed over members"`
	SteeringReqCnt  int            `json:"steeringRequestCount" doc:"Steering polls, summed over members"`
	IssueCount      int            `json:"issueCount" doc:"Client-message conformance issues, summed over members"`
	//nolint:lll
	Members []SteeringSession `json:"members,omitempty" doc:"Member sessions, most-recently-active first (detail view; event timelines omitted)"`
	Events  []SteeringEvent   `json:"events,omitempty" doc:"Timeline of group switches (detail view; oldest first)"`
}

// SteeringSessionMgr is a bounded, time-limited store of content-steering session and group
// activity.
type SteeringSessionMgr struct {
	mu          sync.RWMutex
	sessions    map[string]*SteeringSession
	groups      map[string]*SteeringGroup
	maxSessions int
	maxEvents   int
	ttl         time.Duration
	now         func() time.Time // injectable for tests
}

// NewSteeringSessionMgr creates a session manager with the default bounds.
func NewSteeringSessionMgr() *SteeringSessionMgr {
	return &SteeringSessionMgr{
		sessions:    make(map[string]*SteeringSession),
		groups:      make(map[string]*SteeringGroup),
		maxSessions: steeringDefaultMaxSessions,
		maxEvents:   steeringDefaultMaxEvents,
		ttl:         steeringSessionTTL,
		now:         time.Now,
	}
}

// getOrCreate returns the session for sid, creating it if needed. Caller must hold mu.
func (m *SteeringSessionMgr) getOrCreate(sid string, ts time.Time) *SteeringSession {
	s, ok := m.sessions[sid]
	if !ok {
		s = &SteeringSession{Sid: sid, CreatedAt: ts, SegmentCounts: make(map[string]int)}
		m.sessions[sid] = s
	}
	s.LastSeen = ts
	return s
}

// appendBoundedEvent appends an event to a timeline, trimming to max (drop oldest) when max > 0.
func appendBoundedEvent(events []SteeringEvent, e SteeringEvent, max int) []SteeringEvent {
	events = append(events, e)
	if max > 0 && len(events) > max {
		events = events[len(events)-max:]
	}
	return events
}

// RecordSegment counts a segment request served for a session by a given service location, records
// the session's content-steering group (csid) if one is supplied, and remembers the address (service
// location) and segment path the client last fetched.
func (m *SteeringSessionMgr) RecordSegment(sid, csid, location, segment string) {
	if sid == "" {
		sid = "anon"
	}
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getOrCreate(sid, ts)
	if csid != "" {
		s.CSID = csid
	}
	if s.SegmentCounts == nil {
		s.SegmentCounts = make(map[string]int)
	}
	s.SegmentCounts[location]++
	s.LastLocation = location
	if segment != "" {
		s.LastSegment = segment
	}
	// Conformance ground truth: once the client has had the grace period since it received a steered
	// top, its segment requests should be going to that CDN. A persistent off-pathway fetch is the
	// real "client ignored steering" signal (recorded once per episode to avoid per-segment spam).
	if s.servedTop != "" {
		switch {
		case location == s.servedTop:
			s.OffPathway = false
		case !s.OffPathway && !s.servedTopSince.IsZero() && ts.Sub(s.servedTopSince) >= steeringConvergeGrace:
			s.OffPathway = true
			s.IssueCount++
			s.Events = appendBoundedEvent(s.Events, SteeringEvent{Time: ts, Kind: SteeringEventOffPathway,
				Issues: []string{fmt.Sprintf("fetching from %q but was steered to %q", location, s.servedTop)}}, m.maxEvents)
		}
	}
	m.evictLocked(ts)
}

// ComputeAndRecord records a steering-manifest poll for a session and returns the PATHWAY-PRIORITY
// to serve. When csid is set the decision is owned by the group (every member is served the same
// order, switched together); otherwise the session owns its own decision. In rotate mode (and absent
// a manual override) the priority is the stateless wall-clock rotation; in trigger mode (or after a
// switch) it is the owner's pinned order. cfg carries the stream's steering configuration (the
// steering endpoint is stateless and rebuilds it from its own URL). pathway/throughput are the
// client-reported _DASH_pathway/_DASH_throughput values, recorded for inspection.
func (m *SteeringSessionMgr) ComputeAndRecord(sid, csid string, cfg *SteeringConfig, pathway, throughput string) []string {
	if sid == "" {
		sid = "anon"
	}
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getOrCreate(sid, ts)
	s.CDNs = append([]string(nil), cfg.CDNs...)
	s.Mode = cfg.Mode
	if csid != "" {
		s.CSID = csid
	}

	// Validate the format of the client's _DASH_pathway/_DASH_throughput message. Whether the client
	// is actually following the steering decision is judged from its segment requests (see
	// RecordSegment), not from _DASH_pathway, which is a per-pathway measurement report.
	issues := verifySteeringPoll(cfg, pathway, throughput)

	var priority []string
	if csid != "" {
		// The decision is shared by the group: resolve and update it there.
		g := m.getOrCreateGroup(csid, cfg, ts)
		priority = resolveSteeringPriority(cfg, g.ManualOverride, g.CurrentPriority, ts.Unix())
		g.CurrentPriority = append([]string(nil), priority...)
	} else {
		priority = resolveSteeringPriority(cfg, s.ManualOverride, s.CurrentPriority, ts.Unix())
	}
	// Record on the session the order it was actually served and when the served top last changed
	// (the moment the client received it — the convergence-grace clock). A new top restarts the
	// window, so any earlier off-pathway flag is cleared.
	s.CurrentPriority = append([]string(nil), priority...)
	if len(priority) > 0 && priority[0] != s.servedTop {
		s.servedTop = priority[0]
		s.servedTopSince = ts
		s.OffPathway = false
	}
	s.LastPolledAt = ts
	s.SteeringReqCnt++
	s.IssueCount += len(issues)
	s.Events = appendBoundedEvent(s.Events, SteeringEvent{Time: ts, Kind: SteeringEventSteering,
		Priority: append([]string(nil), priority...), Pathway: pathway, Throughput: throughput, Issues: issues}, m.maxEvents)
	m.evictLocked(ts)
	return priority
}

// resolveSteeringPriority computes the PATHWAY-PRIORITY to serve given the steering config, the
// decision owner's manual-override flag, and its current pinned order. In trigger mode or under an
// override it returns the pinned order (defaulting to the configured default order if none yet);
// otherwise it returns the stateless wall-clock rotation. The result is always a fresh slice.
func resolveSteeringPriority(cfg *SteeringConfig, override bool, current []string, nowUnix int64) []string {
	if cfg.Mode == steeringModeTrigger || override {
		if len(current) == 0 {
			return cfg.defaultOrder()
		}
		return append([]string(nil), current...)
	}
	return cfg.rotatePriority(nowUnix)
}

// switchOrder reorders base for a switch: target is a service location to move to the front, or
// "next" (or "") to advance one step. Returns the new order and true, or nil/false if target is a
// name not present in base.
func switchOrder(base []string, target string) ([]string, bool) {
	order := append([]string(nil), base...)
	if target == "" || target == "next" {
		if len(order) > 1 {
			order = append(order[1:], order[0])
		}
		return order, true
	}
	if !slices.Contains(order, target) {
		return nil, false
	}
	out := []string{target}
	for _, name := range order {
		if name != target {
			out = append(out, name)
		}
	}
	return out, true
}

// getOrCreateGroup returns the group for csid, creating it if needed, and refreshes its config and
// LastSeen. Caller must hold mu.
func (m *SteeringSessionMgr) getOrCreateGroup(csid string, cfg *SteeringConfig, ts time.Time) *SteeringGroup {
	g, ok := m.groups[csid]
	if !ok {
		g = &SteeringGroup{CSID: csid, CreatedAt: ts}
		m.groups[csid] = g
	}
	g.CDNs = append([]string(nil), cfg.CDNs...)
	g.Mode = cfg.Mode
	g.LastSeen = ts
	return g
}

// splitDASHList splits a content-steering query value (_DASH_pathway or _DASH_throughput) into its
// comma-separated entries, trimming spaces and surrounding double-quotes and dropping empties. The
// two parameters pair positionally: the i-th throughput is the client's measured bitrate on the
// i-th pathway (ETSI TS 103 998 §6.2.3 / DASH-IF Content Steering).
func splitDASHList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []string
	for p := range strings.SplitSeq(v, ",") {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// verifySteeringPoll validates the FORMAT of the _DASH_pathway/_DASH_throughput parameters a client
// appends to a steering poll: each pathway must be a configured service location, each throughput a
// non-negative integer (bits/s), and — since a client (e.g. dash.js) sends them as a positionally-
// paired list of all the pathways it has measured — their counts must match.
//
// It deliberately does NOT judge which pathway the client is "using": _DASH_pathway is a per-pathway
// measurement report, not a declaration of the active pathway (dash.js lists every known pathway,
// in an implementation-defined order, not "the one in use" first). Whether the client actually
// followed the steering decision is determined from its segment requests (the cdn_ token), in
// RecordSegment, which is the ground truth.
func verifySteeringPoll(cfg *SteeringConfig, pathway, throughput string) []string {
	pathways := splitDASHList(pathway)
	throughputs := splitDASHList(throughput)
	var issues []string

	// _DASH_pathway: every reported pathway must be a configured service location.
	for _, p := range pathways {
		if !slices.Contains(cfg.CDNs, p) {
			issues = append(issues, fmt.Sprintf("_DASH_pathway %q is not a configured service location %v", p, cfg.CDNs))
		}
	}
	// _DASH_throughput: every entry must be a non-negative integer (bits per second).
	for _, t := range throughputs {
		if n, err := strconv.Atoi(t); err != nil || n < 0 {
			issues = append(issues, fmt.Sprintf("_DASH_throughput %q is not a non-negative integer", t))
		}
	}
	// The two parameters pair positionally, so their cardinalities must match when both are given.
	if len(pathways) > 0 && len(throughputs) > 0 && len(pathways) != len(throughputs) {
		issues = append(issues, fmt.Sprintf("_DASH_pathway has %d entries but _DASH_throughput has %d",
			len(pathways), len(throughputs)))
	}
	return issues
}

// Switch pins a new priority order for a session: target may be a service location to move to
// the front, or "next" (or "") to advance one step. It sets a manual override so subsequent
// steering polls return the new order regardless of the configured mode. Returns the new
// priority and true, or nil/false if the session is unknown or target is not a known service
// location.
func (m *SteeringSessionMgr) Switch(sid, target string) ([]string, bool) {
	if sid == "" {
		sid = "anon"
	}
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok || len(s.CDNs) == 0 {
		return nil, false
	}
	base := s.CurrentPriority
	if len(base) == 0 {
		base = s.CDNs
	}
	order, ok := switchOrder(base, target)
	if !ok {
		return nil, false
	}
	s.CurrentPriority = order
	s.ManualOverride = true
	s.LastSeen = ts
	s.Events = appendBoundedEvent(s.Events, SteeringEvent{Time: ts, Kind: SteeringEventSwitch,
		Priority: append([]string(nil), order...)}, m.maxEvents)
	return append([]string(nil), order...), true
}

// SwitchGroup pins a new shared priority order for a content-steering group: target is a service
// location to move to the front, or "next" (or "") to advance one step. It sets a manual override
// so subsequent steering polls from every member return the new order regardless of the configured
// mode. Returns the new priority and true, or nil/false if the group is unknown or target is not a
// known service location.
func (m *SteeringSessionMgr) SwitchGroup(csid, target string) ([]string, bool) {
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[csid]
	if !ok || len(g.CDNs) == 0 {
		return nil, false
	}
	base := g.CurrentPriority
	if len(base) == 0 {
		base = g.CDNs
	}
	order, ok := switchOrder(base, target)
	if !ok {
		return nil, false
	}
	g.CurrentPriority = order
	g.ManualOverride = true
	g.LastSeen = ts
	g.Events = appendBoundedEvent(g.Events, SteeringEvent{Time: ts, Kind: SteeringEventSwitch,
		Priority: append([]string(nil), order...)}, m.maxEvents)
	return append([]string(nil), order...), true
}

// Get returns a deep copy of the session for sid (so callers can read it without the lock),
// dropping it if it has expired.
func (m *SteeringSessionMgr) Get(sid string) (*SteeringSession, bool) {
	if sid == "" {
		sid = "anon"
	}
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

// Clear removes all recorded sessions and groups and returns the number of sessions removed.
func (m *SteeringSessionMgr) Clear() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.sessions)
	m.sessions = make(map[string]*SteeringSession)
	m.groups = make(map[string]*SteeringGroup)
	return n
}

// ClearSession removes a single session by id, returning true if it existed.
func (m *SteeringSessionMgr) ClearSession(sid string) bool {
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
func (m *SteeringSessionMgr) List() []SteeringSession {
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictLocked(ts)
	out := make([]SteeringSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		summary := *s.clone()
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

// ClearGroup removes a content-steering group and all of its member sessions, returning true if the
// group existed.
func (m *SteeringSessionMgr) ClearGroup(csid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[csid]; !ok {
		return false
	}
	delete(m.groups, csid)
	for sid, s := range m.sessions {
		if s.CSID == csid {
			delete(m.sessions, sid)
		}
	}
	return true
}

// ListGroups returns the live content-steering groups with aggregate member stats (no member lists
// or event timelines), most-recently-active first.
func (m *SteeringSessionMgr) ListGroups() []SteeringGroup {
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictLocked(ts)
	out := make([]SteeringGroup, 0, len(m.groups))
	for csid, g := range m.groups {
		gv := g.clone()
		gv.Events = nil // omit the switch timeline in the list view
		m.fillGroupAggregates(gv, csid)
		out = append(out, *gv)
	}
	sortByLastSeenGroups(out)
	return out
}

// GetGroup returns a deep copy of the group for csid with aggregate stats, its switch timeline, and
// its member sessions (member event timelines omitted), or false if unknown or expired.
func (m *SteeringSessionMgr) GetGroup(csid string) (*SteeringGroup, bool) {
	ts := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[csid]
	if !ok {
		return nil, false
	}
	if m.ttl > 0 && ts.Sub(g.LastSeen) > m.ttl {
		delete(m.groups, csid)
		return nil, false
	}
	gv := g.clone()
	m.fillGroupAggregates(gv, csid)
	for _, s := range m.sessions {
		if s.CSID != csid {
			continue
		}
		ms := s.clone()
		ms.Events = nil // omit member timelines in the group view
		gv.Members = append(gv.Members, *ms)
	}
	// most-recently-active members first (simple insertion sort; the set is small)
	for i := 1; i < len(gv.Members); i++ {
		for j := i; j > 0 && gv.Members[j].LastSeen.After(gv.Members[j-1].LastSeen); j-- {
			gv.Members[j], gv.Members[j-1] = gv.Members[j-1], gv.Members[j]
		}
	}
	return gv, true
}

// fillGroupAggregates sums member-session stats into the group view. Caller must hold mu.
func (m *SteeringSessionMgr) fillGroupAggregates(gv *SteeringGroup, csid string) {
	gv.SegmentCounts = make(map[string]int)
	for _, s := range m.sessions {
		if s.CSID != csid {
			continue
		}
		gv.MemberCount++
		gv.SteeringReqCnt += s.SteeringReqCnt
		gv.IssueCount += s.IssueCount
		for loc, n := range s.SegmentCounts {
			gv.SegmentCounts[loc] += n
		}
	}
}

// sortByLastSeenGroups orders groups most-recently-active first (insertion sort; small set).
func sortByLastSeenGroups(gs []SteeringGroup) {
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j].LastSeen.After(gs[j-1].LastSeen); j-- {
			gs[j], gs[j-1] = gs[j-1], gs[j]
		}
	}
}

// evictLocked drops expired sessions and groups and enforces the maxSessions cap (oldest LastSeen
// first). Caller must hold mu.
func (m *SteeringSessionMgr) evictLocked(ts time.Time) {
	if m.ttl > 0 {
		for sid, s := range m.sessions {
			if ts.Sub(s.LastSeen) > m.ttl {
				delete(m.sessions, sid)
			}
		}
		for csid, g := range m.groups {
			if ts.Sub(g.LastSeen) > m.ttl {
				delete(m.groups, csid)
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
	for len(m.groups) > m.maxSessions {
		var oldestCsid string
		var oldest time.Time
		first := true
		for csid, g := range m.groups {
			if first || g.LastSeen.Before(oldest) {
				oldestCsid, oldest, first = csid, g.LastSeen, false
			}
		}
		delete(m.groups, oldestCsid)
	}
}

// clone deep-copies a session so it can be read and mutated outside the lock.
func (s *SteeringSession) clone() *SteeringSession {
	c := *s
	c.CDNs = append([]string(nil), s.CDNs...)
	c.CurrentPriority = append([]string(nil), s.CurrentPriority...)
	c.SegmentCounts = maps.Clone(s.SegmentCounts)
	if c.SegmentCounts == nil {
		c.SegmentCounts = make(map[string]int)
	}
	c.Events = make([]SteeringEvent, len(s.Events))
	for i, e := range s.Events {
		e.Priority = append([]string(nil), e.Priority...)
		e.Issues = append([]string(nil), e.Issues...)
		c.Events[i] = e
	}
	return &c
}

// clone deep-copies a group's stored decision fields. Aggregate fields (MemberCount/SegmentCounts/
// SteeringReqCnt/IssueCount/Members) are left zero for the caller to fill from member sessions.
func (g *SteeringGroup) clone() *SteeringGroup {
	c := *g
	c.CDNs = append([]string(nil), g.CDNs...)
	c.CurrentPriority = append([]string(nil), g.CurrentPriority...)
	c.SegmentCounts = nil
	c.MemberCount = 0
	c.SteeringReqCnt = 0
	c.IssueCount = 0
	c.Members = nil
	c.Events = make([]SteeringEvent, len(g.Events))
	for i, e := range g.Events {
		e.Priority = append([]string(nil), e.Priority...)
		e.Issues = append([]string(nil), e.Issues...)
		c.Events[i] = e
	}
	return &c
}
