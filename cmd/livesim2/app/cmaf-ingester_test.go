package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		require.Equal(t, c.expectedNrSegments, len(rc.receivedSegments), "Number of segments received")
		cancel()
		recServer.Close()
	}
}

type cmafReceiverTestServer struct {
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
		s.receivedSegments[segmentName] = buf
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
		for _, c := range ci {
			s.receivedSegments[segmentName] = append(s.receivedSegments[segmentName], c.Data...)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	slog.Error("Failed to parse MP4 chunk", "err", err)
}
