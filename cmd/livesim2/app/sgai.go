// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	m "github.com/Eyevinn/dash-mpd/mpd"
)

// Server-Guided Ad Insertion (SGAI) using DASH Ed.6 Alternative-MPD Replace events.
//
// A live stream is annotated with an EventStream of scheme
// AlternativeMPDReplaceScheme. Each Event carries a ReplacePresentation that
// points (via @uri) at an ad-decisioning endpoint. A player resolves that URI
// between earliestResolutionTime and presentationTime, switches to the returned
// (List) MPD for the break, and then resumes the live presentation.
const (
	// AlternativeMPDReplaceScheme is the Ed.6 Alternative-MPD Replacement event scheme.
	AlternativeMPDReplaceScheme = "urn:mpeg:dash:event:alternativeMPD:replace:2025"
	// UrlParam2025SchemeIdUri marks usage of the Ed.6 Annex I URL parameters (2025).
	UrlParam2025SchemeIdUri = "urn:mpeg:dash:urlparam:2025"
	// sgaiEventTimescale is the EventStream timescale. 90 kHz matches the MPEG-2 system /
	// SCTE-35 clock that ad-break signaling typically originates from (and allows
	// frame-accurate timing).
	sgaiEventTimescale = uint32(90000)

	defaultSGAIAdEndpoint     = "/sgai/ads"
	defaultSGAIResolveOffsetS = 60
)

// SGAIBreak is a single ad break expressed relative to the availabilityStartTime.
type SGAIBreak struct {
	// OffsetS is the break presentation time in seconds from the period (AST) start.
	OffsetS int `json:"OffsetS"`
	// DurationS is the active window and the maximum ad-pod duration in seconds.
	DurationS int `json:"DurationS"`
}

// SGAIPeriodic describes recurring ad breaks: a break of DurationS starts at every
// wall-clock multiple of PeriodS since the epoch (e.g. PeriodS=60 means every start of a
// minute, UTC). The anchoring is wall-clock, not availabilityStartTime, so all sessions
// share the same break schedule and a viewer may join in the middle of a break.
type SGAIPeriodic struct {
	PeriodS   int `json:"PeriodS"`
	DurationS int `json:"DurationS"`
}

// SGAIConfig configures Alternative-MPD Replace ad breaks for a live stream.
type SGAIConfig struct {
	Breaks         []SGAIBreak   `json:"Breaks,omitempty"`   // fixed breaks (offset from AST)
	Periodic       *SGAIPeriodic `json:"Periodic,omitempty"` // recurring breaks (mutually exclusive with Breaks)
	AdEndpoint     string        `json:"AdEndpoint"`         // path to the ad-decisioning endpoint
	ResolveOffsetS int           `json:"ResolveOffsetS"`     // earliestResolutionTimeOffset (seconds)
	SkipAfterS     *int          `json:"SkipAfterS,omitempty"`
	NoJump         int32         `json:"NoJump,omitempty"`
	Clip           bool          `json:"Clip"`
	ExecuteOnce    bool          `json:"ExecuteOnce"`
}

