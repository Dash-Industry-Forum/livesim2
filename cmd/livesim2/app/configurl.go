// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"strings"
)

type liveMPDType int

const (
	timeLineTime liveMPDType = iota
	timeLineNumber
	segmentNumber
)

type ResponseConfig struct {
	BaseURLs                     []string `json:"BaseURLs,omitempty"`
	UTCTimingMethods             []string `json:"UTCTimingMethods,omitempty"`
	PeriodDurations              []int    `json:"PeriodDurations,omitempty"`
	StartTimeS                   int      `json:"StartTimeS"`
	StopTimeS                    *int     `json:"StopTimeS,omitempty"`
	TimeOffsetS                  *int     `json:"TimeOffsetS,omitempty"`
	InitSegAvailOffsetS          *int     `json:"InitSegAvailOffsetS,omitempty"`
	TimeShiftBufferDepthS        *int     `json:"TimeShiftBufferDepthS,omitempty"`
	MinimumUpdatePeriodS         *int     `json:"MinimumUpdatePeriodS,omitempty"`
	PeriodsPerHour               *int     `json:"PeriodsPerHour,omitempty"`
	XlinkPeriodsPerHour          *int     `json:"XlinkPeriodsPerHour,omitempty"`
	EtpPeriodsPerHour            *int     `json:"EtpPeriodsPerHour,omitempty"`
	EtpDuration                  *int     `json:"EtpDuration,omitempty"`
	PeriodOffset                 *int     `json:"PeriodOffset,omitempty"`
	SCTE35PerMinute              *int     `json:"SCTE35PerMinute,omitempty"`
	StartNr                      *int     `json:"StartNr,omitempty"`
	SuggestedPresentationDelayS  *int     `json:"SuggestedPresentationDelayS,omitempty"`
	AvailabilityTimeOffsetS      *float64 `json:"AvailabilityTimeOffsetS,omitempty"`
	ChunkDurS                    *float64 `json:"ChunkDurS,omitempty"`
	AddLocationFlag              bool     `json:"AddLocationFlag,omitempty"`
	Tfdt32Flag                   bool     `json:"Tfdt32Flag,omitempty"`
	ContUpdateFlag               bool     `json:"ContUpdateFlag,omitempty"`
	InsertAdFlag                 bool     `json:"InsertAdFlag,omitempty"`
	ContMultiPeriodFlag          bool     `json:"ContMultiPeriodFlag,omitempty"`
	SegTimelineFlag              bool     `json:"SegTimelineFlag,omitempty"`
	SegTimelineNrFlag            bool     `json:"SegTimelineNrFlag,omitempty"`
	SidxFlag                     bool     `json:"SidxFlag,omitempty"`
	SegTimelineLossFlag          bool     `json:"SegTimelineLossFlag,omitempty"`
	AvailabilityTimeCompleteFlag bool     `json:"AvailabilityTimeCompleteFlag,omitempty"`
	TimeSubsStpp                 []string `json:"TimeSubsStppLanguages,omitempty"`
	TimeSubsDurMS                int      `json:"TimeSubsDurMS,omitempty"`
}

// NewResponseConfig returns a new ResponseConfig with default values.
func NewResponseConfig() *ResponseConfig {
	c := ResponseConfig{
		StartTimeS:                   defaultAvailabilityStartTimeS,
		AvailabilityTimeCompleteFlag: defaultAvailabilityTimeComplete,
		TimeShiftBufferDepthS:        Ptr(defaultTimeShiftBufferDepthS),
		StartNr:                      Ptr(defaultStartNr),
		TimeSubsDurMS:                defaultTimeSubsDurMS,
	}
	return &c
}

func (rc *ResponseConfig) liveMPDType() liveMPDType {
	switch {
	case rc.SegTimelineFlag:
		return timeLineTime
	case rc.SegTimelineNrFlag:
		return timeLineNumber
	default:
		return segmentNumber
	}
}

