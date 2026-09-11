// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	m "github.com/Eyevinn/dash-mpd/mpd"
)

// SVTA2053 Ad Creative Signaling (SVTA2053-1, payload version 2).
//
// Each Period holding ad creatives carries an EventStream with
// @schemeIdUri = SVTAAdCreativeScheme and one Event per creative. The Event node data is a
// JSON payload describing the creative: its identifiers, its duration, and the tracking URLs
// (impression and VAST quartiles) a player should fire while it plays. livesim2 marks ad-break
// windows of the live timeline as creatives; the AD BREAK slate makes them visible, and the
// tracking URLs point back at livesim2's own beacon endpoint, so the whole ad-measurement
// round trip can be observed on /sgai/session_status without an SSAI stack.
const (
	// SVTAAdCreativeScheme is the SVTA2053 Ad Creative Signaling scheme.
	SVTAAdCreativeScheme = "urn:svta:advertising-wg:ad-creative-signaling"
	// svtaPayloadVersion is the version of the JSON data payload (SVTA2053 v2).
	svtaPayloadVersion = 2
	// svtaAdIDScheme is the SMPTE Ad-ID identifier scheme (RP 2092-1:2015) used for the
	// generated creative identifiers.
	svtaAdIDScheme = "urn:smpte:ul:060E2B34.01040101.01200900.00000000"
	// svtaCatalogIDScheme identifies a livesim2 ad-catalog id (the ad creative's directory
	// name under the vodroot ads/ directory). Used when the signaled creative is a real ad
	// from the catalog rather than a slate window on the main timeline.
	svtaCatalogIDScheme = "urn:dashif:livesim2:ad-catalog-id"
	// svtaDefaultTimescale is the EventStream @timescale. 90 kHz matches the SGAI events and
	// the MPEG-2 system clock ad signaling normally originates from.
	svtaDefaultTimescale = uint32(90000)
	// svtaLookAheadS is how far ahead of the request time periodic breaks are signaled.
	svtaLookAheadS = 60
	// svtaMaxAdsPerBreak bounds the number of creatives a break may be split into (it also
	// keeps the generated event ids, which are breakID*1000+slot, unambiguous).
	svtaMaxAdsPerBreak = 20
	// svtaVerificationVendor and svtaVerificationResource make up the (optional) synthetic
	// verification resource. The script is served from the embedded static tree.
	svtaVerificationVendor   = "livesim2.dashif.org-omid"
	svtaVerificationResource = "/static/svta_verification.js"
)

// SVTAConfig configures SVTA2053 ad-creative signaling for a live stream.
type SVTAConfig struct {
	AdBreaks            // fixed breaks (offset from AST) or a periodic recurrence
	AdsPerBreak  int    `json:"AdsPerBreak"`            // creatives each break is split into
	SkipOffsetS  *int   `json:"SkipOffsetS,omitempty"`  // slot skipOffset in seconds
	ClickThrough bool   `json:"ClickThrough,omitempty"` // add clickThrough + clickTracking
	Verification bool   `json:"Verification,omitempty"` // add a verification resource
	Pod          bool   `json:"Pod,omitempty"`          // also signal the break as a pod
	Timescale    uint32 `json:"Timescale"`              // EventStream @timescale
}

// CreateSVTAConfig parses the value of an "svta" URL option.
//
// Grammar: ( <off>:<dur>[,<off>:<dur>...] | p<period>:<dur> )[;key=val;...]
// keys: ads=<n>, skip=<s>, click=<0|1>, verif=<0|1>, pod=<0|1>, ts=<n>
//
// Examples: 30:15;ads=2 => one 15s break 30s in, signaled as two 7.5s creatives.
// p60:20 => a 20s creative at every start of a (UTC) minute, recurring forever.
func CreateSVTAConfig(val string) (*SVTAConfig, error) {
	if val == "" {
		return nil, fmt.Errorf("empty svta config")
	}
	if hasExtraSpaces(val) {
		return nil, fmt.Errorf("svta config %q has extra spaces", val)
	}
	cfg := &SVTAConfig{
		AdsPerBreak: 1,
		Timescale:   svtaDefaultTimescale,
	}
	parts := strings.Split(val, ";")
	ab, err := parseAdBreaks("svta", parts[0])
	if err != nil {
		return nil, err
	}
	cfg.AdBreaks = ab
	for _, kv := range parts[1:] {
		key, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("svta param %q must be key=val", kv)
		}
		switch key {
		case "ads":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > svtaMaxAdsPerBreak {
				return nil, fmt.Errorf("svta ads %q: must be 1-%d", v, svtaMaxAdsPerBreak)
			}
			cfg.AdsPerBreak = n
		case "skip":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("svta skip %q: must be >= 0", v)
			}
			cfg.SkipOffsetS = &n
		case "click":
			cfg.ClickThrough, err = parseSVTABool("click", v)
		case "verif":
			cfg.Verification, err = parseSVTABool("verif", v)
		case "pod":
			cfg.Pod, err = parseSVTABool("pod", v)
		case "ts":
			// parseUint32 bounds the value to the uint32 the EventStream@timescale is,
			// so a huge value is rejected rather than silently truncated.
			n, err := parseUint32(v)
			if err != nil || n == 0 {
				return nil, fmt.Errorf("svta ts %q: must be a positive 32-bit integer", v)
			}
			cfg.Timescale = n
		default:
			return nil, fmt.Errorf("unknown svta param %q", key)
		}
		if err != nil {
			return nil, err
		}
	}
	if cfg.empty() {
		return nil, fmt.Errorf("svta config %q has no breaks", val)
	}
	return cfg, nil
}

