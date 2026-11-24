// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// TestSelfInit checks that selfinit_ puts the init segment, as a request for it with the
// same URL options returns it, in front of each segment and Partial Segment of the listed
// Representations, and of no others.
func TestSelfInit(t *testing.T) {
	_, get := testLiveServer(t)
	const nowMS = 110_000
	cases := []struct {
		desc     string
		opts     string // URL options, ending with a slash
		asset    string
		rep      string
		media    string
		selfInit bool
	}{
		{"segment", "selfinit_V300/", "testpic_2s", "V300", "50.m4s", true},
		{"segment of another Representation", "selfinit_V300/", "testpic_2s", "A48", "50.m4s", false},
		{"two Representations", "selfinit_V300,A48/", "testpic_2s", "A48", "50.m4s", true},
		{"encrypted segment", "eccp_cbcs/selfinit_V300/", "testpic_2s", "V300", "50.m4s", true},
		{"chunked segment", "chunkdur_0.5/selfinit_V300/", "testpic_2s", "V300", "50.m4s", true},
		{"Partial Segment", "ssras_2,1/chunkdurssr_2,0.5/selfinit_LD_360/", "testpic_2s_low_delay", "LD_360", "51_1.m4s", true},
		{"Partial Segment of another Representation", "ssras_2,1/chunkdurssr_2,0.5/selfinit_LD_720/", "testpic_2s_low_delay", "LD_360", "51_1.m4s", false},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			prefix := "/livesim2/" + c.opts + c.asset + "/" + c.rep + "/"
			status, init := get(prefix+"init.mp4", nowMS)
			require.Equal(t, http.StatusOK, status)
			status, body := get(prefix+c.media, nowMS)
			require.Equal(t, http.StatusOK, status, string(body))
			if !c.selfInit {
				require.False(t, bytes.HasPrefix(body, init[:16]), "no init segment in front")
				return
			}
			require.True(t, bytes.HasPrefix(body, init), "init segment in front")
			// The rest is the media segment, which decodes with the init segment.
			f, err := mp4.DecodeFile(bytes.NewReader(body))
			require.NoError(t, err)
			require.NotNil(t, f.Init)
			require.NotEmpty(t, f.Segments)
			require.NotEmpty(t, f.Segments[0].Fragments)
		})
	}

	status, body := get("/livesim2/selfinit_/testpic_2s/Manifest.mpd", nowMS)
	require.Equal(t, http.StatusBadRequest, status, string(body))
}