// processURLCfg returns all information that can be extracted from the urlParts
func processURLCfg(urlParts []string, nowS int) (cfg *ResponseConfig, cntStart int, err error) {
	// Mimics configprocessor.procss_url
	cfg = NewResponseConfig()
	sc := strConvAccErr{}
	contentStartIdx := -1
	skipStart := 2
cfgLoop:
	for i, part := range urlParts {
		if i < skipStart {
			continue // Skip "" and "livesim"
		}
		key, val, ok := strings.Cut(part, "_")
		if !ok {
			contentStartIdx = i
			break cfgLoop
		}
		switch key {
		case "start", "ast":
			cfg.StartTimeS = sc.Atoi(key, val)
		case "stop":
			cfg.StopTimeS = sc.AtoiPtr(key, val)
		case "startrel":
			cfg.StartTimeS = sc.Atoi(key, val) + nowS
			cfg.AddLocationFlag = true
		case "stoprel":
			cfg.StopTimeS = sc.AtoiPtr(key, val)
			*cfg.StopTimeS += nowS
			cfg.AddLocationFlag = true
		case "dur": // Adds a presentation duration for multiple periods
			cfg.PeriodDurations = append(cfg.PeriodDurations, sc.Atoi(key, val))
		case "timeoffset": //Time offset in seconds version NTP
			cfg.TimeOffsetS = sc.AtoiPtr(key, val)
		case "init": // Make the init segment available earlier
			cfg.InitSegAvailOffsetS = sc.AtoiPtr(key, val)
		case "tsbd": // Timeshift Buffer Depth
			cfg.TimeShiftBufferDepthS = sc.AtoiPtr(key, val)
		case "mup": //minimum update period (in s)
			cfg.MinimumUpdatePeriodS = sc.AtoiPtr(key, val)
		case "modulo": // Make a number of time-limited sessions every hour
			return nil, 0, fmt.Errorf("option %q not implemented", key)
		case "tfdt": // Use 32-bit tfdt (which means that AST must be more recent as well)
			cfg.Tfdt32Flag = true
		case "cont": // Continuous update of MPD AST and segNr
			cfg.ContUpdateFlag = true
		case "periods": // Make multiple periods
			cfg.PeriodsPerHour = sc.AtoiPtr(key, val)
		case "xlink": // Make periods access via xlink
			cfg.XlinkPeriodsPerHour = sc.AtoiPtr(key, val)
		case "etp": // Early terminated periods per hour
			cfg.EtpPeriodsPerHour = sc.AtoiPtr(key, val)
		case "etpDuration": // Add a presentation duration for multiple periods
			cfg.EtpDuration = sc.AtoiPtr(key, val)
		case "insertad": // insert an ad via xlink
			cfg.InsertAdFlag = true
		case "continuous": // Only valid when periods_per_hour is set
			cfg.ContMultiPeriodFlag = true
		case "segtimeline":
			cfg.SegTimelineFlag = true
		case "segtimelinenr":
			cfg.SegTimelineNrFlag = true
		case "baseurl": // Add one or more BaseURLs, put all configurations
			cfg.BaseURLs = append(cfg.BaseURLs, val)
		case "peroff": // Set the period offset
			cfg.PeriodOffset = sc.AtoiPtr(key, val)
		case "scte35": // Add this many SCTE-35 ad periods every minute
			cfg.SCTE35PerMinute = sc.AtoiPtr(key, val)
		case "utc": // Get hyphen-separated list of utc-timing methods and make into list
			cfg.UTCTimingMethods = strings.Split(val, "-")
		case "snr": // Segment startNumber. -1 means default implicit number which ==  1
			cfg.StartNr = sc.AtoiPtr(key, val)
		case "ato": // availabilityTimeOffset
			if val == "inf" {
				inf := -1.0
				cfg.AvailabilityTimeOffsetS = &inf // Signals that the value is infinite
			} else {
				cfg.AvailabilityTimeOffsetS = sc.AtofPosPtr(key, val)
			}
		case "spd": // suggestedPresentationDelay
			cfg.SuggestedPresentationDelayS = sc.AtoiPtr(key, val)
		case "sidx": // Insert sidx in each segment
			cfg.SidxFlag = true
		case "segtimelineloss": // Segment timeline loss case
			cfg.SegTimelineLossFlag = true
		case "chunkdur": // chunk duration in seconds
			cfg.ChunkDurS = sc.AtofPosPtr(key, val)
			cfg.AvailabilityTimeCompleteFlag = false
		case "timesubsstpp": // comma-separated list of languages
			cfg.TimeSubsStpp = strings.Split(val, ",")
		case "timesubsdur": // duration in milliseconds
			cfg.TimeSubsDurMS = sc.Atoi(key, val)
		default:
			contentStartIdx = i
			break cfgLoop
		}
	}
	if sc.err != nil {
		return nil, 0, sc.err
	}
	if contentStartIdx == -1 {
		return nil, 0, fmt.Errorf("no content part")
	}

	err = verifyConfig(cfg)
	if err != nil {
		return cfg, contentStartIdx, fmt.Errorf("url config: %w", err)
	}
	return cfg, contentStartIdx, nil
}

func verifyConfig(cfg *ResponseConfig) error {
	if cfg.SegTimelineNrFlag {
		return fmt.Errorf("mpd type SegmentTimeline with Number not yet supported")
	}
	if len(cfg.TimeSubsStpp) > 0 && cfg.SegTimelineFlag {
		return fmt.Errorf("combination of SegTimeline and generated stpp subtitles not yet supported")
	}
	return nil
}
