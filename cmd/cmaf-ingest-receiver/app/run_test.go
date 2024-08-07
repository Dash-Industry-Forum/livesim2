package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReceivingMediaLiveInput(t *testing.T) {
	cases := []struct {
		name             string
		streamsURLFormat bool
		startNr          int
	}{
		{name: "StreamsFormat with startNr=1", streamsURLFormat: false, startNr: 1}, // MediaLive case
		{name: "SegmentFormat", streamsURLFormat: true, startNr: 0},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("streamsURLFormat=%t", c.streamsURLFormat), func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "ew-cmaf-ingest-test")
			defer os.RemoveAll(tmpDir)
			assert.NoError(t, err)
			chName := "awsMediaLiveScte35"
			srcDir := filepath.Join("testdata")
			opts := Options{
				prefix:                "/upload",
				timeShiftBufferDepthS: 30,
				storage:               tmpDir,
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := &Config{
				Channels: []ChannelConfig{
					{Name: chName, StartNr: c.startNr},
				},
			}
			receiver, err := NewReceiver(ctx, &opts, cfg)
			require.NoError(t, err)
			router := setupRouter(receiver)
			server := httptest.NewServer(router)
			defer server.Close()
			t.Logf("Server started with prefix: %s at %s", opts.prefix, server.URL)
			testTrackData := createTrackTestData(t, srcDir, chName)
			wg := sync.WaitGroup{}

			// Send all init segments and check that they have been received
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, "init",
						trd.ext, c.streamsURLFormat)
				}
				wg.Done()
			}()
			wg.Wait()
			// Check what has been uploaded tmpDir/chName
			dstDir := filepath.Join(tmpDir, chName)
			assert.True(t, testDirExists(t, dstDir), "channel directory should exist")
			trDirs, err := os.ReadDir(dstDir)
			assert.NoError(t, err)
			require.Equal(t, 3, len(trDirs), "there should be 3 tracks written")
			for _, trd := range testTrackData {
				assert.True(t, testDirExists(t, filepath.Join(dstDir, trd.trName)), fmt.Sprintf("%s directory should exist", trd.trName))
				assert.True(t, testFileExists(t, filepath.Join(dstDir, trd.trName, "init"+trd.ext)),
					fmt.Sprintf("%s/init%s should exist", trd.trName, trd.ext))
			}

			// Send first media segments and check that they have been received
			wg = sync.WaitGroup{}
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat)
				}
				wg.Done()
			}()
			wg.Wait()
			// Check that first media segment has been written
			for _, trd := range testTrackData {
				assert.True(t, testFileExists(t, filepath.Join(dstDir, trd.trName, fmt.Sprintf("%d%s", trd.minNr-c.startNr, trd.ext))),
					fmt.Sprintf("%s/%d%s should exist", trd.trName, trd.minNr-c.startNr, trd.ext))
			}

			// Send the second media segments. Since the duration is not the same, there should be no content_info.json written.
			wg = sync.WaitGroup{}
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr+1)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat)
				}
				wg.Done()
			}()
			wg.Wait()
			time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing
			assert.True(t, !testFileExists(t, filepath.Join(dstDir, "content_info.json")), "content_info.json should not exist yet")
			// Check that the second media segment has been written
			for _, trd := range testTrackData {
				assert.True(t, testFileExists(t, filepath.Join(dstDir, trd.trName, fmt.Sprintf("%d%s", trd.minNr+1-c.startNr, trd.ext))),
					fmt.Sprintf("%s/%d%s should exist", trd.trName, trd.minNr+1-c.startNr, trd.ext))
			}
			// Send the third media segments. Since the duration is the same for two segments, the manifest.mpd should be written.
			wg = sync.WaitGroup{}
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr+2)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat)
				}
				wg.Done()
			}()
			wg.Wait()
			time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
			assert.True(t, testFileExists(t, filepath.Join(dstDir, "manifest.mpd")), "manifest.mpd should exist")

			fs, ok := receiver.channelMgr.GetChannel(chName)
			assert.True(t, ok, "channel should exist")
			sdb, ok := fs.segDataBuffers["video"]
			assert.True(t, ok, "segment data buffer should exist")
			var videoTrackData trTestData
			for _, trd := range testTrackData {
				if trd.trName == "video" {
					videoTrackData = trd
				}
			}
			assert.Equal(t, sdb.firstSeqNr, uint32(videoTrackData.minNr+1-c.startNr),
				"first sequence number in segment data buffer should be the second segment")
		})
	}
}

type trTestData struct {
	trName string
	ext    string
	minNr  int
	maxNr  int
}

func createTrackTestData(t *testing.T, srcDir, chName string) []trTestData {
	dirPath := filepath.Join(srcDir, chName)
	trDirs, err := os.ReadDir(dirPath)
	assert.NoError(t, err)

	trDatas := make([]trTestData, 0, len(trDirs))
	for _, dir := range trDirs {
		if !dir.IsDir() {
			continue
		}
		trd := trTestData{}
		trPath := filepath.Join(dirPath, dir.Name())
		segments, err := os.ReadDir(trPath)
		assert.NoError(t, err)
		for _, seg := range segments {
			name, ext := splitNameExt(seg.Name())
			switch name {
			case "init":
				trd.trName = dir.Name()
				trd.ext = ext
			default:
				nr, err := strconv.Atoi(name)
				assert.NoError(t, err)
				if trd.minNr == 0 || nr < trd.minNr {
					trd.minNr = nr
				}
				if nr > trd.maxNr {
					trd.maxNr = nr
				}
			}
		}
		trDatas = append(trDatas, trd)
	}
	minSegNr := trDatas[0].minNr
	maxSegNr := trDatas[0].maxNr
	for _, trd := range trDatas {
		if trd.minNr != minSegNr || trd.maxNr != maxSegNr {
			t.Fatalf("Track %s has different min/max numbers", trd.trName)
		}
	}
	return trDatas
}

func sendSegment(t *testing.T, client *http.Client, serverURL, prefix, srcDir, chName, trName,
	segmentName, ext string, streamsURL bool) {
	t.Helper()
	segPath := filepath.Join(srcDir, chName, trName, segmentName+ext)
	segmentData, err := os.ReadFile(segPath)
	require.NoError(t, err)
	buf := bytes.NewBuffer(segmentData)
	var url string
	if streamsURL {
		url = fmt.Sprintf("%s%s/%s/Streams(%s%s)", serverURL, prefix, chName, trName, ext)
	} else {
		url = fmt.Sprintf("%s%s/%s/%s/%s%s", serverURL, prefix, chName, trName, segmentName, ext)
	}
	req, err := http.NewRequest(http.MethodPut, url, buf)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func splitNameExt(name string) (string, string) {
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)], ext
}

func testDirExists(t *testing.T, dirPath string) bool {
	t.Helper()
	into, err := os.Stat(dirPath)
	if !os.IsNotExist(err) {
		return into.IsDir()
	}
	return false
}

func testFileExists(t *testing.T, filePath string) bool {
	t.Helper()

	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
