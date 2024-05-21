// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestIndexPageWithHost(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
		Host:      "https://example.com/subfolder",
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, body := testFullRequest(t, ts, "GET", "/", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Greater(t, strings.Index(string(body), `href="https://example.com/subfolder/assets"`), 0)
}

func TestIndexPageWithoutHostAndVersion(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()

	resp, body := testFullRequest(t, ts, "GET", "/", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Greater(t, strings.Index(string(body), fmt.Sprintf(`href="%s/assets"`, ts.URL)), 0)

	resp, body = testFullRequest(t, ts, "GET", "/version", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	bodyStr := string(body)
	require.Equal(t, fmt.Sprintf("{\"Version\":\"%s\"}", internal.GetVersion()), bodyStr)
}
