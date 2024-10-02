package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchMPD(t *testing.T) {
	cases := []struct {
		path           string
		expectedOutDir string
		expectedMatch  bool
	}{
		{path: "/asset/manifest.mpd", expectedMatch: true, expectedOutDir: "asset"},
		{path: "/rootdir/asset/manifest.mpd", expectedMatch: true, expectedOutDir: filepath.Join("rootdir", "asset")},
		{path: "/asset/Streams(video.cmfv)", expectedMatch: false, expectedOutDir: ""},
	}

	for _, c := range cases {
		outDir, match := matchMPD(c.path)
		assert.Equal(t, c.expectedMatch, match)
		assert.Equal(t, c.expectedOutDir, outDir)
	}
}

func TestMatchStream(t *testing.T) {
	cases := []struct {
		path           string
		storage        string
		expectedMatch  bool
		expectedStream stream
	}{
		{path: "/asset/Streams(video.cmfv)", expectedMatch: true,
			expectedStream: stream{
				chName:    "asset",
				trName:    "video",
				ext:       ".cmfv",
				mediaType: "video",
				chDir:     "asset",
				trDir:     filepath.Join("asset", "video")},
		},
		{path: "/asset/Streams(video.cmf)", expectedMatch: false},
		{path: "/lab/ex/ex1.isml/Streams(video-2000Kbps.cmfv)",
			expectedMatch: true,
			expectedStream: stream{
				chName:    "lab/ex/ex1.isml",
				trName:    "video-2000Kbps",
				ext:       ".cmfv",
				mediaType: "video",
				chDir:     filepath.Join("lab", "ex", "ex1.isml"),
				trDir:     filepath.Join("lab", "ex", "ex1.isml", "video-2000Kbps")},
		},
	}

	for _, c := range cases {
		gotStream, match := findStreamMatch(c.storage, c.path)
		assert.Equal(t, c.expectedMatch, match)
		assert.Equal(t, c.expectedStream, gotStream)
	}
}
