package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchMPD(t *testing.T) {
	cases := []struct {
		path            string
		exptectedOutDir string
		expectedMatch   bool
	}{
		{path: "/asset/manifest.mpd", expectedMatch: true, exptectedOutDir: "asset"},
		{path: "/rootdir/asset/manifest.mpd", expectedMatch: true, exptectedOutDir: "rootdir/asset"},
		{path: "/asset/Streams(video.cmfv)", expectedMatch: false, exptectedOutDir: ""},
	}

	for _, c := range cases {
		outDir, match := matchMPD(c.path)
		assert.Equal(t, c.expectedMatch, match)
		assert.Equal(t, c.exptectedOutDir, outDir)
	}
}
