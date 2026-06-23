// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	m "github.com/Eyevinn/dash-mpd/mpd"
)

const (
	// sgaiAdsBaseDir is the vodroot subdirectory holding the SPS ad creatives.
	sgaiAdsBaseDir = "ads"
	// sgaiCallbackScheme is the DASH callback event scheme used for impression beacons.
	sgaiCallbackScheme = "urn:mpeg:dash:event:callback:2015"
)

// sgaiBeaconStampEventID controls whether the break/avail event id is stamped directly onto
// the List-MPD beacon URLs as ?evId=<breakId>.
//
// It is OFF by default because the event id already rides onto the beacon for free: the
// List-MPD request URL carries break=<id> (the ReplacePresentation @uri), and the callback
// RequestParam (useMPDUrlQuery + queryTemplate="$querypart$") copies that whole query onto
// every beacon — so each impression is already attributable to its specific break occurrence.
// Turn this ON for players that copy only a query subset and would otherwise drop break=;
// sgaiBeaconHandlerFunc reads evId with a break fallback either way. Keeping it off keeps the
// beacon URLs clean. It is a var (not a const) so it can be toggled if needed.
var sgaiBeaconStampEventID = false

// discoverAdCreatives returns the sorted MPD paths (e.g. "ads/ad0/Manifest.mpd") of the
// available SPS ad creatives: the directories under the ads directory that contain an MPD.
func discoverAdCreatives(vodFS fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(vodFS, sgaiAdsBaseDir)
	if err != nil {
		return nil, err
	}
	ads := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		adPath := path.Join(sgaiAdsBaseDir, e.Name())
		if mpdName := findAdMPD(vodFS, adPath); mpdName != "" {
			ads = append(ads, path.Join(adPath, mpdName))
		}
	}
	sort.Strings(ads)
	return ads, nil
}

// findAdMPD returns the name of the MPD file in adPath ("" if none). The MPD name is not
// fixed since packagers differ (e.g. GPAC writes manifest.mpd): Manifest.mpd wins if a dir
// has several, otherwise the lexicographically first *.mpd.
func findAdMPD(vodFS fs.FS, adPath string) string {
	entries, err := fs.ReadDir(vodFS, adPath)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".mpd" {
			continue
		}
		if e.Name() == "Manifest.mpd" {
			return e.Name()
		}
		if best == "" || e.Name() < best {
			best = e.Name()
		}
	}
	return best
}

// sgaiSessionID extracts the client/session id from the sessionId or sid query parameter.
func sgaiSessionID(r *http.Request) string {
	q := r.URL.Query()
	if v := q.Get("sessionId"); v != "" {
		return v
	}
	return q.Get("sid")
}

// rotateBySid returns items rotated so the starting index is a hash of sid (stable per sid),
// giving each session a different lead while keeping the set and relative order.
func rotateBySid[T any](items []T, sid string) []T {
	if len(items) == 0 {
		return nil
	}
	start := int(mixSessionHash(sid) % uint64(len(items)))
	out := make([]T, 0, len(items))
	for i := range items {
		out = append(out, items[(start+i)%len(items)])
	}
	return out
}

