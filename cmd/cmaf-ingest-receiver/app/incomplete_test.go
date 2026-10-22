package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReceivingIncompleteSegments checks how the receiver handles a video segment that
// does not arrive in full, after three complete segments of all tracks.
func TestReceivingIncompleteSegments(t *testing.T) {
	cases := []struct {
		desc string
		// body returns the request body and its Content-Length (-1 for chunked transfer encoding)
		body       func(data []byte, frags [2][]byte) (io.Reader, int64)
		wantStatus int // 0 if the request is aborted by the sender
		// wantDurs are the durations of the video segments in the SegmentTimeline, of which
		// the last is that of the test segment if it is accepted.
		wantDurs []uint64
		wantWarn bool
	}{
		{
			desc: "complete",
			body: func(data []byte, _ [2][]byte) (io.Reader, int64) {
				return bytes.NewReader(data), int64(len(data))
			},
			wantStatus: http.StatusOK,
			wantDurs:   []uint64{172800, 172800, 172800},
		},
		{
			desc: "ends in the mdat",
			body: func(data []byte, _ [2][]byte) (io.Reader, int64) {
				short := data[:len(data)-50_000]
				return bytes.NewReader(short), int64(len(short))
			},
			wantStatus: http.StatusBadRequest,
			wantDurs:   []uint64{172800, 172800},
		},
		{
			desc: "aborted in the second chunk",
			body: func(_ []byte, frags [2][]byte) (io.Reader, int64) {
				pr, pw := io.Pipe()
				go func() {
					_, _ = pw.Write(frags[0])
					_, _ = pw.Write(frags[1][:len(frags[1])/2])
					pw.CloseWithError(fmt.Errorf("source gone"))
				}()
				return pr, -1
			},
			wantDurs: []uint64{172800, 172800},
		},
		{
			desc: "ends after the first chunk",
			body: func(_ []byte, frags [2][]byte) (io.Reader, int64) {
				return bytes.NewReader(frags[0]), -1
			},
			wantStatus: http.StatusOK,
			wantDurs:   []uint64{172800, 172800, 86400},
			wantWarn:   true,
		},
	}
	logs := &lockedBuffer{}
	defaultLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(defaultLogger) })

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			tmpDir := t.TempDir()
			logs.Reset()
			synctest.Test(t, func(t *testing.T) {
				chName := "awsMediaLiveScte35"
				srcDir := filepath.Join("testdata")
				opts := Options{prefix: "/upload", timeShiftBufferDepthS: 30, storage: tmpDir}
				ctx, cancel := context.WithCancel(context.Background())
				cfg := &Config{Channels: []ChannelConfig{{Name: chName, StartNr: 1}}}
				receiver, err := NewReceiver(ctx, &opts, cfg)
				require.NoError(t, err)
				defer func() {
					cancel()
					receiver.WaitAll()
				}()
				server := httptest.NewTestServer(t, setupRouter(receiver, opts.storage, ""))
				client := server.Client()
				testTrackData := createTrackTestData(t, srcDir, chName)
				for _, trd := range testTrackData {
					sendSegment(t, client, server.URL, opts.prefix, srcDir, chName, trd.trName, "init", trd.ext, false, http.StatusOK)
				}
				for i := range 3 {
					for _, trd := range testTrackData {
						sendSegment(t, client, server.URL, opts.prefix, srcDir, chName, trd.trName, fmt.Sprint(trd.minNr+i),
							trd.ext, false, http.StatusOK)
					}
					synctest.Wait()
				}

				nr := testTrackData[0].minNr + 3
				for _, trd := range testTrackData {
					if trd.trName != "video" {
						sendSegment(t, client, server.URL, opts.prefix, srcDir, chName, trd.trName, fmt.Sprint(nr), trd.ext,
							false, http.StatusOK)
						continue
					}
					segPath := filepath.Join(srcDir, chName, trd.trName, fmt.Sprintf("%d%s", nr, trd.ext))
					data, err := os.ReadFile(segPath)
					require.NoError(t, err)
					body, contentLength := c.body(data, splitInTwoChunks(t, srcDir, chName, trd, data))
					url := fmt.Sprintf("%s%s/%s/%s/%d%s", server.URL, opts.prefix, chName, trd.trName, nr, trd.ext)
					req, err := http.NewRequest(http.MethodPut, url, body)
					require.NoError(t, err)
					req.ContentLength = contentLength
					resp, err := client.Do(req)
					if c.wantStatus == 0 {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						require.NoError(t, resp.Body.Close())
						require.Equal(t, c.wantStatus, resp.StatusCode)
					}
				}
				synctest.Wait()

				// Only a segment that is accepted is stored.
				segFile := filepath.Join(tmpDir, chName, "video", fmt.Sprintf("%d.cmfv", nr-1))
				assert.Equal(t, c.wantStatus == http.StatusOK, testFileExists(segFile), "%s stored", segFile)
				manifest := readMPD(t, filepath.Join(tmpDir, chName, timelineNrMPD))
				var durs []uint64
				for _, as := range manifest.Periods[0].AdaptationSets {
					if as.ContentType != "video" {
						continue
					}
					for _, s := range as.SegmentTemplate.SegmentTimeline.S {
						for range s.R + 1 {
							durs = append(durs, s.D)
						}
					}
				}
				assert.Equal(t, c.wantDurs, durs, "video segment durations in the SegmentTimeline")
			})
			assert.Equal(t, c.wantWarn, bytes.Contains(logs.Bytes(), []byte("possibly incomplete")), "short segment warning")
		})
	}
}

