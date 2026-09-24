package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/chunkparser"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/require"
)

func TestCmafIngesterMgr(t *testing.T) {
	// Create a new server with a test configuration
	// Then it is started, create a CMAF ingester manager with the server
	// Create a HTTP server that can receive data
	// Create a new CMAF ingester with the manager
	// Check that init and media segments are received.
	// TODO: Add test for DRM

	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogText,
		LogLevel:  "debug",
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	cm := NewCmafIngesterMgr(server)
	cm.Start()

	cases := []struct {
		livesimURL         string
		testNowMS          *int
		streamsURLs        bool
		nrTriggers         int
		expectedNrSegments int
	}{
		{"/livesim2/segtimeline_1/ato_1/chunkdur_1000/testpic_2s/Manifest.mpd", mpd.Ptr(int(10000)), false, 2, 4},
		{"/livesim2/segtimeline_1/testpic_2s/Manifest.mpd", mpd.Ptr(int(10000)), true, 2, 2},
		{"/livesim2/segtimeline_1/testpic_2s/Manifest.mpd", mpd.Ptr(int(10000)), false, 2, 6},
	}

	for i, c := range cases {
		rc := newCmafReceiverTestServer()
		recServer := httptest.NewServer(rc)
		setup := CmafIngesterSetup{
			User:        "",
			PassWord:    "",
			DestRoot:    recServer.URL,
			DestName:    "testpic_ingest",
			URL:         c.livesimURL,
			TestNowMS:   c.testNowMS,
			Duration:    nil,
			StreamsURLs: c.streamsURLs,
		}
		cId, err := cm.NewCmafIngester(setup)
		require.NoError(t, err)
		require.Equal(t, uint64(i+1), cId, "CMAF ingester ID be one-based and increase by 1")
		cI := cm.ingesters[cId]
		require.NotNil(t, cI, "CMAF ingester should be created")
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, 1000*time.Second)
		go cI.start(ctx)
		for j := 0; j < c.nrTriggers; j++ {
			cI.triggerNextSegment()
		}
		time.Sleep(500 * time.Millisecond)
		// Now we need to check that the segments are received
		rc.mu.Lock()
		nrReceived := len(rc.receivedSegments)
		rc.mu.Unlock()
		require.Equal(t, c.expectedNrSegments, nrReceived, "Number of segments received")
		cancel()
		recServer.Close()
	}
}

type cmafReceiverTestServer struct {
	mu                      sync.Mutex
	receivedSegments        map[string][]byte
	receivedPartialSegments map[string][]byte
}

func newCmafReceiverTestServer() *cmafReceiverTestServer {
	return &cmafReceiverTestServer{
		receivedSegments:        make(map[string][]byte),
		receivedPartialSegments: make(map[string][]byte),
	}
}