// parseInterests splits a comma-separated interests value into trimmed, non-empty tokens.
func parseInterests(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sgaiAdMeta is the optional per-ad metadata read from <vodroot>/ads/ads.json.
type sgaiAdMeta struct {
	Title     string   `json:"title,omitempty"`
	Interests []string `json:"interests,omitempty"`
}

// adMetaMatchesAny reports whether the ad is tagged with any of the requested interests
// (case-insensitive).
func adMetaMatchesAny(m sgaiAdMeta, interests []string) bool {
	for _, want := range interests {
		for _, have := range m.Interests {
			if strings.EqualFold(have, want) {
				return true
			}
		}
	}
	return false
}

// loadAdMeta reads optional per-ad metadata from <vodroot>/ads/ads.json. Missing or invalid
// metadata is not an error — it just means no interest steering and no titles.
func loadAdMeta(vodFS fs.FS) map[string]sgaiAdMeta {
	data, err := fs.ReadFile(vodFS, path.Join(sgaiAdsBaseDir, "ads.json"))
	if err != nil {
		return nil
	}
	var meta map[string]sgaiAdMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return meta
}

// mixSessionHash hashes sid (FNV-1a 64) and applies the murmur3 fmix64 finalizer. FNV's low
// bits avalanche poorly for small moduli (e.g. mod 3 with a few ads), so the finalizer is
// needed to spread the lead-ad assignment uniformly across sessions.
func mixSessionHash(sid string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sid))
	x := h.Sum64()
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// sgaiAdsHandlerFunc is the ad-decisioning endpoint referenced by a Replace event's @uri.
// It returns a personalized List MPD (Ed.6) whose Periods import the Single-Period-Static
// ad creatives served under /vod/ads. The pod ordering is keyed on the session id.
func (s *Server) sgaiAdsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	sid := sgaiSessionID(r)
	// interests is a comma-separated list (e.g. interests=boats,sailing); the singular
	// `interest` is accepted as a fallback alias.
	interestsRaw := r.URL.Query().Get("interests")
	if interestsRaw == "" {
		interestsRaw = r.URL.Query().Get("interest")
	}
	interests := parseInterests(interestsRaw)
	// dur is the break duration in seconds (from the @uri); the pod is trimmed to fit it.
	// It is required and must be positive: the ReplacePresentation @uri always carries a
	// valid dur, so a missing/non-numeric/non-positive value is a malformed request. Without
	// it selectPod would treat the limit as "unlimited" and return the whole catalog as the
	// pod, overrunning the break — so reject it instead of silently mis-filling.
	durS, err := strconv.Atoi(r.URL.Query().Get("dur"))
	if err != nil || durS <= 0 {
		log.Error("sgai ads: missing or invalid break duration", "dur", r.URL.Query().Get("dur"))
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "missing or invalid break duration (dur must be a positive integer)", http.StatusBadRequest)
		return
	}

	cat := s.adCatalog()
	if len(cat.ads) == 0 {
		log.Error("sgai ads: no ad creatives")
		http.Error(w, "no ad creatives available", http.StatusNotFound)
		return
	}
	podEntries := cat.selectPod(sid, interests, durS*1000)
	if len(podEntries) == 0 {
		// No ad pod: either no interests were given (the default — show the base ad) or the
		// requested interests matched no creative. Either way the break stays unfilled: the
		// 404 makes the player skip executing the Replace event, so the viewer keeps the
		// underlying break content (the AD BREAK countdown slate). Still recorded as a
		// decision (with an empty pod) so the session monitor shows the break occurred.
		if s.sgaiSessions != nil && r.URL.Query().Get("preview") != "1" {
			s.sgaiSessions.RecordDecision(sid, interestsRaw, nil)
		}
		breakDur := time.Duration(durS) * time.Second
		breakEnd := time.Now().Add(breakDur)
		reason := "no interests: base ad break (AD BREAK slate kept)"
		if interestsRaw != "" {
			reason = fmt.Sprintf("no ads match interests %q: base ad break (AD BREAK slate kept)", interestsRaw)
		}
		log.Info("sgai ad decision", "sid", sid, "interests", interestsRaw, "pod", "(none)",
			"reason", reason, "breakDurSec", durS, "breakEnd", breakEnd.Format("15:04:05.000"))
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, reason, http.StatusNotFound)
		return
	}
	pod := make([]string, len(podEntries))
	podIDs := make([]string, len(podEntries))
	durMS := make(map[string]int, len(podEntries))
	for i, e := range podEntries {
		pod[i] = e.MPDPath
		podIDs[i] = e.ID
		durMS[e.ID] = e.DurationMS
	}
	host := fullHost(s.Cfg.Host, r)
	// breakID is the break/avail event id from the ReplacePresentation @uri; carried onto the
	// beacons for per-occurrence attribution (via the callback RequestParam, and optionally
	// stamped as ?evId= — see sgaiBeaconURL).
	breakID := r.URL.Query().Get("break")
	mpd := buildAdListMPD(host, pod, durMS, breakID)

	buf := bytes.NewBuffer(make([]byte, 0, 1024))
	size, err := mpd.Write(buf, "  ", true)
	if err != nil {
		log.Error("sgai ads: write MPD", "err", err)
		http.Error(w, "could not write List MPD", http.StatusInternalServerError)
		return
	}
	// preview=1 is used by UI/tools to fetch the pod for display without it counting as a
	// real ad decision in the session record.
	if s.sgaiSessions != nil && r.URL.Query().Get("preview") != "1" {
		s.sgaiSessions.RecordDecision(sid, interestsRaw, podIDs)
	}
	breakDur := time.Duration(durS) * time.Second
	totalAdDur := 0
	for _, e := range podEntries {
		totalAdDur += e.DurationMS
	}
	breakEnd := time.Now().Add(breakDur)
	log.Info("sgai ad decision", "sid", sid, "interests", interestsRaw, "pod", strings.Join(podIDs, ","),
		"breakDurSec", durS, "podDurMs", totalAdDur, "breakEnd", breakEnd.Format("15:04:05.000"))
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.Header().Set("Content-Type", "application/dash+xml")
	// The List MPD is a per-session, per-break ad decision — it must never be cached.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// sgaiTrackingPoints are the per-ad progress beacons (VAST-style quartiles) emitted as
