// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
)

type liveMPDType int

const (
	MAX_TIME_SHIFT_BUFFER_DEPTH_S = 48 * 3600
)

const (
	timeLineTime liveMPDType = iota
	timeLineNumber
	segmentNumber
)

type UTCTimingMethod string

const (
	UtcTimingDirect     UTCTimingMethod = "direct"
	UtcTimingHead       UTCTimingMethod = "head"
	UtcTimingNtp        UTCTimingMethod = "ntp"
	UtcTimingSntp       UTCTimingMethod = "sntp"
	UtcTimingHttpXSDate UTCTimingMethod = "httpxsdate"
	UtcTimingHttpISO    UTCTimingMethod = "httpiso"
	UtcTimingNone       UTCTimingMethod = "none"
)

const (
	UtcTimingNtpServer    = "1.de.pool.ntp.org"
	UtcTimingSntpServer   = "time.kfki.hu"
	UtcTimingHttpServer   = "https://time.akamai.com/?iso"
	UtcTimingHttpServerMS = "https://time.akamai.com/?isoms"
	UtcTimingHeadAsset    = "/static/time.txt"
)

type ResponseConfig struct {
	URLParts                     []string          `json:"-"`
	URLContentIdx                int               `json:"-"`
	BaseURLs                     []string          `json:"BaseURLs,omitempty"`
	UTCTimingMethods             []UTCTimingMethod `json:"UTCTimingMethods,omitempty"`
	PeriodDurations              []int             `json:"PeriodDurations,omitempty"`
	StartTimeS                   int               `json:"StartTimeS"`
	StopTimeS                    *int              `json:"StopTimeS,omitempty"`
	TimeOffsetS                  *float64          `json:"TimeOffsetS,omitempty"`
	InitSegAvailOffsetS          *int              `json:"InitSegAvailOffsetS,omitempty"`
	TimeShiftBufferDepthS        *int              `json:"TimeShiftBufferDepthS,omitempty"`
	MinimumUpdatePeriodS         *int              `json:"MinimumUpdatePeriodS,omitempty"`
	PeriodsPerHour               *int              `json:"PeriodsPerHour,omitempty"`
	XlinkPeriodsPerHour          *int              `json:"XlinkPeriodsPerHour,omitempty"`
	EtpPeriodsPerHour            *int              `json:"EtpPeriodsPerHour,omitempty"`
	EtpDuration                  *int              `json:"EtpDuration,omitempty"`
	PeriodOffset                 *int              `json:"PeriodOffset,omitempty"`
	SCTE35PerMinute              *int              `json:"SCTE35PerMinute,omitempty"`
	StartNr                      *int              `json:"StartNr,omitempty"`
	SuggestedPresentationDelayS  *int              `json:"SuggestedPresentationDelayS,omitempty"`
	AvailabilityTimeOffsetS      float64           `json:"AvailabilityTimeOffsetS,omitempty"`
	ChunkDurS                    *float64          `json:"ChunkDurS,omitempty"`
	LatencyTargetMS              *int              `json:"LatencyTargetMS,omitempty"`
	AddLocationFlag              bool              `json:"AddLocationFlag,omitempty"`
	Tfdt32Flag                   bool              `json:"Tfdt32Flag,omitempty"`
	ContUpdateFlag               bool              `json:"ContUpdateFlag,omitempty"`
	InsertAdFlag                 bool              `json:"InsertAdFlag,omitempty"`
	ContMultiPeriodFlag          bool              `json:"ContMultiPeriodFlag,omitempty"`
	SegTimelineFlag              bool              `json:"SegTimelineFlag,omitempty"`
	SegTimelineNrFlag            bool              `json:"SegTimelineNrFlag,omitempty"`
	SidxFlag                     bool              `json:"SidxFlag,omitempty"`
	SegTimelineLossFlag          bool              `json:"SegTimelineLossFlag,omitempty"`
	AvailabilityTimeCompleteFlag bool              `json:"AvailabilityTimeCompleteFlag,omitempty"`
	TimeSubsStpp                 []string          `json:"TimeSubsStppLanguages,omitempty"`
	TimeSubsWvtt                 []string          `json:"TimeSubsWvttLanguages,omitempty"`
	TimeSubsDurMS                int               `json:"TimeSubsDurMS,omitempty"`
	TimeSubsRegion               int               `json:"TimeSubsRegion,omitempty"`
	Host                         string            `json:"Host,omitempty"`
	SegStatusCodes               []SegStatusCodes  `json:"SegStatus,omitempty"`
}