// ServeHTTP implements the http.Handler interface
// It is used to receive data from a CMAF ingester
// that sends data using PUT requests.
// The data can either be a full segment or a stream
// sent using HTTP Chunked-Transfer-Encoding so that it grows over time.
// The data is stored in the receivedSegments map if complete,
// but non-complete data is stored in the receivedPartialSegments map until completed.
func (s *cmafReceiverTestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if the request is a PUT request
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get the segment name from the URL path
	segmentName := r.URL.Path[1:]

	ingestVersion := r.Header.Get("DASH-IF-Ingest")
	if ingestVersion != CMAFIngestVersion {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if r.Header.Get("Content-Length") != "" { // Receive full segment based on Content-Length
		contentLen, _ := strconv.Atoi(r.Header.Get("Content-Length"))
		buf := make([]byte, contentLen)
		n, err := io.ReadFull(r.Body, buf)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		if n != contentLen {
			w.WriteHeader(http.StatusBadRequest)
		}
		s.mu.Lock()
		s.receivedSegments[segmentName] = buf
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	ci := make([]chunkparser.ChunkData, 0, 2)

	buf := make([]byte, 32*1024)
	cp := chunkparser.NewMP4ChunkParser(r.Body, buf, func(cd chunkparser.ChunkData) error {
		ci = append(ci, cd)
		return nil
	})

	err := cp.Parse()
	if err == nil {
		s.mu.Lock()
		for _, c := range ci {
			s.receivedSegments[segmentName] = append(s.receivedSegments[segmentName], c.Data...)
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	slog.Error("Failed to parse MP4 chunk", "err", err)
}

// TestCmafSourceChunkedNoHoldBack checks that all bytes of a Write reach the
// receiver before the next Write is made, so that no part of a CMAF chunk
// is delayed until the next chunk is produced.
func TestCmafSourceChunkedNoHoldBack(t *testing.T) {
	received := make(chan int, 100)
	recServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16*1024)
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				received <- n
			}
			if err != nil {
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer recServer.Close()

	cs := newCmafSource(slog.Default(), recServer.URL+"/seg.m4s", "video", "", "", true)
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		cs.sendChunked(context.Background())
	}()

	waitForBytes := func(want int) {
		t.Helper()
		got := 0
		timeout := time.After(2 * time.Second)
		for got < want {
			select {
			case n := <-received:
				got += n
			case <-timeout:
				t.Fatalf("received %d of %d bytes before next write", got, want)
			}
		}
		require.Equal(t, want, got)
	}

	// Sizes below, at, and above the 32KB io.Copy buffer size of the HTTP client
	for _, size := range []int{100, 32 * 1024, 60_000, 100_000} {
		n, err := cs.Write(make([]byte, size))
		require.NoError(t, err)
		require.Equal(t, size, n)
		waitForBytes(size)
	}
	require.NoError(t, cs.pw.Close())
	<-sendDone
}

// TestCmafSourceChunkedReceiverGone checks that a writer is not blocked
// forever if the request fails.
func TestCmafSourceChunkedReceiverGone(t *testing.T) {
	recServer := httptest.NewServer(http.NotFoundHandler())
	url := recServer.URL + "/seg.m4s"
	recServer.Close()

	cs := newCmafSource(slog.Default(), url, "video", "", "", true)
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		cs.sendChunked(context.Background())
	}()
	writeErr := make(chan error, 1)
	go func() {
		var err error
		for err == nil {
			_, err = cs.Write(make([]byte, 1000))
		}
		writeErr <- err
	}()
	select {
	case err := <-writeErr:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked after failed request")
	}
	<-sendDone
}

// TestCmafIngesterSegmentAfterAvailability checks that a chunked segment sent
// after its availability time is paced against the current time, so that
// it is completed when it ends, rather than late by the time it was started late.
func TestCmafIngesterSegmentAfterAvailability(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogText,
		LogLevel:  "info",
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	cm := NewCmafIngesterMgr(server)
	cm.Start()

	var mu sync.Mutex
	completedMS := make(map[string]int)
	recServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		completedMS[r.URL.Path] = unixMS()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer recServer.Close()

	// 2s segments with 0.5s chunks. With ato 1.5s, a segment is available after its first chunk.
	setup := CmafIngesterSetup{
		DestRoot: recServer.URL,
		DestName: "testpic_ingest",
		URL:      "/livesim2/ato_1.5/chunkdur_0.5/testpic_2s/Manifest.mpd",
	}
	cId, err := cm.NewCmafIngester(setup)
	require.NoError(t, err)
	c := cm.ingesters[cId]

	// Pick the segment in progress, and start sending it at least minLateMS after its availability time
	const minLateMS = 500
	segNr := findLastSegNr(c.cfg, c.asset, unixMS(), c.asset.refRep) + 1
	availMS, err := calcSegmentAvailabilityTime(c.asset, c.asset.refRep, uint32(segNr), c.cfg)
	require.NoError(t, err)
	if wait := int(availMS) + minLateMS - unixMS(); wait > 0 {
		time.Sleep(time.Duration(wait) * time.Millisecond)
	}
	lateMS := unixMS() - int(availMS)
	segEndMS := int(availMS) + 1500

	err = c.sendMediaSegments(context.Background(), segNr, int(availMS), false)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, completedMS, len(c.repsData))
	for path, doneMS := range completedMS {
		delayMS := doneMS - segEndMS
		require.Less(t, delayMS, lateMS/2, "segment %s completed %dms after its end, started %dms late",
			path, delayMS, lateMS)
	}
}
