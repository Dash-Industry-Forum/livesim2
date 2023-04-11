// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/rs/zerolog/log"
)

type Options struct {
	AssetURL   string
	OutDir     string
	LogFile    string
	LogFormat  string
	LogLevel   string
	MaxTimeS   int
	Version    bool
	Force      bool
	AutoOutDir bool
}

func Fetch(o *Options) error {
	ctx, cancel := context.WithCancel(context.Background())
	if o.MaxTimeS > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(o.MaxTimeS)*time.Second)
	}
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		cancel()
	}()
	err := createDirIfNotExists(o.OutDir)
	if err != nil {
		return fmt.Errorf("createDir: %w", err)
	}
	cnt, err := start(ctx, o)
	log.Info().Int("nrFiles", cnt.total()).Int("nrExisting", cnt.nrExisting).
		Int("nrDownloaded", cnt.nrDownloaded).Int("nrErrors", cnt.nrErrors).Msg("download results")
	return err
}

func createDirIfNotExists(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return true
}

type counts struct {
	nrDownloaded int
	nrExisting   int
	nrErrors     int
}

func (c counts) total() int {
	return c.nrDownloaded + c.nrExisting + c.nrErrors
}

func start(ctx context.Context, o *Options) (counts, error) {
	cnt := counts{}
	mpdURL := o.AssetURL
	outDir := o.OutDir
	parts := strings.Split(mpdURL, "/")
	mpdName := parts[len(parts)-1]
	cnt, err := downloadMPD(ctx, mpdURL, outDir, mpdName, cnt, o.Force)
	if err != nil {
		return cnt, err
	}
	mpdPath := path.Join(outDir, mpdName)
	mpd, err := m.ReadFromFile(mpdPath)
	if err != nil {
		return cnt, fmt.Errorf("read mpd: %w", err)
	}
	if mpd.Type != nil && *mpd.Type == "dynamic" { // TODO. Replace with mpd.GetType()
		return cnt, fmt.Errorf("dynamic MPD not supported")
	}
	baseURL := getBase(mpdURL)
	for _, period := range mpd.Periods {
		for _, as := range period.AdaptationSets {
			segTmpl := as.SegmentTemplate
			for _, rep := range as.Representations {
				if rep.SegmentTemplate != nil {
					segTmpl = rep.SegmentTemplate
				}
				if segTmpl == nil {
					return cnt, fmt.Errorf("no SegmentTemplate for representation: %s", rep.Id)
				}
				initStr, _ := rep.GetInit()
				cnt = downloadInit(ctx, segTmpl, outDir, baseURL, initStr, cnt, o.Force)
				media, _ := rep.GetMedia()
				switch {
				case segTmpl.SegmentTimeline != nil:
					stl := segTmpl.SegmentTimeline
					switch {
					case strings.Contains(media, "$Time$"):
						cnt = downloadSegmentTimeLineWithTime(ctx, stl, media, outDir, baseURL, cnt, o.Force)
					case strings.Contains(media, "$Number$"):
						log.Warn().Msg("SegmentTimeline with $Number$ not yet supported")
						// downloadSegmentTimeLineWithNumber
					default:
						return cnt, fmt.Errorf("strange media for SegmentTimeline")
					}
				case strings.Contains(segTmpl.Media, "$Number$"):
					periodDur, err := period.GetDuration()
					if err != nil {
						return cnt, fmt.Errorf("period duration issue: %w", err)
					}
					totDurMS := uint32(periodDur / 1_000_000)
					cnt = downloadSegmentNumber(ctx, segTmpl, totDurMS, media, outDir, baseURL, cnt, o.Force)
				default:
					return cnt, fmt.Errorf("unsupported representation: %s", rep.Id)
				}
			}
		}
	}
	return cnt, nil
}

func downloadMPD(ctx context.Context, mpdURL, outDir, mpdName string, cnt counts, force bool) (counts, error) {
	outPath := path.Join(outDir, mpdName)
	if fileExists(outPath) && !force {
		log.Info().Str("path", outPath).Str("url", mpdURL).Msg("file already exists. Skipping")
		cnt.nrExisting++
	} else {
		err := downloadToFile(ctx, mpdURL, outPath)
		if err != nil {
			cnt.nrErrors++
			return cnt, fmt.Errorf("download %s: %w", mpdURL, err)
		}
		err = internal.WriteMPDData(outDir, mpdName, mpdURL)
		if err != nil {
			log.Warn().Err(err).Msg("could not write mdlist file")
		}
	}
	return cnt, nil
}

func downloadInit(ctx context.Context, segTmpl *m.SegmentTemplateType, outDir, baseURL, initStr string, cnt counts, force bool) counts {
	u := baseURL + initStr
	p := path.Join(outDir, initStr)
	cnt, err := downloadAndCount(ctx, u, p, cnt, force)
	if err != nil {
		log.Warn().Err(err).Msg("download init segment")
	}
	return cnt
}

