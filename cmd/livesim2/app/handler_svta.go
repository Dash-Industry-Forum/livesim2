// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// svtaFieldEnvelope is a carriage envelope of a remotely resolved field: a payload of
// trackingEvent or verification objects (SVTA2053-1 §5.2.2.1).
type svtaFieldEnvelope[T any] struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Payload []T    `json:"payload"`
}

// svtaRemoteTrackingHandlerFunc resolves the $remote.tracking URLs of svta_...;remote=<mode>
// and sgai_...;svta=remote (SVTA2053-1 §5.2.2.2). Path: /svta/tracking/<adId>, with the query
// that svtaRemoteTrackingURL writes. It answers a trackingEvent envelope with the same tracking
// array as the inline form, so the beacons that follow are recorded as before, and it records
// the fetch in the session, which shows when the player resolved the field. With mode=404 or
// mode=wrongtype it answers 404 or a verification envelope instead, to test the error handling
// of §5.2.2.2.2.
func (s *Server) svtaRemoteTrackingHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	adID := pathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), svtaRemoteTrackingPath))
	q := r.URL.Query()
	sid := sgaiSessionID(r)
	evID := q.Get("evId")
	mode := q.Get("mode")
	if mode == "" {
		mode = svtaRemoteOK
	}
	if adID == "" || strings.Contains(adID, "/") {
		http.Error(w, "svta remote: no ad id", http.StatusBadRequest)
		return
	}
	host := fullHost(s.Cfg.Host, r)
	var body string
	switch mode {
	case svtaRemoteOK:
		var tracking []svtaTracking
		if adID == svtaPodAdID {
			durS, err := strconv.ParseFloat(q.Get("dur"), 64)
			if err != nil || durS < 0 {
				http.Error(w, "svta remote: bad pod duration", http.StatusBadRequest)
				return
			}
			tracking = svtaPodTracking(host, evID, sid, durS)
		} else {
			tracking = svtaTrackingFor(host, adID, evID, sid, q.Get("click") == "1")
		}
		body = marshalSVTA(svtaFieldEnvelope[svtaTracking]{
			Version: svtaPayloadVersion, Type: "trackingEvent", Payload: tracking})
	case svtaRemoteWrongType:
		body = marshalSVTA(svtaFieldEnvelope[svtaVerification]{
			Version: svtaPayloadVersion, Type: "verification",
			Payload: []svtaVerification{{Vendor: svtaVerificationVendor, Resource: host + svtaVerificationResource}}})
	case svtaRemote404:
	default:
		http.Error(w, "svta remote: unknown mode", http.StatusBadRequest)
		return
	}
	if s.sgaiSessions != nil {
		s.sgaiSessions.RecordRemote(sid, adID, "tracking", evID, mode)
	}
	log.Info("svta remote", "sid", sid, "adId", adID, "evId", evID, "mode", mode)
	if mode == svtaRemote404 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
