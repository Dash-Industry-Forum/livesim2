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

	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/dash-mpd/mpd"
)

type errorWithHttpType struct {
	msg        string
	statusCode int
}

func (e errorWithHttpType) Error() string {
	return e.msg
}

func generateAndLogHttpError(log *slog.Logger, msg string, statusCode int) *errorWithHttpType {
	log.Error(msg)
	return &errorWithHttpType{msg, statusCode}
}

func cfgFromRequest(r *http.Request, log *slog.Logger) (nowMS int, cfg *ResponseConfig, errHT *errorWithHttpType) {
	uPath := r.URL.Path
	u, err := url.Parse(uPath)
	if err != nil {
		return 0, nil, generateAndLogHttpError(log, "URL parsing", http.StatusInternalServerError)
	}

	q := r.URL.Query()
	nowMS, err = getNowMS(q.Get("nowMS"))
	if err != nil {
		return 0, nil, generateAndLogHttpError(log, "bad nowMS query", http.StatusBadRequest)
	}

	nowDate := q.Get("nowDate")
	if nowDate != "" {
		nowMS, err = getMSFromDate(nowDate)
		if err != nil {
			return 0, nil, generateAndLogHttpError(log, "bad nowDate query", http.StatusBadRequest)
		}
	}

	publishTime := q.Get("publishTime")
	if publishTime != "" {
		nowMS, err = getMSFromDate(publishTime)
		if err != nil {
			return 0, nil, generateAndLogHttpError(log, "bad publishTime query", http.StatusBadRequest)
		}
	}

	cfg, err = processURLCfg(u.String(), nowMS)
	if err != nil {
		msg := fmt.Sprintf("processURL error: %q", err)
		return 0, nil, generateAndLogHttpError(log, msg, http.StatusBadRequest)
	}

	if cfg.TimeOffsetS != nil {
		offsetMS := int(*cfg.TimeOffsetS * 1000)
		nowMS += offsetMS
	}

	if nowMS < cfg.StartTimeS*1000 {
		tooEarlyMS := cfg.StartTimeS - nowMS
		msg := fmt.Sprintf("%dms too early", tooEarlyMS)
		return 0, nil, generateAndLogHttpError(log, msg, http.StatusTooEarly)
	}

	return nowMS, cfg, nil
}

