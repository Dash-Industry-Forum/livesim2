package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/require"
)

func TestCmafIngesterMgr(t *testing.T) {
	// Create a new server with a test configuration
	// Then it is started, create a CMAF ingester manager with the server
	// Create a HTTP server that can receive data
	// Create a new CMAF ingester with the manager
	// Check that init segments are sent to the receiving server

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
	_, err = cm.NewCmafIngester(CmafIngesterRequest{"", "", "http://localhost:8080", "/livesim2/testpic_2s/Manifest.mpd", nil})
	require.Error(t, err)
	cm.Start()

	rc := newCmafReceiverTestServer()
	recServer := httptest.NewServer(rc)
	defer recServer.Close()

	// cId, err := cm.NewCmafIngester(CmafIngesterRequest{"", "", recServer.URL, "/livesim2/segtimeline_1/ato_1.5/chunkdur_1.5/testpic_2s/Manifest.mpd", mpd.Ptr(int(10000))})
	cId, err := cm.NewCmafIngester(CmafIngesterRequest{"", "", recServer.URL, "/livesim2/segtimeline_1/testpic_2s/Manifest.mpd", mpd.Ptr(int(10000))})
	require.NoError(t, err)
	require.Equal(t, uint64(1), cId, "first CMAF ingester ID should be 1")
	cI := cm.ingesters[cId]
	require.NotNil(t, cI, "CMAF ingester should be created")
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, 1000*time.Second)
	go cI.start(ctx)
	cI.triggerNextSegment()
	//time.Sleep(200 * time.Millisecond)
	cI.triggerNextSegment()
	time.Sleep(1000 * time.Millisecond)
	// Now we need to check that the init segments are received
	require.Equal(t, 6, len(rc.receivedSegments), "should have received 2 init segments and 4 media segments")
	cancel()
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
	segmentName := r.URL.Path

	if r.Header.Get("Content-Length") != "" {
		contentLen, _ := strconv.Atoi(r.Header.Get("Content-Length"))
		buf := make([]byte, contentLen)
		n, err := r.Body.Read(buf)
		if err != nil && err != io.EOF {
			w.WriteHeader(http.StatusInternalServerError)
		}
		if n != contentLen {
			w.WriteHeader(http.StatusBadRequest)
		}
		s.receivedSegments[segmentName] = buf
		w.WriteHeader(http.StatusOK)
		return
	}

	buf := make([]byte, 1024*1024)
	for {
		n, err := r.Body.Read(buf)
		if err != nil && err != io.EOF {
			w.WriteHeader(http.StatusInternalServerError)
		}
		if n > 0 {
			// Data is partial
			s.receivedPartialSegments[segmentName] = append(s.receivedPartialSegments[segmentName], buf[:n]...)
			continue
		}
		if err == io.EOF {
			// Data is complete
			s.receivedSegments[segmentName] = s.receivedPartialSegments[segmentName]
			delete(s.receivedPartialSegments, segmentName)
		}
		// Read the data from the request
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Check if the data is complete
		if r.Header.Get("Content-Length") == "" {
			// Data is partial
			s.receivedPartialSegments[segmentName] = append(s.receivedPartialSegments[segmentName], data...)
			w.WriteHeader(http.StatusOK)
			return
		}
	}
}
