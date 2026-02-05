package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Eyevinn/dash-mpd/mpd"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func finalTestClose(closer io.Closer) {
	if err := closer.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to close: %s\n", err)
	}
}

func TestReceivingMediaLiveInput(t *testing.T) {
	cases := []struct {
		name             string
		streamsURLFormat bool
		startNr          int
	}{
		{name: "StreamsFormat with startNr=1", streamsURLFormat: true, startNr: 1},            // MediaLive case
		{name: "SegmentFormat startNr=0, seqNrShift=-1", streamsURLFormat: false, startNr: 0}, // The shift is autodetected
	}

	err := logging.InitSlog("info", "json")
	assert.NoError(t, err)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "ew-cmaf-ingest-test")
			assert.NoError(t, err)
			chName := "awsMediaLiveScte35"
			srcDir := filepath.Join("testdata")
			opts := Options{
				prefix:                "/upload",
				timeShiftBufferDepthS: 30,
				storage:               tmpDir,
			}

			ctx, cancel := context.WithCancel(context.Background())
			cfg := &Config{
				Channels: []ChannelConfig{
					{Name: chName, StartNr: c.startNr},
				},
			}
			receiver, err := NewReceiver(ctx, &opts, cfg)
			require.NoError(t, err)
			// Cleanup: cancel context, wait for goroutines, then remove temp dir
			defer func() {
				cancel()
				receiver.WaitAll()
				finalRemove(tmpDir)
			}()
			router := setupRouter(receiver, opts.storage, "files")
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
						trd.ext, c.streamsURLFormat, http.StatusOK)
				}
				wg.Done()
			}()
			wg.Wait()
			// Check what has been uploaded tmpDir/chName
			dstDir := filepath.Join(tmpDir, chName)
			assert.True(t, testDirExists(dstDir), "channel directory should exist")
			trDirs, err := os.ReadDir(dstDir)
			assert.NoError(t, err)
			require.Equal(t, 3, len(trDirs), "there should be 3 tracks written")
			for _, trd := range testTrackData {
				assert.True(t, testDirExists(filepath.Join(dstDir, trd.trName)), fmt.Sprintf("%s directory should exist", trd.trName))
				assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, "init"+trd.ext)),
					fmt.Sprintf("%s/init%s should exist", trd.trName, trd.ext))
				assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, "init_org"+trd.ext)),
					fmt.Sprintf("%s/init_org%s should exist", trd.trName, trd.ext))
			}

			// Send first media segments and check that they have been received
			wg = sync.WaitGroup{}
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat, http.StatusOK)
				}
				wg.Done()
			}()
			wg.Wait()
			time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
			// Check that first media segment has been written
			for _, trd := range testTrackData {
				assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, fmt.Sprintf("%d%s", trd.minNr-c.startNr, trd.ext))),
					fmt.Sprintf("%s/%d%s should exist", trd.trName, trd.minNr-c.startNr, trd.ext))
			}

			// Send the second media segments. Since the duration is not the same, there should be no content_info.json written.
			wg = sync.WaitGroup{}
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr+1)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat, http.StatusOK)
				}
				wg.Done()
			}()
			wg.Wait()
			time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing
			assert.True(t, !testFileExists(filepath.Join(dstDir, "content_info.json")), "content_info.json should not exist yet")
			// Check that the second media segment has been written
			for _, trd := range testTrackData {
				assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, fmt.Sprintf("%d%s", trd.minNr+1-c.startNr, trd.ext))),
					fmt.Sprintf("%s/%d%s should exist", trd.trName, trd.minNr+1-c.startNr, trd.ext))
			}
			// Send the third media segments. Since the duration of segment 2 and 3 are the same, manifest and content_info
			// should have been written unless shifted.
			wg = sync.WaitGroup{}
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr+2)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat, http.StatusOK)
				}
				wg.Done()
			}()
			wg.Wait()
			time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
			ch, ok := receiver.channelMgr.GetChannel(chName)
			assert.True(t, ok, "channel should exist")
			assert.True(t, testFileExists(filepath.Join(dstDir, "manifest.mpd")), "manifest.mpd should exist")
			require.False(t, testFileExists(filepath.Join(dstDir, timelineNrMPD)), "manifest_time_nrß.mpd should not exist")

			var videoTrackData trTestData
			for _, trd := range testTrackData {
				if trd.trName == "video" {
					videoTrackData = trd
				}
			}
			if !ch.isShifted() {
				firstSeqNr, ok := ch.segTimesGen.getBufferFirstSeqNr("video")
				assert.True(t, ok, "segment data buffer should have items")
				assert.Equal(t, firstSeqNr, uint32(videoTrackData.minNr+1-c.startNr),
					"first sequence number in segment data buffer should be the second segment")
			} else {
				nrItems := ch.segTimesGen.getBufferNrItems("video")
				assert.Equal(t, 0, int(nrItems), "shifted segment data buffer should be empty")
			}

			// Send the fourth media segments.
			// If shifted, there should be segments in the SegmentTimel line MPD, if not, just one segment.
			wg.Add(1)
			go func() {
				for _, trd := range testTrackData {
					segName := fmt.Sprintf("%d", trd.minNr+3)
					sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, segName,
						trd.ext, c.streamsURLFormat, http.StatusOK)
				}
				wg.Done()
			}()
			wg.Wait()
			time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
			assert.True(t, testFileExists(filepath.Join(dstDir, timelineNrMPD)), "manifest_timeline_nr.mpd should now exist")
			data, err := os.ReadFile(filepath.Join(dstDir, timelineNrMPD))
			require.NoError(t, err)
			manifest, err := mpd.MPDFromBytes(data)
			require.NoError(t, err)
			assert.Equal(t, 1, len(manifest.Periods), "there should be 1 period")
			assert.Equal(t, 2, len(manifest.Periods[0].AdaptationSets), "there should be 2 adaptation sets")
			for _, as := range manifest.Periods[0].AdaptationSets {
				stl := as.SegmentTemplate
				assert.NotNil(t, stl, "segment template should exist")
				nrSegments := 0
				for _, s := range stl.SegmentTimeline.S {
					nrSegments += int(s.R) + 1
				}
				if ch.isShifted() { // Just one segment
					require.Equal(t, 896605657, int(*stl.StartNumber), "start number should be 896605657")
					require.Equal(t, 1, nrSegments, "number of segments should be 1")
				} else {
					require.Equal(t, 896605655, int(*stl.StartNumber), "start number should be 896605655")
					require.Equal(t, 3, nrSegments, "number of segments should be 3")

				}
			}

			// Check that the manifest fetched from the file server is the same as the one in the storage
			client := http.DefaultClient
			req, err := http.NewRequest("GET", fmt.Sprintf("%s/files/%s/manifest.mpd", server.URL, chName), nil)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			mpdHttp, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			mpdDisk, err := os.ReadFile(filepath.Join(dstDir, "manifest.mpd"))
			require.NoError(t, err)
			if !bytes.Equal(mpdHttp, mpdDisk) {
				t.Errorf("mpdDisk: %s differs from mpdDiskHttp: %s", string(mpdDisk), string(mpdHttp))
			}
		})

	}
}