// CreateSGAIConfig parses the value of an "sgai" URL option.
//
// Grammar: ( <off>:<dur>[,<off>:<dur>...] | p<period>:<dur> )[;key=val;...]
// keys: skipafter=<s>, nojump=<0|1|2>, clip=<0|1>, once=<0|1>, resolve=<s>, ep=<path>
//
// Examples: 30:15;skipafter=5;nojump=2  => one 15s break 30s in, skippable after 5s,
// not skippable by seeking, latest such event wins.
// p60:20 => a 20s break at every start of a (UTC) minute, recurring forever.
func CreateSGAIConfig(val string) (*SGAIConfig, error) {
	if val == "" {
		return nil, fmt.Errorf("empty sgai config")
	}
	if hasExtraSpaces(val) {
		return nil, fmt.Errorf("sgai config %q has extra spaces", val)
	}
	cfg := &SGAIConfig{
		AdEndpoint:     defaultSGAIAdEndpoint,
		ResolveOffsetS: defaultSGAIResolveOffsetS,
		Clip:           true,
		ExecuteOnce:    true,
	}
	parts := strings.Split(val, ";")
	if spec, ok := strings.CutPrefix(parts[0], "p"); ok {
		// Periodic: p<period>:<dur> — a break of <dur> at every wall-clock multiple of <period>.
		if strings.Contains(spec, ",") {
			return nil, fmt.Errorf("sgai periodic %q cannot be combined with more breaks", parts[0])
		}
		per, dur, ok := strings.Cut(spec, ":")
		if !ok {
			return nil, fmt.Errorf("sgai periodic %q must be p<period>:<dur>", parts[0])
		}
		perS, err := strconv.Atoi(per)
		if err != nil || perS <= 0 {
			return nil, fmt.Errorf("sgai periodic %q: bad period", parts[0])
		}
		durS, err := strconv.Atoi(dur)
		if err != nil || durS <= 0 {
			return nil, fmt.Errorf("sgai periodic %q: bad duration", parts[0])
		}
		if durS >= perS {
			return nil, fmt.Errorf("sgai periodic %q: duration must be less than the period", parts[0])
		}
		cfg.Periodic = &SGAIPeriodic{PeriodS: perS, DurationS: durS}
	} else {
		for bs := range strings.SplitSeq(parts[0], ",") {
			off, dur, ok := strings.Cut(bs, ":")
			if !ok {
				return nil, fmt.Errorf("sgai break %q must be <off>:<dur>", bs)
			}
			offS, err := strconv.Atoi(off)
			if err != nil || offS < 0 {
				return nil, fmt.Errorf("sgai break %q: bad offset", bs)
			}
			durS, err := strconv.Atoi(dur)
			if err != nil || durS <= 0 {
				return nil, fmt.Errorf("sgai break %q: bad duration", bs)
			}
			cfg.Breaks = append(cfg.Breaks, SGAIBreak{OffsetS: offS, DurationS: durS})
		}
	}
	for _, kv := range parts[1:] {
		key, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("sgai param %q must be key=val", kv)
		}
		switch key {
		case "skipafter":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("sgai skipafter %q: must be >= 0", v)
			}
			cfg.SkipAfterS = &n
		case "nojump":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 2 {
				return nil, fmt.Errorf("sgai nojump %q: must be 0, 1 or 2", v)
			}
			cfg.NoJump = int32(n)
		case "clip":
			switch v {
			case "1", "true":
				cfg.Clip = true
			case "0", "false":
				cfg.Clip = false
			default:
				return nil, fmt.Errorf("sgai clip %q: must be 0 or 1", v)
			}
		case "once":
			cfg.ExecuteOnce = v == "1" || v == "true"
		case "resolve":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("sgai resolve %q: must be >= 0", v)
			}
			cfg.ResolveOffsetS = n
		case "ep":
			if !isCleanAbsPath(v) {
				return nil, fmt.Errorf("sgai ep %q: must be a clean absolute path (e.g. /sgai/ads)", v)
			}
			cfg.AdEndpoint = v
		default:
			return nil, fmt.Errorf("unknown sgai param %q", key)
		}
	}
	if len(cfg.Breaks) == 0 && cfg.Periodic == nil {
		return nil, fmt.Errorf("sgai config %q has no breaks", val)
	}
	return cfg, nil
}

// isCleanAbsPath reports whether p is a safe absolute path (no scheme, authority, userinfo
// or backslashes), so that AdEndpoint cannot change the authority of the @uri when spliced
// after scheme://host (e.g. "@evil.com" or "//evil.com" would redirect the ad request).
func isCleanAbsPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	if strings.ContainsAny(p, "@\\") {
		return false
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return false
	}
	return true
}

