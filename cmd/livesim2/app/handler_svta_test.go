// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveSVTARemote fetches a $remote.tracking URL from the handler and decodes the envelope of
// a 200 response.
func resolveSVTARemote(t *testing.T, s *Server, rawURL string) (int, svtaFieldEnvelope[svtaTracking]) {
	t.Helper()
	w := httptest.NewRecorder()
	s.svtaRemoteTrackingHandlerFunc(w, httptest.NewRequest(http.MethodGet, rawURL, nil))
	var env svtaFieldEnvelope[svtaTracking]
	if w.Code == http.StatusOK {
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	}
	return w.Code, env
}

// svtaTestPayloads returns the SVTA EventStream payloads of a period for an svta_ value.
func svtaTestPayloads(t *testing.T, val string) []svtaEnvelope {
	t.Helper()
	cfg := NewResponseConfig()
	cfg.Host = "https://example.com"
	cfg.SteerSessionID = "alice"
	sc, err := CreateSVTAConfig(val)
	require.NoError(t, err)
	cfg.SVTA = sc
	period := &m.Period{Id: "P0"}
	addSVTAAdCreativeEvents(&m.MPD{Periods: []*m.Period{period}}, period, cfg, 0)
	require.Len(t, period.EventStreams, 1)
	return svtaEventPayloads(t, period.EventStreams[0])
}

// TestSVTARemoteTracking checks that remote=1 replaces the tracking of every slot and of the
// pod with a $remote.tracking URL, declares features.remoteFields, and that each URL resolves
// to the tracking array of the inline form. Each fetch is recorded in the session.
func TestSVTARemoteTracking(t *testing.T) {
	inline := svtaTestPayloads(t, "30:15;ads=2;click=1;pod=1")
	remote := svtaTestPayloads(t, "30:15;ads=2;click=1;pod=1;remote=1")
	require.Len(t, remote, 3, "the pod and two creatives")
	require.Len(t, inline, len(remote))

	s := &Server{Cfg: &ServerConfig{Host: "https://example.com"}, sgaiSessions: NewSgaiSessionMgr()}
	wantURLs := []string{
		"https://example.com/svta/tracking/svta-pod?dur=15&evId=1&sid=alice",
		"https://example.com/svta/tracking/svta-ad1?click=1&evId=1&sid=alice",
		"https://example.com/svta/tracking/svta-ad2?click=1&evId=1&sid=alice",
	}
	for i, env := range remote {
		assert.Nil(t, inline[i].Features, "inline tracking declares no features")
		require.NotNil(t, env.Features, "SVTA2053-1 Table 15")
		assert.True(t, env.Features.RemoteFields)
		got := env.Payload[0]
		assert.Empty(t, got.Tracking, "the remote tracking replaces the inline one")
		require.NotNil(t, got.Remote)
		assert.Equal(t, wantURLs[i], got.Remote.Tracking)

		code, resolved := resolveSVTARemote(t, s, got.Remote.Tracking)
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, svtaPayloadVersion, resolved.Version)
		assert.Equal(t, "trackingEvent", resolved.Type)
		assert.Equal(t, inline[i].Payload[0].Tracking, resolved.Payload, "the same tracking as inline")
		// Everything else is unchanged.
		want := inline[i].Payload[0]
		want.Tracking, got.Remote = nil, nil
		assert.Equal(t, want, got)
	}

	// The JSON keys of SVTA2053-1 Tables 15 and 16.
	raw := svtaPayload("slot", svtaSlot{Type: "linear", Remote: &svtaRemote{Tracking: "https://x/t"}})
	assert.Contains(t, raw, `"$remote":{"tracking":"https://x/t"}`)
	assert.Contains(t, raw, `"features":{"remoteFields":true}`)

	sess, ok := s.sgaiSessions.Get("alice")
	require.True(t, ok)
	assert.Equal(t, 3, sess.RemoteCnt)
	assert.Equal(t, 0, sess.BeaconCnt, "resolving is not a beacon")
	require.Len(t, sess.Events, 3)
	assert.Equal(t, SgaiEvent{Time: sess.Events[0].Time, Kind: SgaiEventRemote, AdID: svtaPodAdID,
		Event: "tracking", EvID: "1", Result: svtaRemoteOK}, sess.Events[0])
	assert.Equal(t, "svta-ad2", sess.Events[2].AdID)
}

// TestSVTARemoteTrackingFailures checks the remote= modes that make the resolution fail, as
// SVTA2053-1 §5.2.2.2.2 asks the player to handle: a failed fetch, and an envelope of the
// wrong type. Both are recorded in the session.
func TestSVTARemoteTrackingFailures(t *testing.T) {
	s := &Server{Cfg: &ServerConfig{Host: "https://example.com"}, sgaiSessions: NewSgaiSessionMgr()}

	envs := svtaTestPayloads(t, "30:15;remote=404")
	u := envs[0].Payload[0].Remote.Tracking
	assert.Equal(t, "https://example.com/svta/tracking/svta-ad1?evId=1&mode=404&sid=alice", u)
	code, _ := resolveSVTARemote(t, s, u)
	assert.Equal(t, http.StatusNotFound, code)

	envs = svtaTestPayloads(t, "30:15;remote=wrongtype")
	w := httptest.NewRecorder()
	s.svtaRemoteTrackingHandlerFunc(w, httptest.NewRequest(http.MethodGet, envs[0].Payload[0].Remote.Tracking, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var env svtaFieldEnvelope[svtaVerification]
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "verification", env.Type, "a valid envelope, but not the trackingEvent one asked for")
	require.Len(t, env.Payload, 1)
	assert.Equal(t, svtaVerificationVendor, env.Payload[0].Vendor)

	sess, ok := s.sgaiSessions.Get("alice")
	require.True(t, ok)
	require.Len(t, sess.Events, 2)
	assert.Equal(t, svtaRemote404, sess.Events[0].Result)
	assert.Equal(t, svtaRemoteWrongType, sess.Events[1].Result)
}

func TestSVTARemoteTrackingBadRequests(t *testing.T) {
	s := &Server{Cfg: &ServerConfig{Host: "https://example.com"}, sgaiSessions: NewSgaiSessionMgr()}
	for _, u := range []string{
		"/svta/tracking/",
		"/svta/tracking/a/b",
		"/svta/tracking/svta-ad1?mode=500",
		"/svta/tracking/svta-pod?evId=1",
		"/svta/tracking/svta-pod?dur=-1",
	} {
		code, _ := resolveSVTARemote(t, s, u)
		assert.Equal(t, http.StatusBadRequest, code, u)
	}
	assert.Empty(t, s.sgaiSessions.List(), "a bad request is not recorded")
}
