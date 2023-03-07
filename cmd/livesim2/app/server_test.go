// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/cmd/livesim2/app"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer(t *testing.T) {
	args := []string{"livesim2", "--vodroot", "./testdata/assets"}
	cfg, err := app.LoadConfig(args, ".")
	assert.NoError(t, err)

	_, err = logging.InitZerolog(cfg.LogLevel, logging.LogDiscard)
	assert.NoError(t, err)

	server, err := app.SetupServer(context.Background(), cfg)
	assert.NoError(t, err)

	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, respBody := testRequest(t, ts, "GET", "/livesim2/testpic_2s/Manifest.mpd", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	mpdResponse, err := mpd.ReadFromString(string(respBody))

	require.NoError(t, err)
	for _, period := range mpdResponse.Periods {
		require.Equal(t, 2, len(period.AdaptationSets))
		require.Equal(t, 1, len(period.AdaptationSets[0].Representations))
	}

	// Test too early
	resp, respBody = testRequest(t, ts, "GET", "/livesim2/testpic_2s/V300/100.m4s?nowMS=180000", nil)
	require.Equal(t, http.StatusTooEarly, resp.StatusCode, "too early response code")
	require.Equal(t, "too early by 22000ms\n", string(respBody))

	// Test healthz
	resp, _ = testRequest(t, ts, "GET", "/healthz", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "healthz")
}

// Auxiliary functions for handler_*_test ================

func testRequest(t *testing.T, ts *httptest.Server, method, path string, reqBody io.Reader) (*http.Response, []byte) {
	req, err := http.NewRequest(method, ts.URL+path, reqBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer resp.Body.Close()

	return resp, respBody
}