// TestReceivingNonIdealInput tests the receiving of segments from a less ideal encoder.
// Here, the init segments may be late, and are never resent.
// We therefore check for init_org at start and reuse.
// Other special things are
// [X] No init segments at start (came after 48 segments)
// [X] Drop media segment until init segment received
// [X] Read and reuse an earlier init segment (distinguish original and changed). The original is stored as init_org.cmfx
//
//	The creation time is today, not 1970
//
// [X] Detect and use 1970-01-01
//
//	The sequence numbers are low, not related to 1970, and cannot be used
//
// [X] Detect far from time and do not use. Don't store first media segment
// [X] Calculate number from incoming time after two segments with same duration (after adjustment)
//
//	The sequence number may be different for different streams
//
// [X] Rewrite timescale for subtitles from 50000 to 1000
// [X] The baseMediaDecodeTime is not a multiple of the segment duration.
//
//	Thus not a multiple of 2s, but in fact .3282s after that.
//	We want multiples, so we need to shift input times. Probably increase as little as possible.
//	etect this and decide on shift for best alignment
//
// [X] Rewrite times of the segments to be multiples
// [X] All metadata boxes are missing like: btrt, kind (for role)
// [X] Roles are missing, so make it possible to configure
// [X] Add configuration of representations to config file, in particular roles and languages.
// [X] Sometimes there are outages so that one or more segments are missing. We need to continue once the segments are back
// [X] Allow for holes, and continue generating SegmentTimeline once all tracks have segments.
func TestReceivingNonIdealInput(t *testing.T) {
	streamsURLFormat := true
	tmpDir, err := os.MkdirTemp("", "ew-cmaf-ingest-test-nonideal")
	assert.NoError(t, err)
	chName := "zero_3.84s"
	srcDir := filepath.Join("testdata")
	opts := Options{
		prefix:                "/upload",
		timeShiftBufferDepthS: 30,
		storage:               tmpDir,
	}

	err = logging.InitSlog("info", "text")
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cfg := &Config{
		Channels: []ChannelConfig{
			{
				Name: chName,
				Reps: []RepresentationConfig{
					{Name: "text-nor-0", Role: "subtitle", DisplayName: "Norwegian subtitles"},
					{Name: "text-nor-1_hearing_impaired", Role: "caption", DisplayName: "Norwegian hearing impaired"},
				},
			},
		},
	}
	receiver, err := NewReceiver(ctx, &opts, cfg)
	require.NoError(t, err)
	// Cleanup: cancel context, wait for goroutines, then remove temp dir
	defer func() {
		cancel()
		receiver.WaitAll()
		finalRemove(tmpDir)
	}()
	router := setupRouter(receiver, opts.storage, "")
	server := httptest.NewServer(router)
	defer server.Close()
	t.Logf("Server started with prefix: %s at %s", opts.prefix, server.URL)
	testTrackData := createTrackTestData(t, srcDir, chName)
	err = addOrigInitSegments(srcDir, chName, tmpDir, testTrackData)
	require.NoError(t, err)

	// Send the first media segments. That should trigger reading of init_org
	wg := sync.WaitGroup{}
	nextNr := 0
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go func() {
			for _, trd := range testTrackData {
				sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, fmt.Sprintf("%d", nextNr),
					trd.ext, streamsURLFormat, http.StatusOK)
			}
			wg.Done()
		}()
		wg.Wait()
		nextNr++
	}
	// Check what has been uploaded tmpDir/chName
	dstDir := filepath.Join(tmpDir, chName)
	assert.True(t, testDirExists(dstDir), "channel directory should exist")
	trDirs, err := os.ReadDir(dstDir)
	assert.NoError(t, err)
	ch := receiver.channelMgr.channels[chName]
	assert.NotNil(t, ch, "channel should exist")
	require.Equal(t, 5, len(trDirs), "there should be 5 tracks written with init segments and one segment in each")
	for _, trd := range testTrackData {
		assert.True(t, testDirExists(filepath.Join(dstDir, trd.trName)), fmt.Sprintf("%s directory should exist", trd.trName))
		assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, "init"+trd.ext)),
			fmt.Sprintf("%s/init%s should exist", trd.trName, trd.ext))
		td := ch.trDatas[trd.trName]
		switch contentType := ch.trDatas[trd.trName].contentType; contentType {
		case "video":
			assert.Equal(t, 50000, int(td.timeScaleIn), "video input timescale should be 50000")
			assert.Equal(t, 50000, int(td.timeScaleOut), "video output timescale should be 50000")
		case "audio":
			assert.Equal(t, 48000, int(td.timeScaleIn), "audio input timescale should be 48000")
			assert.Equal(t, 48000, int(td.timeScaleOut), "audio output timescale should be 48000")
		case "text":
			assert.Equal(t, 50000, int(td.timeScaleIn), "text input timescale should be 50000")
			assert.Equal(t, 1000, int(td.timeScaleOut), "text output timescale should be 1000")
		default:
			t.Fatalf("Unknown content type %s", contentType)
		}
		assert.Equal(t, 0, int(ch.startTime), "startTime should be zero")
		assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, fmt.Sprintf("%d%s", 8090, trd.ext))))
	}

	// Send second media segments and check that they have been received
	wg = sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for _, trd := range testTrackData {
			sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, fmt.Sprintf("%d", nextNr),
				trd.ext, streamsURLFormat, http.StatusOK)
		}
		wg.Done()
	}()
	wg.Wait()
	nextNr++
	time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
	// Check that second segment has been written to nr 8091, except for second video track which has been updated
	// It is updated because data from the first video track is used to determine the shift.
	seqNr := 8091
	for _, trd := range testTrackData {
		if trd.ext == ".cmfv" && trd.trName != ch.masterTrName {
			seqNr = 449002889
		}
		fName := fmt.Sprintf("%d%s", seqNr, trd.ext)
		assert.True(t, testFileExists(filepath.Join(dstDir, trd.trName, fName)), fmt.Sprintf("%s should exist", fName))
	}
	// Check that metadata has been written (two segments with same duration)
	assert.True(t, testFileExists(filepath.Join(dstDir, "manifest.mpd")), "manifest.mpd should exist")
	data, err := os.ReadFile(filepath.Join(dstDir, "manifest.mpd"))
	require.NoError(t, err)
	manifest, err := mpd.MPDFromBytes(data)
	require.NoError(t, err)
	assert.Equal(t, 1, len(manifest.Periods), "there should be 1 period")
	assert.Equal(t, 4, len(manifest.Periods[0].AdaptationSets), "there should be 4 adaptation sets")
	for _, as := range manifest.Periods[0].AdaptationSets {
		switch as.ContentType {
		case "video":
			assert.Equal(t, "video/mp4", as.MimeType, "video should have correct mime type")
			assert.Equal(t, "und", as.Lang, "video should have correct language")
			assert.Equal(t, "video", string(as.ContentType), "video should have correct content type")
			assert.Equal(t, 2, len(as.Representations), "video should have 2 representations")
		case "audio":
			assert.Equal(t, "audio/mp4", as.MimeType, "audio should have correct mime type")
		case "text":
			for _, rep := range as.Representations {
				switch rep.Id {
				case "text-nor-0":
					assert.Equal(t, "Norwegian subtitles", string(rep.Labels[0].Value), "Norwegian subtitles should have correct label")
					assert.Equal(t, "subtitle", as.Roles[0].Value, "Norwegian subtitles should have correct role")
				case "text-nor-1_hearing_impaired":
					assert.Equal(t, "Norwegian hearing impaired", string(rep.Labels[0].Value),
						"Norwegian hearing impaired should have correct label")
					assert.Equal(t, "caption", as.Roles[0].Value, "Norwegian hearing impaired should have correct role")
				default:
					t.Fatalf("Unknown representation %s", rep.Id)
				}
			}
		}
	}
	assert.False(t, testFileExists(filepath.Join(dstDir, timelineNrMPD)))

	// Send the third media segments. Since segment duration for video is the same, segment data should be written to SegmentTimeline.
	wg = sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for _, trd := range testTrackData {
			sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, fmt.Sprintf("%d", nextNr),
				trd.ext, streamsURLFormat, http.StatusOK)
		}
		wg.Done()
	}()
	wg.Wait()
	nextNr++
	time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
	assert.True(t, testFileExists(filepath.Join(dstDir, timelineNrMPD)), "manifest_timeline_nr.mpd should now exist")
	data, err = os.ReadFile(filepath.Join(dstDir, timelineNrMPD))
	require.NoError(t, err)
	manifest, err = mpd.MPDFromBytes(data)
	require.NoError(t, err)
	assert.Equal(t, 1, len(manifest.Periods), "there should be 1 period")
	assert.Equal(t, 4, len(manifest.Periods[0].AdaptationSets), "there should be 4 adaptation sets")
	for _, as := range manifest.Periods[0].AdaptationSets {
		stl := as.SegmentTemplate
		assert.NotNil(t, stl, "segment template should exist")
		assert.True(t, testFileExists(filepath.Join(dstDir, timelineNrMPD)), "manifest_timeline_nr.mpd should now exist")
		data, err = os.ReadFile(filepath.Join(dstDir, timelineNrMPD))
		require.NoError(t, err)
		manifest, err = mpd.MPDFromBytes(data)
		require.NoError(t, err)
		assert.Equal(t, 1, len(manifest.Periods), "there should be 1 period")
		assert.Equal(t, 4, len(manifest.Periods[0].AdaptationSets), "there should be 4 adaptation sets")
		for _, as := range manifest.Periods[0].AdaptationSets {
			stl := as.SegmentTemplate
			assert.NotNil(t, stl, "segment template should exist")
			assert.Equal(t, 449002890, int(*stl.StartNumber))
			data, err = os.ReadFile(filepath.Join(dstDir, timelineNrMPD))
			require.NoError(t, err)
			manifest, err = mpd.MPDFromBytes(data)
			require.NoError(t, err)
			assert.Equal(t, 1, len(manifest.Periods), "there should be 1 period")
			assert.Equal(t, 4, len(manifest.Periods[0].AdaptationSets), "there should be 4 adaptation sets")
			for _, as := range manifest.Periods[0].AdaptationSets {
				stl := as.SegmentTemplate
				assert.NotNil(t, stl, "segment template should exist")
				assert.Equal(t, 449002890, int(*stl.StartNumber), "start number should be 449002890")
			}
		}
	}

	// Next, let us have a jump in the sequence numbers, so segmentTimes should have one new number.
	// There should still be one interval, since we don't generate multi-interval segmentTimes (yet).
	nextNr++ // Skipping one
	wg.Add(1)
	go func() {
		for _, trd := range testTrackData {
			sendSegment(t, http.DefaultClient, server.URL, opts.prefix, srcDir, chName, trd.trName, fmt.Sprintf("%d", nextNr),

				trd.ext, streamsURLFormat, http.StatusOK)
		}
		wg.Done()
	}()
	wg.Wait()
	nextNr++
	time.Sleep(50 * time.Millisecond) // Need to finish the asynchronous writing of metadata
	require.NoError(t, err)
	assert.True(t, testFileExists(filepath.Join(dstDir, timelineNrMPD)))
	data, err = os.ReadFile(filepath.Join(dstDir, timelineNrMPD))
	require.NoError(t, err)
	manifest, err = mpd.MPDFromBytes(data)
	require.NoError(t, err)
	assert.Equal(t, 4, len(manifest.Periods[0].AdaptationSets), "there should be 4 adaptation sets")
	for _, as := range manifest.Periods[0].AdaptationSets {
		stl := as.SegmentTemplate
		assert.NotNil(t, stl, "segment template should exist")
		assert.Equal(t, 449002892, int(*stl.StartNumber), "start number should be 449002892")
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
			if trd.trName == "" {
				trd.trName = dir.Name()
				trd.ext = ext
			}
			switch name {
			case "init", "init_org":
				// Do nothing
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
	segmentName, ext string, streamsURL bool, expectedStatusCode int) {
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
	slog.Info("Sending segment", "url", url, "segPath", segPath)
	req, err := http.NewRequest(http.MethodPut, url, buf)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, expectedStatusCode, resp.StatusCode, "status code should be as expected")
}

func splitNameExt(name string) (string, string) {
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)], ext
}

func testDirExists(dirPath string) bool {
	into, err := os.Stat(dirPath)
	if !os.IsNotExist(err) {
		return into.IsDir()
	}
	return false
}

func testFileExists(filePath string) bool {

	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

func addOrigInitSegments(srcDir, chName, tmpDir string, ttd []trTestData) error {
	for _, trd := range ttd {
		srcInitPath := filepath.Join(srcDir, chName, trd.trName, "init_org"+trd.ext)
		if !fileExists(srcInitPath) {
			continue
		}
		dstDir := filepath.Join(tmpDir, chName, trd.trName)
		err := os.MkdirAll(dstDir, 0755)
		if err != nil {
			return err
		}
		dstInitPath := filepath.Join(dstDir, "init_org"+trd.ext)
		ifh, err := os.Open(srcInitPath)
		if err != nil {
			return err
		}
		defer finalTestClose(ifh)
		ofh, err := os.Create(dstInitPath)
		if err != nil {
			return err
		}
		defer finalTestClose(ofh)
		_, err = io.Copy(ofh, ifh)
		if err != nil {
			return err
		}
	}
	return nil
}