// parseSVTABool parses a 0/1 (or false/true) svta option value.
func parseSVTABool(key, v string) (bool, error) {
	switch v {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("svta %s %q: must be 0 or 1", key, v)
	}
}

// ParseSVTAConfig parses an svta option value, accumulating any error on the converter.
func (s *strConvAccErr) ParseSVTAConfig(key, val string) *SVTAConfig {
	if s.err != nil {
		return nil
	}
	cfg, err := CreateSVTAConfig(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return cfg
}

// The SVTA2053 v2 JSON data payload. The field names and shapes follow the v2 carriage
// envelope: an envelope with a type ("slot" or "pod") and a payload array of objects whose
// start/duration are in seconds relative to the carrying Event@presentationTime.
type svtaEnvelope struct {
	Version int        `json:"version"`
	Type    string     `json:"type"`
	Payload []svtaSlot `json:"payload"`
}

// svtaSlot is one entry of the payload array: a Slot (type "linear") in a "slot" envelope,
// or a Pod (no type, pod-level tracking only) in a "pod" envelope.
type svtaSlot struct {
	Type          string             `json:"type,omitempty"`
	Start         float64            `json:"start"`
	Duration      float64            `json:"duration"`
	Identifiers   []svtaIdentifier   `json:"identifiers,omitempty"`
	Tracking      []svtaTracking     `json:"tracking,omitempty"`
	Verifications []svtaVerification `json:"verifications,omitempty"`
	SkipOffset    *float64           `json:"skipOffset,omitempty"`
	ClickThrough  string             `json:"clickThrough,omitempty"`
}

type svtaIdentifier struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}

type svtaTracking struct {
	Type   string   `json:"type"`
	Offset *float64 `json:"offset,omitempty"`
	URLs   []string `json:"urls"`
}

type svtaVerification struct {
	Vendor     string `json:"vendor"`
	Resource   string `json:"resource,omitempty"`
	Parameters string `json:"parameters,omitempty"`
}

// svtaTrackingPoints are the per-creative tracking events, as fractions of the creative
// duration. The names are the VAST names that players map directly (Shaka only recognizes
// these; it drops the spec's alternative "progress"+offset form), and they are the same
// events livesim2 already reports as SGAI callback beacons, plus "start".
var svtaTrackingPoints = []struct {
	event    string
	fraction float64
}{
	{"impression", 0.0},
	{"start", 0.0},
	{"firstQuartile", 0.25},
	{"midpoint", 0.5},
	{"thirdQuartile", 0.75},
	{"complete", 1.0},
}

// svtaInteractionEvents are the interaction tracking events. They carry no offset: per
// SVTA2053-1 §4.4.5 an absent offset means the timing is driven by the semantics of the event
// type, and these fire when the viewer acts rather than at a point on the timeline. They make
// an interrupted ad playout observable — without them, a pause is only visible indirectly, as
// the quartiles arriving late and the tail of the creative going unreported.
//
// Only the SVTA2053 carriage can express these. A DASH Ed.6 callback event (the other set of
// beacons livesim2 emits, in the ad Periods of the SGAI List MPD) is untyped: it says no more
// than "GET this URL when playback reaches this presentation time", so an interaction has no
// way to trigger one.
var svtaInteractionEvents = []string{"pause", "resume"}

// isRepeatableBeaconEvent reports whether an event may legitimately arrive more than once for
// one creative occurrence. The timeline points fire at most once each, so an identical re-fire
// is a duplicate worth collapsing, but a viewer can pause and resume any number of times inside
// a single ad — deduping those would hide every interaction after the first.
func isRepeatableBeaconEvent(event string) bool {
	return slices.Contains(svtaInteractionEvents, event)
}