// ParseSGAIConfig parses an sgai option value, accumulating any error on the converter.
func (s *strConvAccErr) ParseSGAIConfig(key, val string) *SGAIConfig {
	if s.err != nil {
		return nil
	}
	cfg, err := CreateSGAIConfig(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return cfg
}

// sgaiAdURI builds the absolute ReplacePresentation@uri pointing at the ad-decisioning endpoint.
// The client appends Annex I parameters (session id via useMPDUrlQuery and the execution-delta).
func sgaiAdURI(cfg *ResponseConfig, id uint64, durS int) string {
	return fmt.Sprintf("%s%s?break=%d&dur=%d", cfg.Host, cfg.SGAI.AdEndpoint, id, durS)
}

// sgaiBreakInst is one concrete break occurrence to signal in the MPD.
type sgaiBreakInst struct {
	id      uint64
	offsetS int64 // break start in seconds relative to the availabilityStartTime
	durS    int
}

// breakInstances returns the break occurrences to signal at wall-clock time nowMS (ms since
// epoch) for a stream with availabilityStartTime astS (s since epoch). Fixed breaks are all
// signaled, unchanged across refreshes. For a periodic config the occurrences start at every
// wall-clock multiple of PeriodS since the epoch (e.g. every start of a minute for p60) and
// the list is windowed: breaks that have already ended are dropped (one in progress is kept,
// so a late joiner lands mid-ad) and the look-ahead is one resolve offset plus one period.
// The id is the occurrence number since the epoch, so it is stable across refreshes and
// unique per break (it keys the @uri ?break= and the execution-delta state).
func (c *SGAIConfig) breakInstances(nowMS int, astS int) []sgaiBreakInst {
	if c.Periodic == nil {
		out := make([]sgaiBreakInst, 0, len(c.Breaks))
		for i, b := range c.Breaks {
			out = append(out, sgaiBreakInst{id: uint64(i + 1), offsetS: int64(b.OffsetS), durS: b.DurationS})
		}
		return out
	}
	p, d := c.Periodic.PeriodS, c.Periodic.DurationS
	nowS := nowMS / 1000
	horizonS := nowS + c.ResolveOffsetS + p
	k := max((nowS-d)/p, 0) // candidate for the earliest occurrence that may still be in progress
	var out []sgaiBreakInst
	for t := k * p; t <= horizonS; t += p {
		if t+d <= nowS || t < astS {
			continue // already ended, or before the availability start
		}
		out = append(out, sgaiBreakInst{id: uint64(t/p) + 1, offsetS: int64(t - astS), durS: d})
	}
	return out
}

// addSGAIReplaceEvents injects an Alternative-MPD Replace EventStream (one Event per break
// occurrence at nowMS) into the first Period, the sibling Annex I RequestParam, and the
// MPD-level urlparam:2025 marker.
//
// Each Event@presentationTime is an absolute offset from the availabilityStartTime, so it is
// stable across MPD refreshes. With a stream started at availabilityStartTime≈now (e.g.
// startrel_0) the break lands a fixed number of seconds after the viewer joins. Periodic
// configs instead anchor the breaks to the wall clock (see breakInstances).
func addSGAIReplaceEvents(mpd *m.MPD, period *m.Period, cfg *ResponseConfig, nowMS int) {
	if cfg.SGAI == nil {
		return
	}
	insts := cfg.SGAI.breakInstances(nowMS, cfg.StartTimeS)
	if len(insts) == 0 {
		return
	}
	ts := sgaiEventTimescale
	es := &m.EventStreamType{
		SchemeIdUri: AlternativeMPDReplaceScheme,
		Timescale:   m.Ptr(ts),
	}
	for _, b := range insts {
		inner := &m.AlternativeMPDEventType{
			Uri:                          sgaiAdURI(cfg, b.id, b.durS),
			EarliestResolutionTimeOffset: m.FloatInf64(float64(cfg.SGAI.ResolveOffsetS) * float64(ts)),
			MaxDuration:                  uint64(b.durS) * uint64(ts),
			ExecuteOnce:                  cfg.SGAI.ExecuteOnce,
			NoJump:                       cfg.SGAI.NoJump,
		}
		if cfg.SGAI.SkipAfterS != nil {
			inner.SkipAfter = m.Duration(time.Duration(*cfg.SGAI.SkipAfterS) * time.Second)
		}
		es.Events = append(es.Events, &m.EventType{
			PresentationTime: uint64(b.offsetS) * uint64(ts),
			Duration:         uint64(b.durS) * uint64(ts),
			Id:               m.Ptr(b.id),
			ReplacePresentation: &m.AlternativeMPDReplaceEventType{
				Clip:                    m.Ptr(cfg.SGAI.Clip),
				AlternativeMPDEventType: inner,
			},
		})
	}
	// Annex I: carry the main MPD URL query (e.g. the session id) onto the altmpd request
	// and add the execution-delta state of the first signaled break.
	es.RequestParam = []*m.ExtendedUrlInfoType{{
		UrlQueryInfoType: m.UrlQueryInfoType{
			// prta first so the well-formed param is always present even when the MPD URL
			// carries no query ($querypart$ then expands to empty rather than leaving a "&&").
			QueryTemplate:  fmt.Sprintf("prta=$urn:mpeg:dash:state:execution-delta#%d$&$querypart$", insts[0].id),
			UseMPDUrlQuery: true,
		},
		IncludeInRequests: "altmpd",
	}}
	period.EventStreams = append(period.EventStreams, es)

	// MPD-level marker that Annex I (2025) URL parameters are used.
	mpd.EssentialProperties = append(mpd.EssentialProperties,
		m.NewDescriptor(UrlParam2025SchemeIdUri, "", ""))
}
