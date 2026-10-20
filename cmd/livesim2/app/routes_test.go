// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReversePlayerProxy(t *testing.T) {
	var gotPath, gotHost, gotXFF string
	player := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost, gotXFF = r.URL.Path, r.Host, r.Header.Get("X-Forwarded-For")
		_, _ = w.Write([]byte("player"))
	}))
	defer player.Close()

	proxy := httptest.NewServer(createReversePlayerProxy("/player", player.URL+"/"))
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/player/index.html?mpd=x")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	playerURL, err := url.Parse(player.URL)
	require.NoError(t, err)
	require.Equal(t, "/index.html", gotPath)
	require.Equal(t, playerURL.Host, gotHost)
	require.Equal(t, "127.0.0.1", gotXFF)
}
