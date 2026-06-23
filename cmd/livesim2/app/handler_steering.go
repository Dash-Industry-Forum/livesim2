// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// steeringSessionID extracts the client/session id from the sessionId or sid query parameter
// (same convention as SGAI).
func steeringSessionID(r *http.Request) string {
	q := r.URL.Query()
	if v := q.Get("sessionId"); v != "" {
		return v
	}
	return q.Get("sid")
}

// steeringManifestHandlerFunc is the DASH Content Steering server endpoint referenced by the
// MPD's <ContentSteering> element. Path: /steering/steer_<spec> (the same steer_ token used on
// the stream URL, so the endpoint is stateless about the stream configuration). It returns a
// steering manifest (VERSION/TTL/RELOAD-URI/PATHWAY-PRIORITY) as application/json. The client
// (e.g. dash.js) appends _DASH_pathway and _DASH_throughput, which are recorded for inspection.
func (s *Server) steeringManifestHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	rest := strings.TrimPrefix(r.URL.Path, "/steering/")
	// The path is steer_<spec>, optionally prefixed with a csid_<group> token:
	// /steering/[csid_<group>/]steer_<spec>. Scan the path segments for each token so the
	// endpoint stays stateless about the stream configuration and the group.
	var steerSpec, csid string
	for seg := range strings.SplitSeq(rest, "/") {
		if v, ok := strings.CutPrefix(seg, "steer_"); ok {
			steerSpec = v
		} else if v, ok := strings.CutPrefix(seg, "csid_"); ok {
			csid = v
		}
	}
	if steerSpec == "" {
		http.Error(w, "steering URL must be /steering/[csid_<group>/]steer_<spec>", http.StatusBadRequest)
		return
	}
	cfg, err := CreateSteeringConfig(steerSpec)
	if err != nil {
		log.Error("steering: bad config", "spec", steerSpec, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sid := steeringSessionID(r)
	pathway := strings.Trim(r.URL.Query().Get("_DASH_pathway"), `"`)
	throughput := r.URL.Query().Get("_DASH_throughput")

	var priority []string
	if s.steeringSessions != nil {
		priority = s.steeringSessions.ComputeAndRecord(sid, csid, cfg, pathway, throughput)
	} else {
		priority = cfg.rotatePriority(time.Now().Unix())
	}

	manifest := SteeringManifest{
		Version:         steeringManifestVer,
		TTL:             cfg.TTL,
		ReloadURI:       steeringServerURL(fullHost(s.Cfg.Host, r), rest, sid),
		PathwayPriority: priority,
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		http.Error(w, "could not marshal steering manifest", http.StatusInternalServerError)
		return
	}
	log.Info("steering manifest", "sid", sid, "csid", csid, "mode", cfg.Mode, "priority", strings.Join(priority, ","),
		"pathway", pathway, "throughput", throughput)
	w.Header().Set("Content-Type", "application/json")
	// Per-session, time-varying steering decision — it must never be cached.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// steeringSessionStatusHandlerFunc renders the live status page (steeringSessionStatusPage in
// steering_session_status.templ) showing, live, the per-CDN segment request distribution and the
// current pathway priority for a session id. The page polls the JSON API
// (/api/steering/sessions[/<sid>]) every second; with no ?sid= it lists active sessions, and with
// ?csid= it shows the group view. Its polling logic lives in /static/steering_session_status.js.
func (s *Server) steeringSessionStatusHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := steeringSessionStatusPage(fullHost(s.Cfg.Host, r)).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