// SegStatusCodes configures regular extraordinary segment response codes
type SegStatusCodes struct {
	// Cycle is cycle length in seconds
	Cycle int
	// Rsq is relative sequence number (in cycle)
	Rsq int
	// Code is the HTTP response code
	Code int
	// Reps is a list of applicable representations (empty means all)
	Reps []string
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

// getRepType returns the live representation type depending on MPD and segment name (type).
// Normally follows the MPD type, but for image (thumbnails), always returns segmentNumber.
func (rc *ResponseConfig) getRepType(segName string) liveMPDType {
	if isImage(segName) {
		return segmentNumber
	}
	return rc.liveMPDType()
}

// getAvailabilityTimeOffset returns the availabilityTimeOffsetS. Note that it can be infinite.
func (rc *ResponseConfig) getAvailabilityTimeOffsetS() float64 {
	return rc.AvailabilityTimeOffsetS
}

// getStartNr for MPD. Default value if not set is 1.
func (rc *ResponseConfig) getStartNr() int {
	// Default startNr is 1 according to spec, but can be overridden by actual value set in cfg.
	if rc.StartNr != nil {
		return *rc.StartNr
	}
	return 1
}

// processURLCfg returns all information that can be extracted from url
func processURLCfg(confURL string, nowMS int) (*ResponseConfig, error) {
	// Mimics configprocessor.process_url

	cfgURL, err := url.QueryUnescape(confURL)
	if err != nil {
		return nil, fmt.Errorf("url.QueryUnescape: %w", err)
	}
	urlParts := strings.Split(cfgURL, "/")
	cfg := NewResponseConfig()
	cfg.URLParts = urlParts
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
			cfg.StartTimeS = sc.Atoi(key, val) + ms2S(nowMS)
			cfg.AddLocationFlag = true
		case "stoprel":
			cfg.StopTimeS = sc.AtoiPtr(key, val)
			*cfg.StopTimeS += ms2S(nowMS)
			cfg.AddLocationFlag = true
		case "dur": // Adds a presentation duration for multiple periods
			cfg.PeriodDurations = append(cfg.PeriodDurations, sc.Atoi(key, val))
		case "timeoffset": //Time offset in seconds version NTP
			cfg.TimeOffsetS = sc.Atof(key, val)
		case "init": // Make the init segment available earlier
			cfg.InitSegAvailOffsetS = sc.AtoiPtr(key, val)
		case "tsbd": // Timeshift Buffer Depth
			cfg.TimeShiftBufferDepthS = sc.AtoiPtr(key, val)
		case "mup": //minimum update period (in s)
			cfg.MinimumUpdatePeriodS = sc.AtoiPtr(key, val)
		case "modulo": // Make a number of time-limited sessions every hour
			return nil, fmt.Errorf("option %q not implemented", key)
		case "tfdt": // Use 32-bit tfdt (which means that AST must be more recent as well)
			cfg.Tfdt32Flag = true
		case "cont": // Continuous update of MPD AST and segNr
			cfg.ContUpdateFlag = true
		case "periods": // Make n periods per hour
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
		case "scte35": // Signal this many SCTE-35 ad periods inband (emsg messages) every minute
			cfg.SCTE35PerMinute = sc.AtoiPtr(key, val)
		case "utc": // Get hyphen-separated list of utc-timing methods and make into list
			cfg.UTCTimingMethods = sc.SplitUTCTimings(key, val)
		case "snr": // Segment startNumber. -1 means default implicit number which ==  1
			cfg.StartNr = sc.AtoiPtr(key, val)
		case "ato": // availabilityTimeOffset
			cfg.AvailabilityTimeOffsetS = sc.AtofInf(key, val)
		case "ltgt": // latencyTargetMS
			cfg.LatencyTargetMS = sc.AtoiPtr(key, val)
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
		case "timesubswvtt": // comma-separated list of languages
			cfg.TimeSubsWvtt = strings.Split(val, ",")
		case "timesubsdur": // duration in milliseconds
			cfg.TimeSubsDurMS = sc.Atoi(key, val)
		case "timesubsreg": // region (0 or 1)
			cfg.TimeSubsRegion = sc.Atoi(key, val)
		case "statuscode":
			cfg.SegStatusCodes = sc.ParseSegStatusCodes(key, val)
		default:
			contentStartIdx = i
			break cfgLoop
		}
	}
	if sc.err != nil {
		return nil, sc.err
	}
	if contentStartIdx == -1 {
		return nil, fmt.Errorf("no content part")
	}

	err = verifyAndFillConfig(cfg, nowMS)
	if err != nil {
		return cfg, fmt.Errorf("url config: %w", err)
	}
	cfg.URLContentIdx = contentStartIdx
	return cfg, nil
}

func verifyAndFillConfig(cfg *ResponseConfig, nowMS int) error {
	if cfg.SegTimelineNrFlag && cfg.SegTimelineFlag {
		return fmt.Errorf("SegmentTimelineTime and SegmentTimelineNr cannot be used at same time")
	}
	if cfg.TimeSubsRegion < 0 || cfg.TimeSubsRegion > 1 {
		return fmt.Errorf("timesubsreg number must be 0 or 1")
	}
	if cfg.MinimumUpdatePeriodS != nil && *cfg.MinimumUpdatePeriodS <= 0 {
		return fmt.Errorf("minimumUpdatePeriod must be > 0")
	}
	if cfg.getAvailabilityTimeOffsetS() > 0 && cfg.LatencyTargetMS == nil {
		cfg.LatencyTargetMS = Ptr(defaultLatencyTargetMS)
	}
	if cfg.TimeShiftBufferDepthS != nil {
		tsbd := *cfg.TimeShiftBufferDepthS
		if tsbd < 0 || tsbd > MAX_TIME_SHIFT_BUFFER_DEPTH_S {
			return fmt.Errorf("timeShiftBufferDepth %ds is not less than %ds", tsbd, MAX_TIME_SHIFT_BUFFER_DEPTH_S)
		}
	}
	if cfg.ContMultiPeriodFlag && cfg.PeriodsPerHour == nil {
		return fmt.Errorf("period continuity set, but not multiple periods per hour")
	}
	if cfg.SCTE35PerMinute != nil {
		err := scte35.IsValidSCTE35Interval(*cfg.SCTE35PerMinute)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *ResponseConfig) URLContentPart() string {
	return strings.Join(c.URLParts[c.URLContentIdx:], "/")
}

// fullHost uses non-empty cfgHost or extracts from requests scheme://host from request.
func fullHost(cfgHost string, r *http.Request) string {
	if cfgHost != "" {
		return cfgHost
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

// SetHost sets scheme://host to non-trivial cfgValue or tries to detect from request.
func (c *ResponseConfig) SetHost(cfgValue string, r *http.Request) {
	c.Host = fullHost(cfgValue, r)
}

func ms2S(ms int) int {
	return int(math.Round(float64(ms) * 0.001))
}