func downloadSegmentTimeLineWithTime(ctx context.Context, stl *m.SegmentTimelineType, mediaPattern, outDir, baseURL string, cnt counts, force bool) counts {
	startTime := uint64(0)
	var err error
	for _, segItvl := range stl.S {
		if segItvl.T != nil {
			startTime = *segItvl.T
		}
		mPart := replaceTime(mediaPattern, startTime)
		u := baseURL + mPart
		p := path.Join(outDir, mPart)
		cnt, err = downloadAndCount(ctx, u, p, cnt, force)
		if err != nil {
			log.Warn().Err(err).Msg("download file")
		}
		dur := segItvl.D
		startTime += dur
		for i := 0; i < segItvl.R; i++ {
			mPart := replaceTime(mediaPattern, startTime)
			u := baseURL + mPart
			p := path.Join(outDir, mPart)
			cnt, err = downloadAndCount(ctx, u, p, cnt, force)
			if err != nil {
				log.Warn().Err(err).Msg("download file")
			}
			startTime += dur
		}
	}
	return cnt
}

func downloadSegmentNumber(ctx context.Context, stpl *m.SegmentTemplateType, totDurMS uint32, mediaPattern, outDir, baseURL string, cnt counts, force bool) counts {
	startNr := uint32(1)
	if stpl.StartNumber != nil {
		startNr = *stpl.StartNumber
	}
	if stpl == nil {
		log.Warn().Msg("segment duration not set")
		return cnt
	}
	dur := *stpl.Duration
	timeScale := uint32(1)
	if stpl.Timescale != nil {
		timeScale = *stpl.Timescale
	}
	var err error
	nrSegments := totDurMS * timeScale / (dur * 1000)
	for i := startNr; i <= nrSegments+1; i++ { // Try one more to avoid rounding problems
		mPart := replaceNumber(mediaPattern, i)
		u := baseURL + mPart
		p := path.Join(outDir, mPart)
		cnt, err = downloadAndCount(ctx, u, p, cnt, force)
		if err != nil && i < nrSegments {
			log.Warn().Err(err).Msg("download file")
		}
	}
	return cnt
}

func downloadAndCount(ctx context.Context, url, outPath string, cnt counts, force bool) (counts, error) {
	if fileExists(outPath) && !force {
		cnt.nrExisting++
		log.Info().Str("path", outPath).Str("url", url).Msg("file already exists. Skipping")
	} else {
		err := downloadToFile(ctx, url, outPath)
		if err != nil {
			cnt.nrErrors++
			return cnt, fmt.Errorf("problem downloading %s: %w", url, err)
		} else {
			cnt.nrDownloaded++
		}
	}
	return cnt, nil
}

func getBase(u string) string {
	idx := strings.LastIndex(u, "/")
	if idx == -1 {
		return ""
	}
	return u[:idx+1]
}

func replaceTime(media string, time uint64) string {
	return strings.Replace(media, "$Time$", strconv.Itoa(int(time)), 1)
}

func replaceNumber(media string, nr uint32) string {
	return strings.Replace(media, "$Number$", strconv.Itoa(int(nr)), 1)
}

// downloadToFile downloads content directly into a file given by outPath
func downloadToFile(ctx context.Context, url, outPath string) error {
	client := http.DefaultClient
	if fileExists(outPath) {
		log.Info().Str("path", outPath).Msg("file exists")
		return nil
	}
	log.Info().Str("url", url).Str("path", outPath).Msg("Downloading")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("could not read %s. Code %d", url, resp.StatusCode)
	}

	dir := getBase(outPath)
	err = createDirIfNotExists(dir)
	if err != nil {
		return err
	}

	ofh, err := os.Create(outPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(ofh, resp.Body)
	if err != nil {
		return err
	}
	log.Debug().Str("path", outPath).Msg("stored")
	return nil
}

// AutoDir adds part of MPD URL to outDir, trying to remove matching parts.
func AutoDir(rawMPDurl, outDir string) (string, error) {
	u, err := url.Parse(rawMPDurl)
	if err != nil {
		return "", err
	}

	uParts := strings.Split(u.Path, "/")
	uBaseParts := uParts[1 : len(uParts)-1]
	outParts := strings.Split(outDir, "/")

	// Move uBaseParts to the left and find match as far to the left as possible
	maxOutEnd := len(outParts) - 1
	minOutEnd := max(1, maxOutEnd-len(uBaseParts)+1)
	bestOutEnd := -1
	for outStart := maxOutEnd; outStart >= minOutEnd; outStart-- {
		match := true
		outRange := maxOutEnd + 1 - outStart
		if outRange > len(uBaseParts) {
			break
		}
		for i := 0; i < outRange; i++ {
			if outParts[outStart+i] != uBaseParts[i] {
				match = false
				break
			}
		}
		if match {
			bestOutEnd = outStart
		}
	}
	if bestOutEnd >= 0 {
		outParts = outParts[:bestOutEnd]
	}
	outPath := path.Join(strings.Join(outParts, "/"), strings.Join(uBaseParts, "/"))
	return outPath, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
