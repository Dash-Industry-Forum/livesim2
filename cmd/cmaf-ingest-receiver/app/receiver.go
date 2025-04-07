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
		channelMgr: NewChannelMgr(cfg, uint32(opts.timeShiftBufferDepthS), uint32(opts.receiveNrRawSegments)),
	}
	return r, nil
}

// SegmentHandlerFunc is a handler for receiving segments, but will also accept MPDs (extension .mpd).
func (r *Receiver) SegmentHandlerFunc(w http.ResponseWriter, req *http.Request) {
	// Extract the path and filename from URL
	// Drop the first part that should be /upload or similar as specified by prefix.
	path := strings.TrimPrefix(req.URL.Path, r.prefix)
	slog.Debug("Trimmed path", "path", path)
	if chName, ok := matchMPD(path); ok {
		handleMPD(w, req, r.storage, chName)
		return
	}
	stream, ok := findStreamMatch(r.storage, path)
	if !ok {
		slog.Error("Failed to find valid stream", "path", path)
		http.Error(w, "Failed to find valid stream", http.StatusBadRequest)
		return
	}
	ch, ok := r.channelMgr.GetChannel(stream.chName)
	if !ok {
		r.channelMgr.AddChannel(r.ctx, stream.chName, stream.chDir)
		slog.Debug("Created new  channel", "name", stream.chName, "dir", stream.chDir)
		ch, _ = r.channelMgr.GetChannel(stream.chName)
	}
	if ch.ignore {
		slog.Debug("Dropping stream", "chName", stream.chName, "path", path)
		discardUpload(w, req, http.StatusOK)
		return
	}
	log := slog.Default().With(
		"chName", stream.chName,
		"trName", stream.trName,
	)
	if ch.authUser != "" || ch.authPswd != "" {
		user, pswd, ok := req.BasicAuth()
		if !ok || user != ch.authUser || pswd != ch.authPswd {
			log.Error("Unauthorized", "user", user, "chName", stream.chName)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	repCfg, ok := ch.repsCfg[stream.trName]
	if ok && repCfg.Ignore {
		log.Debug("Ignoring representation")
		discardUpload(w, req, http.StatusOK)
		return
	}
	if _, ok := r.streams[stream.id()]; !ok {
		log.Info("New stream", "urlPath", path, "streamId", stream.id(), "mediaType", stream.mediaType)
		r.streams[stream.id()] = stream
		err := os.MkdirAll(stream.trDir, 0755)
		if err != nil {
			log.Error("Failed to create directory", "err", err)
			http.Error(w, "Failed to create directory", http.StatusInternalServerError)
			return
		}
		err = findAndProcessOrigInitSegment(log, ch, stream)
		if err != nil {
			log.Error("Failed to find and process original init segment", "err", err)
		}
	}
	defer func() {
		log.Debug("Closing body", "url", path)
		err := req.Body.Close()
		if err != nil {
			log.Error("Failed to close request body", "err", err)
		}
	}()
	log.Debug("Headers", "path", path, "headers", req.Header)

	var contentLength int
	var err error
	if req.Header.Get("Content-Length") != "" {
		contentLength, err = strconv.Atoi(req.Header.Get("Content-Length"))
		if err != nil {
			log.Error("Failed to parse Content-Length", "err", err)
			http.Error(w, "Failed to parse Content-Length", http.StatusBadRequest)
			return
		}
	}

	var ofh *os.File
	var filePath string

	trName := stream.trName
	ch.mu.RLock()
	masterTimescale := ch.masterTimescale
	masterSegDur := ch.masterSegDuration
	masterTimeShift := ch.masterTimeShift
	masterSeqNrShift := ch.masterSeqNrShift
	ch.mu.RUnlock()

	rsd := &recSegData{name: stream.trName,
		shouldBeShifted: masterTimeShift != 0 || masterSeqNrShift != 0,
	}

	defaultDur := mpd.Ptr(uint32(0))

	chunkParserCallback := func(cd chunkparser.ChunkData) error {
		// Set ofh to the write file output and then write data
		data := cd.Data // Used so that you can overwrite cd.Data when needed
		if cd.IsInitSegment {
			filePath = filepath.Join(stream.trDir, fmt.Sprintf("%s%s", "init", stream.ext))
			err := os.MkdirAll(stream.trDir, 0755)
			if err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			log.Debug("Init segment received", "filePath", filePath)
			ofh, err = os.Create(filePath)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			data, err = processInitSegment(log, ch, stream, data, false /* isOrg */)
			if err != nil {
				return fmt.Errorf("failed to process init segment: %w", err)
			}
		} else {
			sr := bits.NewFixedSliceReader(cd.Data)
			chunk, err := mp4.DecodeFileSR(sr, mp4.WithDecodeFlags(mp4.DecFileFlags(mp4.DecModeLazyMdat)))
			if err != nil {
				return fmt.Errorf("failed to decode chunk %d: %w", rsd.chunkNr, err)
			}
			log.Debug("Media chunk received", "chunkNr", rsd.chunkNr, "chName", stream.chName, "trName", trName)

			if len(chunk.Segments) == 0 || len(chunk.Segments[0].Fragments) == 0 {
				return fmt.Errorf("no segments or fragments in chunk")
			}
			seg := chunk.Segments[0]
			moof := seg.Fragments[0].Moof
			trd, ok := ch.trDatas[trName]
			if !ok {
				return fmt.Errorf("failed to find track data trName: %s", trName)
			}
			trex := trd.init.Moov.Mvex.Trex
			*defaultDur = trex.DefaultSampleDuration
			tfhd := moof.Traf.Tfhd
			if tfhd.DefaultSampleDuration != 0 {
				*defaultDur = tfhd.DefaultSampleDuration
			}
			if trd.timeScaleOut != trd.timeScaleIn {
				*defaultDur = *defaultDur * trd.timeScaleOut / trd.timeScaleIn
			}

			if rsd.chunkNr == 0 {
				// Create new file path based on sequence number and startNr of channel
				// The outgoing sequence number should be
				// (baseMediaTime - startTime) / segmentDuration - startNr
				// when tuned in. At start, it should be
				// incomingSeqNr - startNr.
				rsd.seqNrIn = moof.Mfhd.SequenceNumber
				rsd.seqNr = rsd.seqNrIn - uint32(ch.startNr)
				inTime := moof.Traf.Tfdt.BaseMediaDecodeTime()
				t := int64(inTime)
				if rsd.shouldBeShifted {
					if masterTimeShift != 0 {
						if masterTimescale != trd.timeScaleIn {
							t = t * int64(masterTimescale) / int64(trd.timeScaleIn)
						}
						t += masterTimeShift
						rsd.isShifted = true
						t = t * int64(trd.timeScaleIn) / int64(masterTimescale)
					}
					segDur := int64(masterSegDur) * int64(trd.timeScaleIn) / int64(masterTimescale)
					rsd.seqNr = uint32((t+segDur/2)/segDur) - uint32(ch.startNr)
					if rsd.seqNr != rsd.seqNrIn {
						log.Debug("SeqNr change", "seqNrIn", rsd.seqNrIn, "seqNr", rsd.seqNr)
						rsd.isShifted = true
					}
				}
				if trd.timeScaleOut != trd.timeScaleIn {
					t = t * int64(trd.timeScaleOut) / int64(trd.timeScaleIn)
				}

				rsd.dts = uint64(t)
				if rsd.dts != inTime {
					log.Debug("Time change", "inTime", inTime, "outTime", rsd.dts, "seqNr", rsd.seqNr)
					moof.Traf.Tfdt.SetBaseMediaDecodeTime(rsd.dts)
				}
				filePath = filepath.Join(stream.trDir, fmt.Sprintf("%d%s", rsd.seqNr, stream.ext))
				ofh, err = os.Create(filePath)
				if err != nil {
					return fmt.Errorf("failed to create file: %w", err)
				}

				if styp := chunk.Segments[0].Styp; styp != nil {
					for _, brand := range styp.CompatibleBrands() {
						switch brand {
						case "lmsg": // Last segment of a live stream
							rsd.isLmsg = true
						case "slat": // According to DASH-IF CMAF Ingest spec Section 6.2
							rsd.isSlate = true
						default:
							// Not interesting
						}
					}
				}
				if ch.maxNrBufSegs > 0 {
					deleteSegPath := filepath.Join(stream.trDir, fmt.Sprintf("%d%s", rsd.seqNr-ch.maxNrBufSegs, stream.ext))
					if fileExists(deleteSegPath) {
						log.Debug("Deleting old segment", "path", deleteSegPath)
						err = os.Remove(deleteSegPath)
						if err != nil {
							log.Warn("Failed to delete old segment", "path", deleteSegPath, "err", err)
						}
					}
				}
			}
			//TODO. Add test cases for multiple-chunks rewrite
			if moof.Mfhd.SequenceNumber != rsd.seqNr {
				log.Debug("Sequence number changed", "oldSeqNr", rsd.seqNr, "newSeqNr", moof.Mfhd.SequenceNumber)
				moof.Mfhd.SequenceNumber = rsd.seqNr
			}
			if trd.timeScaleOut != trd.timeScaleIn && moof.Traf.Trun.HasSampleDuration() {
				for i := range moof.Traf.Trun.Samples {
					moof.Traf.Trun.Samples[i].Dur = moof.Traf.Trun.Samples[i].Dur * trd.timeScaleOut / trd.timeScaleIn
				}
			}
			dur := uint32(moof.Traf.Trun.Duration(*defaultDur))
			rsd.nrSamples = uint16(moof.Traf.Trun.SampleCount())
			ch.addChunkData(*rsd)
			log.Debug("Media chunk processed", "chunkNr", rsd.chunkNr, "dur", dur)
			rsd.chunkNr++
			rsd.totDur += dur
			if rsd.isShifted || trd.timeScaleIn != trd.timeScaleOut {
				sw := bits.NewFixedSliceWriter(int(seg.Size()))
				err = seg.EncodeSW(sw)
				if err != nil {
					return fmt.Errorf("failed to encode segment: %w", err)
				}
				data = sw.Bytes()
			}
		}
		n, err := ofh.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write chunk: %w", err)
		}
		log.Info("Wrote segment", "name", filepath.Base(ofh.Name()), "nrBytes", n)
		if n != len(data) {
			return fmt.Errorf("failed to write all chunk bytes %d of %d", n, len(data))
		}
		rsd.totSize += uint32(n)
		return nil
	}

	log.Debug("Receiving file", "url", path, "contentLength", contentLength, "totSize", rsd.totSize)
	var buf []byte
	if contentLength > 0 {
		buf = make([]byte, contentLength)
	} else {
		buf = make([]byte, 1024)
	}
	if ch.receiveNrRaws == 0 {
		p := chunkparser.NewMP4ChunkParser(req.Body, buf, chunkParserCallback)
		err = p.Parse()
		if ofh != nil {
			defer finalClose(ofh)
		}
		if err != nil {
			log.Error("Failed to parse MP4 chunk", "err", err)
			http.Error(w, "Failed to parse MP4 chunk", http.StatusInternalServerError)
			return
		}
		if contentLength > 0 && rsd.totSize != uint32(contentLength) {
			log.Error("Failed to receive all bytes", "nrBytesReceived", rsd.totSize, "contentLength", contentLength)
		}
		if rsd.chunkNr > 0 {
			rsd.isComplete = true
			rsd.dur = rsd.totDur
			ch.addChunkData(*rsd)
		}

		w.WriteHeader(http.StatusOK)
		return
	}
	// Receive raw segments
	nrRead := 0
	nrWritten := 0
	trD, ok := ch.trDatas[stream.trName]
	if !ok {
		log.Debug("New raw track data")
		trD = &trData{name: stream.trName}
		ch.trDatas[stream.trName] = trD
	}

	if trD.nrSegsReceived >= ch.receiveNrRaws && (contentLength == 0 || contentLength >= 4096) {
		log.Debug("Max number of raw segments received. Will not store.", "nrSegsReceived",
			trD.nrSegsReceived, "receiveNrRaws", ch.receiveNrRaws)
		err = req.Body.Close()
		if err != nil {
			log.Error("Failed to close request body", "err", err)
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	fileName := fmt.Sprintf("%s_%d%s", stream.trName, trD.nrSegsReceived, stream.ext)
	if contentLength != 0 && contentLength < 4096 {
		fileName = fmt.Sprintf("%s_init_%d%s", stream.trName, trD.nrSegsReceived, stream.ext)
	}
	filePath = filepath.Join(stream.trDir, fileName)
	log.Info("Receiving raw segment", "url", path, "filePath", filePath, "size", contentLength)
	ofh, err = os.Create(filePath)
	if err != nil {
		log.Error("Failed to create file", "err", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer finalClose(ofh)
	for {
		n, err := req.Body.Read(buf)
		if err != nil && err != io.EOF {
			log.Error("Failed to read request body", "err", err)
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		eof := err == io.EOF
		if n == 0 && eof {
			break
		}

		nrRead += n
		nOut, err := ofh.Write(buf[:n])
		if err != nil {
			log.Error("Failed to write file", "err", err)
			http.Error(w, "Failed to write file", http.StatusInternalServerError)
		}
		nrWritten += nOut
		if nOut != n {
			log.Error("Failed to write all bytes", "nOut", nOut, "n", n)
			http.Error(w, "Failed to write all bytes", http.StatusInternalServerError)
		}
		if eof {
			break
		}
	}
	trD.nrSegsReceived++
}

// DiscardUpload reads and discards the upload and returns the status code.
func discardUpload(w http.ResponseWriter, req *http.Request, statusCode int) {
	path := req.URL.Path
	slog.Debug("Discarding upload", "path", path)
	n, err := io.Copy(io.Discard, req.Body)
	if err != nil {
		slog.Warn("Failed to discard bytes", "path", path, "err", err)
	}
	slog.Debug("Discarded bytes", "path", path, "nrBytes", n)
	err = req.Body.Close()
	if err != nil {
		slog.Error("Failed to close request body", "path", path, "err", err)
	}
	w.WriteHeader(statusCode)
}

// DeleteHandlerFunc is a handler for deleting segments. Not used since fixed timeshiftBufferDepth.
func (r *Receiver) DeleteHandlerFunc(w http.ResponseWriter, req *http.Request) {
	slog.Debug("DeleteHandlerFunc called", "url", req.URL.Path)
}

func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return !os.IsNotExist(err)
}

func findAndProcessOrigInitSegment(log *slog.Logger, ch *channel, stream stream) error {
	path := filepath.Join(stream.trDir, fmt.Sprintf("%s%s", "init_org", stream.ext))
	if !fileExists(path) {
		return nil
	}
	log.Info("Init segment exists, loading it", "path", path)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read init segment: %w", err)
	}
	data, err = processInitSegment(log, ch, stream, data, false /* isOrg */)
	if err != nil {
		return fmt.Errorf("failed to process init segment: %w", err)
	}
	initPath := filepath.Join(stream.trDir, fmt.Sprintf("%s%s", "init", stream.ext))
	err = os.WriteFile(initPath, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write init segment: %w", err)
	}
	return nil
}

func processInitSegment(log *slog.Logger, ch *channel, s stream, data []byte, isOrg bool) ([]byte, error) {
	sr := bits.NewFixedSliceReader(data)
	// Write original init segment to init_org.ext
	if !isOrg {
		origFilePath := filepath.Join(s.trDir, fmt.Sprintf("%s%s", "init_org", s.ext))
		if fileExists(origFilePath) {
			log.Debug("Original init segment already exists", "path", origFilePath)
		}
		err := os.WriteFile(origFilePath, data, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to write original init segment: %w", err)
		}
	}
	iSeg, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode init segment: %w", err)
	}
	init := iSeg.Init
	err = ch.addInitDataAndUpdateTimescale(s, init)
	if err != nil {
		return nil, fmt.Errorf("failed to addInitData: %w", err)
	}
	sw := bits.NewFixedSliceWriter(int(init.Size()))
	err = init.EncodeSW(sw)
	if err != nil {
		return nil, fmt.Errorf("failed to encode wvtt init segment: %w", err)
	}
	return sw.Bytes(), nil
}

func handleMPD(w http.ResponseWriter, req *http.Request, storage, chName string) {
	err := os.MkdirAll(chName, 0755)
	if err != nil {
		slog.Error("Failed to create directory", "err", err)
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		return
	}
	receivedMpdPath := filepath.Join(storage, chName, "received.mpd")
	slog.Debug("Matched MPD", "chName", chName, "path", req.URL.Path, "outFile", receivedMpdPath)
	ofh, err := os.Create(receivedMpdPath)
	if err != nil {
		slog.Error("Failed to create file", "err", err)
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		return
	}
	defer finalClose(ofh)
	_, err = io.Copy(ofh, req.Body)
	if err != nil {
		slog.Error("Failed to write MPD", "err", err)
		http.Error(w, "Failed to write MPD", http.StatusInternalServerError)
		return
	}
	finalClose(req.Body)
	slog.Info("MPD received", "path", req.URL.Path, "storedPath", receivedMpdPath)
	w.WriteHeader(http.StatusOK)
}
