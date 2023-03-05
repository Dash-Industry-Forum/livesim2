package app

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/rs/zerolog/log"
)

const (
	SUBS_STPP_PREFIX    = "timestpp-"
	SUBS_STPP_INIT      = "init.mp4"
	SUBS_STPP_TIMESCALE = 1000
)

func stppSegmentParts(segmentPart string) (lang string, segment string, ok bool) {
	rep, seg, ok := strings.Cut(segmentPart, "/")
	if !ok {
		return "", "", false
	}
	_, lang, ok = strings.Cut(rep, "-")
	if !ok {
		return "", "", false
	}
	return lang, seg, true
}

func isTimeStppInitSegment(segmentPart string) (lang string, ok bool) {
	lang, seg, ok := stppSegmentParts(segmentPart)
	if !ok {
		return "", false
	}
	if seg == SUBS_STPP_INIT {
		return lang, true
	}
	return "", false
}

func writeTimeStppInitSegment(w http.ResponseWriter, cfg *ResponseConfig, a *asset, segmentPart string) (bool, error) {
	lang, ok := isTimeStppInitSegment(segmentPart)
	if !ok {
		return false, nil
	}
	matchingLang := false
	for _, mpdLang := range cfg.TimeSubsStpp {
		if mpdLang == lang {
			matchingLang = true
			break
		}
	}
	if !matchingLang {
		return true, fmt.Errorf("stpp language %q does not match config: %w", lang, errNotFound)
	}
	init := createSubtitlesStppInitSegment(lang, SUBS_STPP_TIMESCALE)
	w.Header().Set("Content-Type", "application/mp4")
	w.Header().Set("Content-Length", strconv.Itoa(int(init.Size())))
	err := init.Encode(w)
	if err != nil {
		log.Error().Err(err).Msg("write init response")
		return true, err
	}
	return true, nil
}

func createSubtitlesStppInitSegment(lang string, timescale uint32) *mp4.InitSegment {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(timescale, "stpp", lang)
	trak := init.Moov.Trak
	schemaLocation := ""
	auxiliaryMimeType := ""
	_ = trak.SetStppDescriptor("http://www.w3.org/ns/ttml", schemaLocation, auxiliaryMimeType)
	return init
}

// StppTimeData is iformation for creating an stpp media segment.
type StppTimeData struct {
	Lang string
	Cues []StppTimeCue
}

// StppTimeCue is cue information to put in template.
type StppTimeCue struct {
	Id    string
	Begin string
	End   string
	Msg   string
}
