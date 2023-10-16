// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// livesimHandlerFunc handles mpd and segment requests.
// ?nowMS=... can be used to set the current time for testing.
func (s *Server) livesimHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	uPath := r.URL.Path
	ext := filepath.Ext(uPath)
	u, err := url.Parse(uPath)
	if err != nil {
		msg := "URL parser"
		log.Error(msg)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	var nowMS int // Set from query string or from wall-clock
	q := r.URL.Query()
	nowMSValue := q.Get("nowMS")
	if nowMSValue != "" {
		nowMS, err = strconv.Atoi(nowMSValue)
		if err != nil {
			http.Error(w, "bad nowMS query", http.StatusBadRequest)
			return
		}
	} else {
		nowMS = int(time.Now().UnixMilli())
	}

	cfg, err := processURLCfg(u.String(), nowMS)
	if err != nil {
		msg := "processURL error"
		log.Error(msg, "err", err)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	cfg.SetHost(s.Cfg.Host, r)

	if cfg.TimeOffsetS != nil {
		offsetMS := int(*cfg.TimeOffsetS * 1000)
		nowMS += offsetMS
	}

	contentPart := cfg.URLContentPart()
	log.Debug("requested content", "url", contentPart)
	a, ok := s.assetMgr.findAsset(contentPart)
	if !ok {
		http.Error(w, "unknown asset", http.StatusNotFound)
		return
	}
	if nowMS < cfg.StartTimeS*1000 {
		tooEarlyMS := cfg.StartTimeS - nowMS
		http.Error(w, fmt.Sprintf("%dms too early", tooEarlyMS), http.StatusTooEarly)
		return
	}
	switch ext {
	case ".mpd":
		_, mpdName := path.Split(contentPart)
		cfg.SetHost(s.Cfg.Host, r)
		err := writeLiveMPD(log, w, cfg, a, mpdName, nowMS)
		if err != nil {
			// TODO. Add more granular errors like 404 not found
			msg := fmt.Sprintf("liveMPD: %s", err)
			log.Error(msg)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
	case ".mp4", ".m4s", ".cmfv", ".cmfa", ".cmft", ".jpg", ".jpeg", ".m4v", ".m4a":
		segmentPart := strings.TrimPrefix(contentPart, a.AssetPath) // includes heading /
		err = writeSegment(r.Context(), w, log, cfg, s.assetMgr.vodFS, a, segmentPart[1:], nowMS, s.textTemplates)
		if err != nil {
			var tooEarly errTooEarly
			switch {
			case errors.Is(err, errNotFound):
				http.Error(w, "Not Found", http.StatusNotFound)
				return
			case errors.As(err, &tooEarly):
				http.Error(w, tooEarly.Error(), http.StatusTooEarly)
			case errors.Is(err, errGone):
				http.Error(w, "Gone", http.StatusGone)
			default:
				msg := "writeSegment"
				log.Error(msg)
				http.Error(w, msg, http.StatusInternalServerError)
				return
			}
		}
	default:
		http.Error(w, "unknown file extension", http.StatusNotFound)
		return
	}
}

func writeLiveMPD(log *slog.Logger, w http.ResponseWriter, cfg *ResponseConfig, a *asset, mpdName string, nowMS int) error {
	work := make([]byte, 0, 1024)
	buf := bytes.NewBuffer(work)
	lMPD, err := LiveMPD(a, mpdName, cfg, nowMS)
	if err != nil {
		return fmt.Errorf("convertToLive: %w", err)
	}
	size, err := lMPD.Write(buf, "  ", true)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.Header().Set("Content-Type", "application/dash+xml")
	n, err := w.Write(buf.Bytes())
	if err != nil {
		log.Error("writing response")
		return err
	}
	if n != size {
		log.Error("could not write all bytes",
			"size", size,
			"nr written", n)
		return err
	}
	return nil
}

func writeSegment(ctx context.Context, w http.ResponseWriter, log *slog.Logger, cfg *ResponseConfig, vodFS fs.FS, a *asset,
	segmentPart string, nowMS int, tt *template.Template) error {
	// First check if init segment and return
	isInitSegment, err := writeInitSegment(w, cfg, vodFS, a, segmentPart)
	if err != nil {
		return fmt.Errorf("writeInitSegment: %w", err)
	}
	if isInitSegment {
		return nil
	}
	if cfg.AvailabilityTimeCompleteFlag {
		return writeLiveSegment(w, cfg, vodFS, a, segmentPart, nowMS, tt)
	}
	// Chunked low-latency mode
	return writeChunkedSegment(ctx, w, log, cfg, vodFS, a, segmentPart, nowMS)
}
