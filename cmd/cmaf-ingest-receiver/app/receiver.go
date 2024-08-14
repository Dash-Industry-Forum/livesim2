package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/chunkparser"
	"github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Receiver is a receiver of CMAF segments.
// There may be parallel full streams with their own set of tracks (streams).
type Receiver struct {
	ctx        context.Context
	prefix     string
	storage    string
	streams    map[string]stream // mapped by stream.id()
	channelMgr *ChannelMgr
}

func NewReceiver(ctx context.Context, opts *Options, cfg *Config) (*Receiver, error) {
	r := &Receiver{
		ctx:        ctx,
		prefix:     opts.prefix,
		storage:    opts.storage,
		streams:    make(map[string]stream),
		channelMgr: NewChannelMgr(cfg, opts.timeShiftBufferDepthS),
	}
	return r, nil
}

// SegmentHandlerFunc is a handler for receiving segments, but will also accept MPDs (extension .mpd).
func (r *Receiver) SegmentHandlerFunc(w http.ResponseWriter, req *http.Request) {
	// Extract the path and filename from URL
	// Drop the first part that should be /upload or similar as specified by prefix.
	path := strings.TrimPrefix(req.URL.Path, r.prefix)
	slog.Debug("Trimmed path", "path", path)
	chName, ok := matchMPD(path)
	if ok {
		slog.Debug("Matched MPD", "path", path, "chName", chName)
		err := os.MkdirAll(chName, 0755)
		if err != nil {
			slog.Error("Failed to create directory", "err", err)
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			return
		}
		receivedMpdPath := filepath.Join(chName, "received.mpd")
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
	//TODO. Include chName to avoid collisions between tracks with same name
	if _, ok := r.streams[stream.id()]; !ok {
		slog.Info("New stream", "urlPath", path, "streamId", stream.id(), "mediatype", stream.mediaType)
		r.streams[stream.id()] = stream
		err := os.MkdirAll(stream.trDir, 0755)
		if err != nil {
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			return
		}
	}
	defer func() {
		slog.Debug("Closing body", "url", path)
		req.Body.Close()
	}()
	slog.Debug("Headers", "path", path, "headers", req.Header)

	var contentLength int
	var err error
	if req.Header.Get("Content-Length") != "" {
		contentLength, err = strconv.Atoi(req.Header.Get("Content-Length"))
		if err != nil {
			http.Error(w, "Failed to parse Content-Length", http.StatusBadRequest)
			slog.Error("Failed to parse Content-Length", "err", err)
			return
		}
		slog.Debug("Content-Length", "path", path, "contentLength", contentLength)
	}

	var ofh *os.File

	ch, ok := r.channelMgr.GetChannel(stream.chName)
	if !ok {
		r.channelMgr.AddChannel(r.ctx, stream.chName, stream.chDir)
		slog.Debug("Created new full stream", "chName", stream.chName, "chDir", stream.chDir)
		ch, _ = r.channelMgr.GetChannel(stream.chName)
	}
	if ch.authUser != "" || ch.authPswd != "" {
		user, pswd, ok := req.BasicAuth()
		if !ok || user != ch.authUser || pswd != ch.authPswd {
			slog.Error("Unauthorized", "user", user, "chName", stream.chName)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	trd := &recSegData{name: stream.trName}
	defaultDur := mpd.Ptr(uint32(0))

	trName := stream.trName

	chunkParserCallback := func(cd chunkparser.ChunkData) error {
		// Set ofh to the write file output and then write data
		slog.Debug("Chunk received", "isInitSegment", cd.IsInitSegment, "len", len(cd.Data))
		data := cd.Data // Used so that you can overwrite cd.Data when needed
		if cd.IsInitSegment {
			filePath := filepath.Join(stream.trDir, fmt.Sprintf("%s%s", "init", stream.ext))
			slog.Debug("Init segment received", "filePath", filePath)
			ofh, err = os.Create(filePath)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			sr := bits.NewFixedSliceReader(cd.Data)
			iSeg, err := mp4.DecodeFileSR(sr)
			if err != nil {
				return fmt.Errorf("failed to decode init segment: %w", err)
			}
			init := iSeg.Init
			err = ch.addInitData(stream, init)
			if err != nil {
				slog.Error("Failed to find init data", "err", err)
			}
		} else {
			sr := bits.NewFixedSliceReader(cd.Data)
			chunk, err := mp4.DecodeFileSR(sr, mp4.WithDecodeFlags(mp4.DecFileFlags(mp4.DecModeLazyMdat)))
			if err != nil {
				slog.Error("Failed to decode chunk", "err", err, "chunkNr", trd.chunkNr, "chName", stream.chName, "trName", trName)
				return fmt.Errorf("failed to decode chunk %d: %w", trd.chunkNr, err)
			}
			slog.Debug("Chunk received", "chunkNr", trd.chunkNr, "chName", stream.chName, "trName", trName)

			if len(chunk.Segments) == 0 || len(chunk.Segments[0].Fragments) == 0 {
				return fmt.Errorf("no segments or fragments in chunk")
			}
			seg := chunk.Segments[0]
			moof := seg.Fragments[0].Moof
			rd, ok := ch.trDatas[trName]
			if !ok {
				slog.Error("Failed to find track data", "trName", trName, "chName", stream.chName)
			}
			trex := rd.init.Moov.Mvex.Trex
			*defaultDur = trex.DefaultSampleDuration
			tfhd := moof.Traf.Tfhd
			if tfhd.DefaultSampleDuration != 0 {
				*defaultDur = tfhd.DefaultSampleDuration
			}

			if trd.chunkNr == 0 {
				// Create new file path based on sequence number
				trd.seqNr = moof.Mfhd.SequenceNumber
				if ch.startNr != 0 {
					newNr := int(trd.seqNr) - ch.startNr
					if newNr < 0 {
						slog.Error("Sequence number less than startNr", "seqNr", trd.seqNr, "startNr", ch.startNr)
					}
					trd.seqNr = uint32(newNr)
				}
				newSegPath := filepath.Join(stream.trDir, fmt.Sprintf("%d%s", trd.seqNr, stream.ext))
				ofh, err = os.Create(newSegPath)
				if err != nil {
					return fmt.Errorf("failed to create file: %w", err)
				}
				trd.dts = moof.Traf.Tfdt.BaseMediaDecodeTime()

				if styp := chunk.Segments[0].Styp; styp != nil {
					for _, brand := range styp.CompatibleBrands() {
						switch brand {
						case "lmsg": // Last segment of a live stream
							trd.isLmsg = true
						case "slat": // According to DASH-IF CMAF Ingest spec Section 6.2
							trd.isSlate = true
						default:
							// Not interesting
						}
					}
				}
				if ch.maxNrBufSegs > 0 {
					deleteSegPath := filepath.Join(stream.trDir, fmt.Sprintf("%d%s", int(trd.seqNr)-ch.maxNrBufSegs, stream.ext))
					if fileExists(deleteSegPath) {
						slog.Debug("Deleting old segment", "path", deleteSegPath)
						err = os.Remove(deleteSegPath)
						slog.Warn("Failed to delete old segment", "path", deleteSegPath, "err", err)
					}
				}
			}
			dur := uint32(moof.Traf.Trun.Duration(*defaultDur))
			ch.addChunkData(*trd)
			trd.chunkNr++
			trd.totDur += dur
		}
		n, err := ofh.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write chunk: %w", err)
		}
		if n != len(cd.Data) {
			return fmt.Errorf("failed to write all chunk bytes %d of %d", n, len(cd.Data))
		}
		trd.totSize += uint32(n)
		return nil
	}

	slog.Info("Receiving file", "url", path, "contentLength", contentLength, "totSize", trd.totSize)
	var buf []byte
	if contentLength > 0 {
		buf = make([]byte, contentLength)
	} else {
		buf = make([]byte, 1024)
	}
	p := chunkparser.NewMP4ChunkParser(req.Body, buf, chunkParserCallback)
	err = p.Parse()
	if ofh != nil {
		defer ofh.Close()
	}
	if err != nil {
		slog.Error("Failed to parse MP4 chunk", "err", err)
		http.Error(w, "Failed to parse MP4 chunk", http.StatusInternalServerError)
		return
	}
	if contentLength > 0 && trd.totSize != uint32(contentLength) {
		slog.Error("Failed to receive all bytes", "nrBytesReceived", trd.totSize, "contentLength", contentLength)
	}
	if trd.chunkNr > 0 {
		trd.isComplete = true
		ch.addChunkData(*trd)
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
