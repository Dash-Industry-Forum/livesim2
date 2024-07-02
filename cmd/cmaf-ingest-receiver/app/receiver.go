package app

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/chunkparser"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Receiver is a receiver of CMAF segments.
// There may be parallel full streams with their own set of representations (streams).
type Receiver struct {
	prefix        string
	storage       string
	streams       map[string]stream
	fullStreamMgr *FullStreamMgr
}

func NewReceiver(opts *Options) (*Receiver, error) {
	r := &Receiver{
		prefix:        opts.prefix,
		storage:       opts.storage,
		streams:       make(map[string]stream),
		fullStreamMgr: NewFullStreamMgr(opts.maxBufferS),
	}
	return r, nil
}

// SegmentHandlerFunc is a handler for receiving segments, but will also accept MPDs (extension .mpd).
func (r *Receiver) SegmentHandlerFunc(w http.ResponseWriter, req *http.Request) {
	// Extract the path and filename from URL
	// Drop the first part that should be /upload or similar as specified by prefix.
	path := strings.TrimPrefix(req.URL.Path, r.prefix)
	slog.Debug("Trimmed path", "path", path)
	assetDir, ok := matchMPD(r.storage, path)
	if ok {
		slog.Debug("Matched MPD", "path", path, "assetDir", assetDir)
		err := os.MkdirAll(assetDir, 0755)
		if err != nil {
			slog.Error("Failed to create directory", "err", err)
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			return
		}
		receivedMpdPath := filepath.Join(assetDir, "received.mpd")
		ofh, err := os.Create(receivedMpdPath)
		if err != nil {
			slog.Error("Failed to create file", "err", err)
			http.Error(w, "Failed to create file", http.StatusInternalServerError)
			return
		}
		defer ofh.Close()
		_, err = io.Copy(ofh, req.Body)
		if err != nil {
			slog.Error("Failed to write MPD", "err", err)
			http.Error(w, "Failed to write MPD", http.StatusInternalServerError)
			return
		}
		req.Body.Close()
		slog.Info("MPD received", "urlPath", path, "storedPath", receivedMpdPath)
		w.WriteHeader(http.StatusOK)
		return
	}
	stream, ok := findStreamMatch(r.storage, path)
	if !ok {
		http.Error(w, "Failed to find valid stream", http.StatusBadRequest)
		slog.Error("Failed to find valid stream", "path", path)
		return
	}
	if _, ok := r.streams[stream.name]; !ok {
		slog.Info("New representation", "urlPath", path, "stream", stream)
		r.streams[stream.name] = stream
		err := os.MkdirAll(stream.repDir, 0755)
		if err != nil {
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			return
		}
	}
	defer func() {
		slog.Debug("Closing body", "url", path)
		req.Body.Close()
	}()
	slog.Debug("Headers", "headers", req.Header)

	var contentLength int
	var err error
	if req.Header.Get("Content-Length") != "" {
		fmt.Println("Content-Length", req.Header.Get("Content-Length"))
		contentLength, err = strconv.Atoi(req.Header.Get("Content-Length"))
		if err != nil {
			http.Error(w, "Failed to parse Content-Length", http.StatusBadRequest)
			slog.Error("Failed to parse Content-Length", "err", err)
			return
		}
		slog.Debug("Content-Length", "contentLength", contentLength)
	}

	firstChunk := true
	var ofh *os.File

	fs, ok := r.fullStreamMgr.GetStream(stream.assetDir)
	if !ok {
		r.fullStreamMgr.AddStream(stream.assetDir)
		fs, _ = r.fullStreamMgr.GetStream(stream.assetDir)
	}

	chunkParserCallback := func(cd chunkparser.ChunkData) error {
		// Set ofh to the write file output and then write data
		slog.Info("Chunk received", "isInitSegment", cd.IsInitSegment, "len", len(cd.Data))
		if cd.IsInitSegment {
			filePath := filepath.Join(stream.repDir, fmt.Sprintf("%s%s", "init", stream.ext))
			slog.Info("Init segment received", "filePath", filePath)
			ofh, err = os.Create(filePath)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			err := fs.AddInitData(stream, cd.Data)
			if err != nil {
				slog.Error("Failed to find init data", "err", err)
			}
		} else {
			if firstChunk {
				slog.Debug("First chunk received", "url", path)
				firstChunk = false
				sr := bits.NewFixedSliceReader(cd.Data)
				chunk, err := mp4.DecodeFileSR(sr, mp4.WithDecodeFlags(mp4.DecFileFlags(mp4.DecModeLazyMdat)))
				if err != nil {
					return fmt.Errorf("failed to decode chunk: %w", err)
				}
				if len(chunk.Segments) == 0 || len(chunk.Segments[0].Fragments) == 0 {
					return fmt.Errorf("no segments or fragments in chunk")
				}
				moof := chunk.Segments[0].Fragments[0].Moof
				// Create new file path based on sequence number
				seqNr := moof.Mfhd.SequenceNumber
				newSegPath := filepath.Join(stream.repDir, fmt.Sprintf("%d%s", seqNr, stream.ext))
				ofh, err = os.Create(newSegPath)
				if err != nil {
					return fmt.Errorf("failed to create file: %w", err)
				}
				dts := moof.Traf.Tfdt.BaseMediaDecodeTime()
				err = fs.AddSegData(stream, seqNr, dts)
				if err != nil {
					return fmt.Errorf("failed to add segment data %w", err)
				}
				deleteSegPath := filepath.Join(stream.repDir, fmt.Sprintf("%d%s", int(seqNr)-fs.maxNrBufSegs, stream.ext))
				if fileExists(deleteSegPath) {
					slog.Debug("Deleting old segment", "path", deleteSegPath)
					err = os.Remove(deleteSegPath)
					if err != nil {
						return fmt.Errorf("failed to delete segment %w", err)
					}
				}
			}
		}
		defer ofh.Close()
		n, err := ofh.Write(cd.Data)
		if err != nil {
			return fmt.Errorf("failed to write chunk: %w", err)
		}
		if n != len(cd.Data) {
			return fmt.Errorf("failed to write all chunk bytes %d of %d", n, len(cd.Data))
		}
		return nil
	}

	slog.Info("Receiving file", "url", path, "contentLength", contentLength)
	var buf []byte
	if contentLength > 0 {
		buf = make([]byte, contentLength)
	} else {
		buf = make([]byte, 1024)
	}
	p := chunkparser.NewMP4ChunkParser(req.Body, buf, chunkParserCallback)
	err = p.Parse()
	if err != nil {
		slog.Error("Failed to parse MP4 chunk", "err", err)
		http.Error(w, "Failed to parse MP4 chunk", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// DeleteHandlerFunc is a handler for deleting segments. Not used since fixed timeshiftBufferDepth.
func (r *Receiver) DeleteHandlerFunc(w http.ResponseWriter, req *http.Request) {
	slog.Debug("DeleteHandlerFunc called", "url", req.URL.Path)
}

func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return !os.IsNotExist(err)
}