// svtaBeaconURL builds a tracking URL for one creative and event. Unlike the SGAI callback
// beacons — where the Annex I RequestParam copies the List-MPD query onto each request — an
// SVTA2053 tracking URL is fired verbatim by the player, so the session id and the break
// (avail) id must be baked into the query for the beacon to be attributable.
func svtaBeaconURL(host, adID, event, breakID, sid string) string {
	q := url.Values{}
	if sid != "" {
		q.Set("sid", sid)
	}
	if breakID != "" {
		q.Set("evId", breakID)
	}
	return fmt.Sprintf("%s/sgai/beacon/%s/%s?%s", host, url.PathEscape(adID), url.PathEscape(event), q.Encode())
}

// svtaTrackingFor builds the tracking array for one creative: the timeline points first, then
// the interaction events.
func svtaTrackingFor(host, adID, breakID, sid string) []svtaTracking {
	tr := make([]svtaTracking, 0, len(svtaTrackingPoints)+len(svtaInteractionEvents)+1)
	for _, tp := range svtaTrackingPoints {
		tr = append(tr, svtaTracking{
			Type: tp.event,
			URLs: []string{svtaBeaconURL(host, adID, tp.event, breakID, sid)},
		})
	}
	return appendSVTAInteractionTracking(tr, host, adID, breakID, sid)
}

// appendSVTAInteractionTracking adds the interaction events to a tracking array. Shared by the
// main-timeline signaling and the per-ad signaling of the SGAI List MPD, so an interrupted ad
// is reported the same way whichever way the creative reached the viewer.
func appendSVTAInteractionTracking(tr []svtaTracking, host, adID, breakID, sid string) []svtaTracking {
	for _, event := range svtaInteractionEvents {
		tr = append(tr, svtaTracking{
			Type: event,
			URLs: []string{svtaBeaconURL(host, adID, event, breakID, sid)},
		})
	}
	return tr
}

// svtaAdID is the creative id used in the beacon paths. It is the slot number within a break,
// so the same creative aggregates across break occurrences on the session-status page, while
// the evId query parameter keeps the individual occurrences apart.
func svtaAdID(slot int) string {
	return fmt.Sprintf("svta-ad%d", slot)
}

// svtaCreativeValue is a synthetic Ad-ID-style identifier value, unique per break occurrence
// and slot so that a player can tell repeated placements of the same creative apart.
func svtaCreativeValue(breakID uint64, slot int) string {
	return fmt.Sprintf("LSIM%d%02dH", breakID, slot)
}

// addSVTAAdCreativeEvents injects an SVTA2053 Ad Creative Signaling EventStream into the
// period: one Event per creative, carrying the v2 JSON payload as node data.
//
// Each break instance is split into cfg.SVTA.AdsPerBreak equal creatives. Event@presentationTime
// is an absolute offset from the availabilityStartTime (the period starts at 0), so it is
// stable across MPD refreshes, and the slot itself starts at 0 relative to the Event — one
// Event per creative, as SVTA2053 requires.
func addSVTAAdCreativeEvents(mpd *m.MPD, period *m.Period, cfg *ResponseConfig, nowMS int) {
	if cfg.SVTA == nil {
		return
	}
	sc := cfg.SVTA
	lookBackS := 0
	if cfg.TimeShiftBufferDepthS != nil {
		lookBackS = *cfg.TimeShiftBufferDepthS
	}
	insts := sc.instances(nowMS, cfg.StartTimeS, svtaLookAheadS, lookBackS)
	if len(insts) == 0 {
		return
	}
	ts := sc.Timescale
	es := &m.EventStreamType{
		SchemeIdUri: SVTAAdCreativeScheme,
		Timescale:   m.Ptr(ts),
	}
	sid := cfg.SteerSessionID
	for _, b := range insts {
		breakID := strconv.FormatUint(b.id, 10)
		offsetTicks := uint64(b.offsetS) * uint64(ts)
		totalTicks := uint64(b.durS) * uint64(ts)
		if sc.Pod {
			es.Events = append(es.Events, svtaPodEvent(cfg.Host, b, breakID, sid, offsetTicks, totalTicks, ts))
		}
		for i := range sc.AdsPerBreak {
			startTicks := totalTicks * uint64(i) / uint64(sc.AdsPerBreak)
			endTicks := totalTicks * uint64(i+1) / uint64(sc.AdsPerBreak)
			slot := i + 1
			adID := svtaAdID(slot)
			durS := float64(endTicks-startTicks) / float64(ts)
			s := svtaSlot{
				Type:     "linear",
				Start:    0,
				Duration: durS,
				Identifiers: []svtaIdentifier{{
					Scheme: svtaAdIDScheme,
					Value:  svtaCreativeValue(b.id, slot),
				}},
				Tracking: svtaTrackingFor(cfg.Host, adID, breakID, sid),
			}
			if sc.SkipOffsetS != nil {
				skip := float64(*sc.SkipOffsetS)
				s.SkipOffset = &skip
			}
			if sc.ClickThrough {
				s.ClickThrough = cfg.Host + "/"
				s.Tracking = append(s.Tracking, svtaTracking{
					Type: "clickTracking",
					URLs: []string{svtaBeaconURL(cfg.Host, adID, "clickTracking", breakID, sid)},
				})
			}
			if sc.Verification {
				s.Verifications = []svtaVerification{{
					Vendor:     svtaVerificationVendor,
					Resource:   cfg.Host + svtaVerificationResource,
					Parameters: fmt.Sprintf("adId=%s&evId=%d", adID, b.id),
				}}
			}
			es.Events = append(es.Events, &m.EventType{
				PresentationTime: offsetTicks + startTicks,
				Duration:         m.Ptr(endTicks - startTicks),
				Id:               m.Ptr(b.id*1000 + uint64(slot)),
				Value:            svtaPayload("slot", s),
			})
		}
	}
	period.EventStreams = append(period.EventStreams, es)
}