// callback events. Each fires when the ad playhead reaches the given fraction of its
// duration, giving server-guided percentage/quartile reporting: 0/25/50/75/100 %.
var sgaiTrackingPoints = []struct {
	event    string
	fraction float64
}{
	{"impression", 0.0},
	{"firstQuartile", 0.25},
	{"midpoint", 0.5},
	{"thirdQuartile", 0.75},
	{"complete", 1.0},
}

// sgaiTrackingTimescale is the timescale (ms) used for the callback-event presentation times.
const sgaiTrackingTimescale = uint32(1000)

// buildAdListMPD builds a List MPD (type=list, profile:list:2024) with one Period per ad.
// pod holds the ad MPD paths (e.g. "ads/ad0/Manifest.mpd"); the ad id is the dir name.
// Each Period imports an SPS ad MPD and carries a callback EventStream with the impression
// and quartile tracking beacons. durMS maps ad id -> ad duration (ms) so the quartile events
// get correct presentation times; a missing/zero duration places them all at time 0 (players
// that time quartiles from playback still report correctly). breakID is the break/avail event
// id (from ?break=) carried for attribution (see sgaiBeaconURL / the callback RequestParam).
//
// The beacon URLs are session-less (common per ad); the session is propagated to each beacon
// by the Annex I callback RequestParam below (useMPDUrlQuery copies the List-MPD request
// query — which already carries sessionId/interests and break — onto the beacon, DASH Ed.6
// Annex I + §8.13.2.4). The MPD-level urlparam:2025 EssentialProperty declares Annex I is used.
func buildAdListMPD(host string, pod []string, durMS map[string]int, breakID string) *m.MPD {
	mpd := m.NewMPD(m.LIST_TYPE)
	mpd.Profiles = m.PROFILE_LIST
	mpd.MinBufferTime = m.Seconds2DurPtr(1)
	for i, mpdPath := range pod {
		adID := path.Base(path.Dir(mpdPath))
		p := m.NewPeriod()
		p.Id = strconv.Itoa(i + 1)
		// Absolute URLs are used so the ad assets resolve against the /vod static
		// content independently of how the player resolves the List MPD base URL.
		p.ImportedMPDs = []*m.ImportedMpdType{{
			EarliestResolutionTimeOffset: 0,
			Value:                        m.AnyURI(fmt.Sprintf("%s/vod/%s", host, mpdPath)),
		}}
		d := 0
		if durMS != nil {
			d = durMS[adID]
		}
		es := &m.EventStreamType{
			SchemeIdUri: sgaiCallbackScheme,
			Value:       "1",
			Timescale:   m.Ptr(sgaiTrackingTimescale),
		}
		for j, tp := range sgaiTrackingPoints {
			pt := uint64(float64(d) * tp.fraction)
			es.Events = append(es.Events, &m.EventType{
				PresentationTime: pt,
				Id:               m.Ptr(uint64(i*len(sgaiTrackingPoints) + j + 1)),
				Value:            sgaiBeaconURL(host, adID, tp.event, breakID),
			})
		}
		// Annex I: carry the List-MPD request query (session id, interests, break) onto each
		// callback beacon GET, so the common-path beacon stays attributable per viewer/break.
		es.RequestParam = []*m.ExtendedUrlInfoType{{
			UrlQueryInfoType: m.UrlQueryInfoType{
				QueryTemplate:  "$querypart$",
				UseMPDUrlQuery: true,
			},
			IncludeInRequests: "callback",
		}}
		p.EventStreams = []*m.EventStreamType{es}
		mpd.AppendPeriod(p)
	}
	// MPD-level marker that Annex I (2025) URL parameters are used.
	mpd.EssentialProperties = append(mpd.EssentialProperties,
		m.NewDescriptor(UrlParam2025SchemeIdUri, "", ""))
	return mpd
}

