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
	// Use a temp dir as storage to get predictable absolute paths
	storageDir := t.TempDir()

	cases := []struct {
		path           string
		storage        string
		expectedMatch  bool
		expectedStream stream
	}{
		{path: "/asset/Streams(video.cmfv)", storage: storageDir, expectedMatch: true,
			expectedStream: stream{
				chName:    "asset",
				trName:    "video",
				ext:       ".cmfv",
				mediaType: "video",
				chDir:     filepath.Join(storageDir, "asset"),
				trDir:     filepath.Join(storageDir, "asset", "video")},
		},
		{path: "/asset/Streams(video.cmf)", storage: storageDir, expectedMatch: false},
		{path: "/lab/ex/ex1.isml/Streams(video-2000Kbps.cmfv)",
			storage:       storageDir,
			expectedMatch: true,
			expectedStream: stream{
				chName:    "lab/ex/ex1.isml",
				trName:    "video-2000Kbps",
				ext:       ".cmfv",
				mediaType: "video",
				chDir:     filepath.Join(storageDir, "lab", "ex", "ex1.isml"),
				trDir:     filepath.Join(storageDir, "lab", "ex", "ex1.isml", "video-2000Kbps")},
		},
	}

	for _, c := range cases {
		gotStream, match, err := findStreamMatch(c.storage, c.path)
		assert.NoError(t, err)
		assert.Equal(t, c.expectedMatch, match)
		if c.expectedMatch {
			assert.Equal(t, c.expectedStream, gotStream)
		}
	}
}

func TestMatchStreamRelativePath(t *testing.T) {
	// Test that relative storage paths work correctly (regression test)
	relativeStorage := "./segments"
	absStorage, err := filepath.Abs(relativeStorage)
	assert.NoError(t, err)

	gotStream, match, err := findStreamMatch(relativeStorage, "/test_channel/Streams(video.cmfv)")
	assert.NoError(t, err)
	assert.True(t, match, "should match with relative storage path")
	assert.Equal(t, "test_channel", gotStream.chName)
	assert.Equal(t, "video", gotStream.trName)
	assert.Equal(t, filepath.Join(absStorage, "test_channel"), gotStream.chDir)
	assert.Equal(t, filepath.Join(absStorage, "test_channel", "video"), gotStream.trDir)
}
