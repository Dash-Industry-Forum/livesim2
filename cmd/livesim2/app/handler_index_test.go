// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestIndexPageWithPrefix(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
		URLPrefix: "/livesim2",
	}
	_, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, body := testFullRequest(t, ts, "GET", "/", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Greater(t, strings.Index(string(body), `href="/livesim2/assets"`), 0)
}

func TestIndexPageWithoutPrefix(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	_, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, body := testFullRequest(t, ts, "GET", "/", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Greater(t, strings.Index(string(body), `href="/assets"`), 0)
}
