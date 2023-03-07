// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoDir(t *testing.T) {
	cases := []struct {
		rawURL     string
		outDir     string
		subDirs    []string
		wantedPath string
	}{
		{
			rawURL:     "https://dash.akamaized.net/WAVE/vectors/cfhd_sets/12.5_25_50/t1/2022-10-17/stream.mpd",
			outDir:     "/home/user/content",
			wantedPath: "/home/user/content/WAVE/vectors/cfhd_sets/12.5_25_50/t1/2022-10-17",
		},
		{
			rawURL:     "https://dash.akamaized.net/WAVE/vectors/cfhd_sets/12.5_25_50/t1/2022-10-17/stream.mpd",
			outDir:     "/home/user/content/WAVE/vectors",
			wantedPath: "/home/user/content/WAVE/vectors/cfhd_sets/12.5_25_50/t1/2022-10-17",
		},
		{
			rawURL:     "https://dash.akamaized.net/WAVE/stream.mpd",
			outDir:     "/home/user/content/WAVE/vectors",
			wantedPath: "/home/user/content/WAVE/vectors/WAVE",
		},
	}
	for _, tc := range cases {
		outPath, err := AutoDir(tc.rawURL, tc.outDir)
		require.NoError(t, err)
		require.Equal(t, tc.wantedPath, outPath)
	}

}