// svtaPodEvent builds the optional pod-level Event covering the full break, with podStart and
// podEnd tracking. It complements (does not replace) the per-creative slot events.
func svtaPodEvent(host string, b adBreakInst, breakID, sid string, offsetTicks, totalTicks uint64, ts uint32) *m.EventType {
	durS := float64(totalTicks) / float64(ts)
	zero, end := 0.0, durS
	pod := svtaSlot{
		Start:    0,
		Duration: durS,
		Tracking: []svtaTracking{
			{Type: "podStart", Offset: &zero, URLs: []string{svtaBeaconURL(host, "svta-pod", "podStart", breakID, sid)}},
			{Type: "podEnd", Offset: &end, URLs: []string{svtaBeaconURL(host, "svta-pod", "podEnd", breakID, sid)}},
		},
	}
	return &m.EventType{
		PresentationTime: offsetTicks,
		Duration:         m.Ptr(totalTicks),
		Id:               m.Ptr(b.id * 1000),
		Value:            svtaPayload("pod", pod),
	}
}

// svtaPayload marshals one carriage envelope. The JSON becomes the Event node data; the XML
// encoder escapes it (&#34; for the quotes, &amp; for the query separators in the tracking
// URLs), which any conformant parser decodes back to the JSON. HTML escaping is turned off so
// that those separators stay plain "&" inside the JSON instead of becoming "\u0026".
func svtaPayload(envType string, slots ...svtaSlot) string {
	env := svtaEnvelope{
		Version: svtaPayloadVersion,
		Type:    envType,
		Payload: slots,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		// The payload types are plain data with no unsupported values, so this cannot fail.
		return ""
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// svtaListMPDEventStream builds the SVTA2053 EventStream for one ad Period of an SGAI List
// MPD: a single Event describing the ad creative that Period imports. Unlike the callback
// beacons — where the Annex I RequestParam copies the List-MPD query onto the request — these
// URLs are fired verbatim by the player, so the session and break ids are baked in.
//
// The tracking set is the same as for the main-timeline signaling, so a creative is reported
// the same way whichever route it reached the viewer by. It is a superset of what the Period's
// callback EventStream can carry: "start" is there, which the callback set leaves out because
// it would need a second event at the same instant as the impression, and so are the
// interaction events, which a callback event cannot express at all.
//
// durMS is the creative duration; with an unknown (zero) duration the tracking points all
// collapse to the start of the creative, exactly as for the callback events.
func svtaListMPDEventStream(host, adID, breakID, sid string, durMS int, eventID uint64) *m.EventStreamType {
	tracking := make([]svtaTracking, 0, len(svtaTrackingPoints)+len(svtaInteractionEvents))
	for _, tp := range svtaTrackingPoints {
		tracking = append(tracking, svtaTracking{
			Type: tp.event,
			URLs: []string{svtaBeaconURL(host, adID, tp.event, breakID, sid)},
		})
	}
	tracking = appendSVTAInteractionTracking(tracking, host, adID, breakID, sid)
	slot := svtaSlot{
		Type:        "linear",
		Start:       0,
		Duration:    float64(durMS) / 1000,
		Identifiers: []svtaIdentifier{{Scheme: svtaCatalogIDScheme, Value: adID}},
		Tracking:    tracking,
	}
	return &m.EventStreamType{
		SchemeIdUri: SVTAAdCreativeScheme,
		Timescale:   m.Ptr(sgaiTrackingTimescale),
		Events: []*m.EventType{{
			PresentationTime: 0,
			Duration:         m.Ptr(uint64(durMS)),
			Id:               m.Ptr(eventID),
			Value:            svtaPayload("slot", slot),
		}},
	}
}