// adDurationMS returns an ad's duration in ms given its MPD path. It prefers the
// consolidated LoopDurMS, but falls back to parsing the ad MPD's
// MediaPresentationDuration directly from the file: ad creatives whose duration is not an
// integer number of segments are skipped by loop-consolidation (so findAsset misses them /
// LoopDurMS == 0) yet are still served and have a valid media duration.
func (s *Server) adDurationMS(mpdPath string) int {
	if a, ok := s.assetMgr.findAsset(path.Dir(mpdPath)); ok && a.LoopDurMS > 0 {
		return a.LoopDurMS
	}
	data, err := fs.ReadFile(s.assetMgr.vodFS, mpdPath)
	if err != nil {
		return 0
	}
	mpd, err := m.ReadFromString(string(data))
	if err != nil || mpd.MediaPresentationDuration == nil {
		return 0
	}
	return int(mpd.MediaPresentationDuration.Seconds() * 1000)
}

// sgaiBeaconURL builds an impression/tracking beacon URL handled by sgaiBeaconHandlerFunc.
// The path is common to every viewer of a given ad (no session id) so the List MPD stays
// cacheable and the beacon endpoint has one path per ad — the session rides onto the request
// via the Annex I callback RequestParam (see buildAdListMPD). When sgaiBeaconStampEventID is
// set, the break/avail event id is appended as ?evId=<breakID> for explicit attribution.
func sgaiBeaconURL(host, adID, event, breakID string) string {
	u := fmt.Sprintf("%s/sgai/beacon/%s/%s", host, adID, event)
	if sgaiBeaconStampEventID && breakID != "" {
		u += "?evId=" + url.QueryEscape(breakID)
	}
	return u
}

// parseBeaconPath extracts adID and event from a /sgai/beacon/<adId>/<event> escaped path —
// the common (session-less) beacon path. Returns empty unless there are exactly two segments.
func parseBeaconPath(escapedPath string) (adID, event string) {
	segs := strings.Split(strings.TrimPrefix(escapedPath, "/sgai/beacon/"), "/")
	if len(segs) != 2 {
		return "", ""
	}
	adID = pathUnescape(segs[0])
	event = pathUnescape(segs[1])
	return adID, event
}

func pathUnescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// sgaiBeaconHandlerFunc receives impression/tracking beacons (with optional CMCD) and logs them.
// Path: /sgai/beacon/<adId>/<event> (common per ad — no session id in the path). The session
// is read from the query (?sessionId=/?sid=), propagated onto the beacon by the Annex I
// callback RequestParam in the List MPD. The break/avail event id is read from ?evId= with a
// fallback to ?break= (the latter rides over via useMPDUrlQuery="$querypart$"). Returns 204.
func (s *Server) sgaiBeaconHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	adID, event := parseBeaconPath(r.URL.EscapedPath())
	sid := sgaiSessionID(r)
	evID := r.URL.Query().Get("evId")
	if evID == "" {
		evID = r.URL.Query().Get("break")
	}
	cmcd := r.URL.Query().Get("CMCD")
	if cmcd == "" {
		cmcd = r.Header.Get("CMCD-Request")
	}
	if s.sgaiSessions != nil {
		s.sgaiSessions.RecordBeacon(sid, adID, event, cmcd, evID)
	}

	logAttrs := []any{"sid", sid, "adId", adID, "event", event}
	if evID != "" {
		logAttrs = append(logAttrs, "evId", evID)
	}
	if cmcd != "" {
		logAttrs = append(logAttrs, "cmcd", cmcd)
	}

	log.Info("sgai beacon", logAttrs...)
	w.WriteHeader(http.StatusNoContent)
}

// sgaiSessionStatusHandlerFunc renders the live status page (sgaiSessionStatusPage in sgai_session_status.templ)
// that shows, live, the ad decisions and impression beacons recorded for a session id. The page
// polls the JSON API (/api/sgai/sessions[/<sid>]) every second; with no ?sid= it lists the active
// sessions. This is the way to observe SGAI activity on a public livesim2 deployment where the
// process log is not visible. The page markup is a template and its polling logic lives in the
// static asset /static/sgai_session_status.js.
func (s *Server) sgaiSessionStatusHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := sgaiSessionStatusPage(fullHost(s.Cfg.Host, r)).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
