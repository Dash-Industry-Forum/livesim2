// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSteeringTestServer sets up a server over the test assets with the steering session clock
// pinned so rotate-mode priorities are deterministic.
func newSteeringTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := ServerConfig{VodRoot: "testdata/assets", TimeoutS: 0, LogFormat: logging.LogDiscard}
	require.NoError(t, logging.InitSlog(cfg.LogLevel, cfg.LogFormat))
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	server.steeringSessions.now = func() time.Time { return time.Unix(0, 0).UTC() }
	ts := httptest.NewServer(server.Router)
	t.Cleanup(ts.Close)
	return ts
}

func TestSteeringMPDHasContentSteeringAndBaseURLs(t *testing.T) {
	ts := newSteeringTestServer(t)
	resp, body := testFullRequest(t, ts, "GET",
		"/livesim2/steer_alpha,beta;ttl=20;qbs=1/testpic_2s/Manifest.mpd?sessionId=t1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	s := string(body)
	for _, want := range []string{
		`<ContentSteering`,
		`defaultServiceLocation="alpha beta"`,
		`queryBeforeStart="true"`,
		`/steering/steer_alpha,beta;ttl=20;qbs=1?sessionId=t1</ContentSteering>`,
		`<BaseURL serviceLocation="alpha">`,
		`<BaseURL serviceLocation="beta">`,
		`/livesim2/cdn_alpha/sid_t1/steer_alpha,beta;ttl=20;qbs=1/testpic_2s/</BaseURL>`,
		`/livesim2/cdn_beta/sid_t1/steer_alpha,beta;ttl=20;qbs=1/testpic_2s/</BaseURL>`,
	} {
		assert.Contains(t, s, want, "MPD should contain %q", want)
	}
}

func TestSteeringManifestEndpointRotate(t *testing.T) {
	ts := newSteeringTestServer(t) // clock pinned at unix 0
	resp, body := testFullRequest(t, ts, "GET", "/steering/steer_alpha,beta;ttl=20;mode=rotate?sessionId=t1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))

	var man SteeringManifest
	require.NoError(t, json.Unmarshal(body, &man))
	assert.Equal(t, 1, man.Version)
	assert.Equal(t, 20, man.TTL)
	// unix 0 -> bucket 0 -> default order.
	assert.Equal(t, []string{"alpha", "beta"}, man.PathwayPriority)
	assert.Contains(t, man.ReloadURI, "/steering/steer_alpha,beta;ttl=20;mode=rotate?sessionId=t1")
}

func TestSteeringManifestBadSpec(t *testing.T) {
	ts := newSteeringTestServer(t)
	resp, _ := testFullRequest(t, ts, "GET", "/steering/steer_onlyone?sessionId=t1", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = testFullRequest(t, ts, "GET", "/steering/notsteer_a,b", nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSteeringSwitchFlipsManifest(t *testing.T) {
	ts := newSteeringTestServer(t)
	// Manual mode: priority is deterministic and held until switched.
	steer := "steer_alpha,beta;ttl=20;mode=trigger"

	_, body := testFullRequest(t, ts, "GET", "/steering/"+steer+"?sessionId=t1", nil)
	var man SteeringManifest
	require.NoError(t, json.Unmarshal(body, &man))
	assert.Equal(t, []string{"alpha", "beta"}, man.PathwayPriority)

	// Trigger a switch via the API.
	resp, swBody := testFullRequest(t, ts, "POST", "/api/steering/sessions/t1/switch",
		strings.NewReader(`{"target":"next"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(swBody), `"beta"`)

	// The next steering poll reflects the new order.
	_, body = testFullRequest(t, ts, "GET", "/steering/"+steer+"?sessionId=t1", nil)
	require.NoError(t, json.Unmarshal(body, &man))
	assert.Equal(t, []string{"beta", "alpha"}, man.PathwayPriority, "client sees the switched order")
}

func TestSteeringMPDWithGroup(t *testing.T) {
	ts := newSteeringTestServer(t)
	resp, body := testFullRequest(t, ts, "GET",
		"/livesim2/csid_groupA/steer_alpha,beta;ttl=20/testpic_2s/Manifest.mpd?sessionId=v1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	s := string(body)
	for _, want := range []string{
		// The steering server URL carries the group token so members re-poll under the same group.
		`/steering/csid_groupA/steer_alpha,beta;ttl=20?sessionId=v1</ContentSteering>`,
		// The csid_ group token rides along on each per-CDN BaseURL (so segments carry it too).
		`/livesim2/cdn_alpha/sid_v1/csid_groupA/steer_alpha,beta;ttl=20/testpic_2s/</BaseURL>`,
		`/livesim2/cdn_beta/sid_v1/csid_groupA/steer_alpha,beta;ttl=20/testpic_2s/</BaseURL>`,
	} {
		assert.Contains(t, s, want, "MPD should contain %q", want)
	}
}

func TestSteeringGroupSwitchMovesMembers(t *testing.T) {
	ts := newSteeringTestServer(t)
	steer := "csid_groupA/steer_alpha,beta;ttl=20;mode=trigger"

	// Two viewers in groupA poll the steering endpoint (group carried in the path).
	for _, sid := range []string{"v1", "v2"} {
		resp, body := testFullRequest(t, ts, "GET", "/steering/"+steer+"?sessionId="+sid, nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var man SteeringManifest
		require.NoError(t, json.Unmarshal(body, &man))
		assert.Equal(t, []string{"alpha", "beta"}, man.PathwayPriority)
	}

	// Switch the whole group with a single API call.
	resp, swBody := testFullRequest(t, ts, "POST", "/api/steering/groups/groupA/switch",
		strings.NewReader(`{"target":"beta"}`))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(swBody), `"beta"`)

	// Both members see the switched order on their next poll.
	for _, sid := range []string{"v1", "v2"} {
		_, body := testFullRequest(t, ts, "GET", "/steering/"+steer+"?sessionId="+sid, nil)
		var man SteeringManifest
		require.NoError(t, json.Unmarshal(body, &man))
		assert.Equal(t, []string{"beta", "alpha"}, man.PathwayPriority, "member follows the group switch")
	}

	// The group API reports both members and the switched shared priority.
	resp, body := testFullRequest(t, ts, "GET", "/api/steering/groups/groupA", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Group SteeringGroup `json:"group"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, 2, out.Group.MemberCount)
	assert.Equal(t, []string{"beta", "alpha"}, out.Group.CurrentPriority)
	assert.True(t, out.Group.ManualOverride)
}

func TestSteeringPollMessageVerification(t *testing.T) {
	ts := newSteeringTestServer(t)
	steer := "steer_alpha,beta;ttl=20;mode=trigger"

	// A well-formed poll: the client reports it is on the default top "alpha" with an integer
	// throughput.
	resp, _ := testFullRequest(t, ts, "GET",
		"/steering/"+steer+"?sessionId=v1&_DASH_pathway=alpha&_DASH_throughput=1200000", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// A non-conformant poll: an unknown pathway and a non-numeric throughput.
	resp, _ = testFullRequest(t, ts, "GET",
		"/steering/"+steer+"?sessionId=v1&_DASH_pathway=ghost&_DASH_throughput=abc", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, body := testFullRequest(t, ts, "GET", "/api/steering/sessions/v1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Session SteeringSession `json:"session"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.Greater(t, out.Session.IssueCount, 0, "the bad poll should be flagged")
	require.Len(t, out.Session.Events, 2)
	assert.Empty(t, out.Session.Events[0].Issues, "first poll is well-formed")
	assert.NotEmpty(t, out.Session.Events[1].Issues, "second poll is flagged")
}

func TestSteeringSegmentCountedPerCDN(t *testing.T) {
	ts := newSteeringTestServer(t)
	// Fetch an init segment through the alpha CDN BaseURL path.
	resp, _ := testFullRequest(t, ts, "GET",
		"/livesim2/cdn_alpha/sid_t1/steer_alpha,beta;ttl=20/testpic_2s/V300/init.mp4", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The API attributes it to (session t1, service location alpha).
	resp, body := testFullRequest(t, ts, "GET", "/api/steering/sessions/t1", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out struct {
		Session SteeringSession `json:"session"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	assert.GreaterOrEqual(t, out.Session.SegmentCounts["alpha"], 1)
	assert.Equal(t, 0, out.Session.SegmentCounts["beta"])
}