// splitInTwoChunks splits the one-fragment segment data into two chunks: styp and a fragment
// with the first half of the samples, and a fragment with the rest.
func splitInTwoChunks(t *testing.T, srcDir, chName string, trd trTestData, data []byte) [2][]byte {
	t.Helper()
	initData, err := os.ReadFile(filepath.Join(srcDir, chName, trd.trName, "init"+trd.ext))
	require.NoError(t, err)
	initFile, err := mp4.DecodeFile(bytes.NewReader(initData))
	require.NoError(t, err)
	segFile, err := mp4.DecodeFile(bytes.NewReader(data))
	require.NoError(t, err)
	seg := segFile.Segments[0]
	frag := seg.Fragments[0]
	samples, err := frag.GetFullSamples(initFile.Init.Moov.Mvex.Trex)
	require.NoError(t, err)
	half := len(samples) / 2
	var chunks [2][]byte
	for i, part := range [][]mp4.FullSample{samples[:half], samples[half:]} {
		var buf bytes.Buffer
		if i == 0 {
			require.NoError(t, seg.Styp.Encode(&buf))
		}
		f, err := mp4.CreateFragment(frag.Moof.Mfhd.SequenceNumber, frag.Moof.Traf.Tfhd.TrackID)
		require.NoError(t, err)
		f.AddFullSamples(part)
		require.NoError(t, f.Encode(&buf))
		chunks[i] = buf.Bytes()
	}
	return chunks
}

// lockedBuffer is a bytes.Buffer that can be written from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// TestReceivingTooLargeChunk checks that an upload whose box header claims more than the
// maximum chunk size is answered with 413 as soon as the header is read, without
// allocating a buffer of the claimed size.
func TestReceivingTooLargeChunk(t *testing.T) {
	tmpDir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		opts := Options{prefix: "/upload", timeShiftBufferDepthS: 30, storage: tmpDir}
		ctx, cancel := context.WithCancel(context.Background())
		receiver, err := NewReceiver(ctx, &opts, &Config{})
		require.NoError(t, err)
		defer func() {
			cancel()
			receiver.WaitAll()
		}()
		server := httptest.NewTestServer(t, setupRouter(receiver, opts.storage, ""))
		client := server.Client()

		// An mdat header that claims 300 MB, sent with chunked transfer encoding.
		const claimed = 300 * 1024 * 1024
		body := binary.BigEndian.AppendUint32(nil, claimed)
		body = append(body, "mdat"...)
		body = append(body, make([]byte, 1000)...)
		req, err := http.NewRequest(http.MethodPut, server.URL+"/upload/ch/video/1.cmfv", io.NopCloser(bytes.NewReader(body)))
		require.NoError(t, err)
		req.ContentLength = -1
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
		assert.False(t, testFileExists(filepath.Join(tmpDir, "ch", "video", "1.cmfv")))
	})
}