// livesimHandlerFunc handles mpd and segment requests.
// ?nowMS=... can be used to set the current time for testing.
func (s *Server) livesimHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	nowMS, cfg, errHT := cfgFromRequest(r, log)
	if errHT != nil {
		http.Error(w, errHT.Error(), errHT.statusCode)
		return
	}

	contentPart := cfg.URLContentPart()
	log.Debug("requested content", "url", contentPart)
	a, ok := s.assetMgr.findAsset(contentPart)
	if !ok {
		msg := fmt.Sprintf("unknown asset %q", contentPart)
		log.Error(msg)
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	cfg.SetHost(s.Cfg.Host, r)
	switch filepath.Ext(r.URL.Path) {
	case ".mpd":
		if !checkQuery(cfg.Query, r.URL) {
			if !checkQuery(cfg.Query, r.URL) {
				log.Error("query check mismatch", "cfg", cfg.Query.raw, "url", r.URL.RawQuery)
				http.Error(w, "query check mismatch ", http.StatusBadRequest)
				return
			}
		}
		_, mpdName := path.Split(contentPart)
		err := writeLiveMPD(log, w, cfg, s.Cfg.DrmCfg, a, mpdName, nowMS)
		if err != nil {
			log.Error("liveMPD", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case ".mp4", ".m4s", ".cmfv", ".cmfa", ".cmft", ".jpg", ".jpeg", ".m4v", ".m4a":
		segmentPart := strings.TrimPrefix(contentPart, a.AssetPath) // includes heading slash
		if len(cfg.Traffic) > 0 {
			var patternNr int
			patternNr, segmentPart = extractPattern(segmentPart)
			if patternNr >= 0 {
				itvls := cfg.Traffic[patternNr]
				switch itvls.StateAt(nowMS / 1000) {
				case lossNo:
					// Just continue
				case loss404:
					http.Error(w, "Not Found", http.StatusNotFound)
					return
				case lossSlow:
					time.Sleep(lossSlowTime)
				case lossHang:
					// Get the result, but after 10s
					time.Sleep(lossHangTime)
					http.Error(w, "Hang", http.StatusServiceUnavailable)
					return
				default:
					http.Error(w, "strange loss state", http.StatusInternalServerError)
					return
				}
			}
		}
		if cfg.Query != nil && contentTypeFromURL(cfg, a, segmentPart[1:]) == "video" {
			if !checkQuery(cfg.Query, r.URL) {
				log.Error("query check mismatch", "cfg", cfg.Query.raw, "url", r.URL.RawQuery)
				http.Error(w, "query check mismatch ", http.StatusBadRequest)
				return
			}
		}
		code, err := writeSegment(r.Context(), w, log, cfg, s.Cfg.DrmCfg, s.assetMgr.vodFS, a, segmentPart[1:],
			nowMS, s.textTemplates, false /*isLast */)
		if err != nil {
			log.Error("writeSegment", "code", code, "err", err)
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
				http.Error(w, "writeSegment", http.StatusInternalServerError)
				return
			}
		}
		if code != 0 {
			log.Debug("special return code", "code", code)
			http.Error(w, "triggered code", code)
			return
		}
	default:
		http.Error(w, "unknown file extension", http.StatusNotFound)
		return
	}
}

func checkQuery(cfgQuery *Query, u *url.URL) bool {
	if cfgQuery == nil {
		return true
	}
	uq := u.Query()
	for key, val := range cfgQuery.parts {
		uVal, ok := uq[key]
		if !ok {
			return false
		}
		if len(val) != len(uVal) {
			return false
		}
		for i := range val {
			if val[i] != uVal[i] {
				return false
			}
		}
	}
	return true
}

// contentTypeFromURL returns the content type of the segment.
// Should work for both init and media segments.
func contentTypeFromURL(cfg *ResponseConfig, a *asset, segmentPart string) string {
	// First check imit for time subs
	_, _, ok, err := matchTimeSubsInitLang(cfg, segmentPart)
	if ok && err == nil {
		return "subtitle"
	}
	// Next match against init segments
	for _, rep := range a.Reps {
		if segmentPart == rep.InitURI {
			return rep.ContentType
		}
	}
	// Finally match against media segments
	rep, _, err := findRepAndSegmentID(a, segmentPart)
	if err != nil {
		return "unknown"
	}
	return rep.ContentType
}

// getNowMS returns value from query or local clock.
func getNowMS(nowMSValue string) (nowMS int, err error) {
	if nowMSValue != "" {
		return strconv.Atoi(nowMSValue)
	}
	return int(time.Now().UnixMilli()), nil
}

// getMSFromDate returns a nowMS value based on date (+1ms).
// The extra millisecond is there to ensure that the corresponding manifest
// can be generated
func getMSFromDate(publishTimeValue string) (nowMS int, err error) {
	t, err := time.Parse(time.RFC3339, publishTimeValue)
	if err != nil {
		return -1, err
	}
	return int(t.UnixMilli()) + 1, nil
}

// extractPattern extracts the pattern number and return a modified segmentPart.
func extractPattern(segmentPart string) (int, string) {
	parts := strings.Split(segmentPart, "/")
	pPart := parts[1]
	if !strings.HasPrefix(pPart, baseURLPrefix) {
		return -1, segmentPart
	}
	nr, err := strconv.Atoi(pPart[len(baseURLPrefix):])
	if err != nil {
		return -1, segmentPart
	}
	// Remove the base URL part, but leave an empty string at start.
	parts = parts[1:]
	parts[0] = ""
	return nr, strings.Join(parts, "/")
}

func writeLiveMPD(log *slog.Logger, w http.ResponseWriter, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	a *asset, mpdName string, nowMS int) error {
	work := make([]byte, 0, 1024)
	buf := bytes.NewBuffer(work)
	lMPD, err := LiveMPD(a, mpdName, cfg, drmCfg, nowMS)
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

// writeSegment writes a segment to the response writer, but may also return a special status code if configured.
func writeSegment(ctx context.Context, w http.ResponseWriter, log *slog.Logger, cfg *ResponseConfig, drmCfg *drm.DrmConfig,
	vodFS fs.FS, a *asset, segmentPart string, nowMS int, tt *template.Template, isLast bool) (code int, err error) {
	// First check if init segment and return
	log.Debug("writeSegment", "segmentPart", segmentPart)
	isInitSegment, err := writeInitSegment(log, w, cfg, drmCfg, a, segmentPart)
	if err != nil {
		return 0, fmt.Errorf("writeInitSegment: %w", err)
	}
	if isInitSegment {
		return 0, nil
	}
	if len(cfg.SegStatusCodes) > 0 {
		code, err = calcStatusCode(cfg, a, segmentPart, nowMS)
		if err != nil {
			return 0, err
		}
		if code != 0 {
			return code, nil
		}
	}
	if cfg.AvailabilityTimeCompleteFlag {
		return 0, writeLiveSegment(log, w, cfg, drmCfg, vodFS, a, segmentPart, nowMS, tt, isLast)
	}
	// Chunked low-latency mode
	return 0, writeChunkedSegment(ctx, log, w, cfg, drmCfg, vodFS, a, segmentPart, nowMS, isLast)
}

// calcStatusCode returns the configured status code for the segment or 0 if none.
func calcStatusCode(cfg *ResponseConfig, a *asset, segmentPart string, nowMS int) (int, error) {
	rep, _, err := findRepAndSegmentID(a, segmentPart)
	if err != nil {
		return 0, fmt.Errorf("findRepAndSegmentID: %w", err)
	}

	// segMeta is to be used for all look up. For audio it uses reference (video) track
	segMeta, err := findSegMeta(a, cfg, segmentPart, nowMS)
	if err != nil {
		return 0, fmt.Errorf("findSegMeta: %w", err)
	}
	startTime := int(segMeta.newTime)
	repTimescale := int(segMeta.timescale)
	for _, ss := range cfg.SegStatusCodes {
		if !repInReps(rep.ID, ss.Reps) {
			continue
		}
		// Then move to the reference track and relate to cycles
		// From segment number we calculate a start time
		// The time gives us how many cycles we have passed (time / cycleDuration)
		cycle := ss.Cycle
		cycleInTimescale := cycle * repTimescale
		nrWraps := startTime / cycleInTimescale
		wrapStartS := nrWraps * cycle
		// Next we need to find the number after wrap
		// For that we need to find the first segment nr after wrapStart
		// Use nowMS = cycleStart to look up the latest segment published at that time
		firstNr := 0
		if nrWraps > 0 {
			lastNr := findLastSegNr(cfg, a, wrapStartS*1000, segMeta.rep)
			firstNr = lastNr + 1
		}
		segTime := findSegStartTime(a, cfg, firstNr, segMeta.rep)
		if segTime < wrapStartS*repTimescale {
			firstNr += 1
		}
		idx := int(segMeta.newNr) - firstNr
		if idx < 0 {
			return 0, fmt.Errorf("segment %d is before first segment %d", segMeta.newNr, firstNr)
		}
		if idx == ss.Rsq {
			return ss.Code, nil
		}
	}
	return 0, nil
}

func findLastSegNr(cfg *ResponseConfig, a *asset, nowMS int, rep *RepData) int {
	wTimes := calcWrapTimes(a, cfg, nowMS, mpd.Duration(60*time.Second))
	timeLineEntries := a.generateTimelineEntries(rep.ID, wTimes, 0)
	return timeLineEntries.lastNr()
}

func findSegStartTime(a *asset, cfg *ResponseConfig, nr int, rep *RepData) int {
	wrapLen := len(rep.Segments)
	startNr := cfg.getStartNr()
	nrAfterStart := int(nr) - startNr
	nrWraps := nrAfterStart / wrapLen
	relNr := nrAfterStart - nrWraps*wrapLen
	wrapDur := a.LoopDurMS * rep.MediaTimescale / 1000
	wrapTime := nrWraps * wrapDur
	seg := rep.Segments[relNr]
	return wrapTime + int(seg.StartTime)
}

func repInReps(segmentPart string, reps []string) bool {
	// TODO. Make better
	if len(reps) == 0 {
		return true
	}
	for _, rep := range reps {
		if strings.Contains(segmentPart, rep) {
			return true
		}
	}
	return false
}
