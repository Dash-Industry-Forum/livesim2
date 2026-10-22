// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"net/url"
	"slices"
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
	// sgaiPrerollID is the Event@id (and @uri ?break=) of the preroll. The regular breaks
	// are numbered from 1 (fixed: 1-based index, periodic: occurrence number + 1), so 0 is free.
	sgaiPrerollID = uint64(0)
)

// SGAIConfig configures Alternative-MPD Replace ad breaks for a live stream.
type SGAIConfig struct {
	AdBreaks              // fixed breaks (offset from AST) or a periodic recurrence
	AdEndpoint     string `json:"AdEndpoint"`     // path to the ad-decisioning endpoint
	ResolveOffsetS int    `json:"ResolveOffsetS"` // earliestResolutionTimeOffset (seconds)
	SkipAfterS     *int   `json:"SkipAfterS,omitempty"`
	NoJump         int32  `json:"NoJump,omitempty"`
	Clip           bool   `json:"Clip"`
	ExecuteOnce    bool   `json:"ExecuteOnce"`
	SVTA           bool   `json:"SVTA,omitempty"` // add SVTA2053 signaling to the List MPD
	// PrerollS is the duration of a preroll break signaled at presentationTime 0 (0 = none).
	PrerollS int `json:"PrerollS,omitempty"`
}

// CreateSGAIConfig parses the value of an "sgai" URL option.
//
// Grammar: ( <off>:<dur>[,<off>:<dur>...] | p<period>:<dur> )[;key=val;...]
// keys: skipafter=<s>, nojump=<0|1|2>, clip=<0|1>, once=<0|1>, resolve=<s>, ep=<path>,
// svta=<0|1>, preroll=<dur>
//
// Examples: 30:15;skipafter=5;nojump=2  => one 15s break 30s in, skippable after 5s,
// not skippable by seeking, latest such event wins.
// p60:20 => a 20s break at every start of a (UTC) minute, recurring forever.
// p60:20;preroll=10 => as above, plus a 10s preroll break at presentationTime 0.
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
	ab, err := parseAdBreaks("sgai", parts[0])
	if err != nil {
		return nil, err
	}
	cfg.AdBreaks = ab
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
		case "svta":
			// Also describe each ad of the returned pod with SVTA2053 ad-creative signaling.
			switch v {
			case "1", "true":
				cfg.SVTA = true
			case "0", "false":
				cfg.SVTA = false
			default:
				return nil, fmt.Errorf("sgai svta %q: must be 0 or 1", v)
			}
		case "resolve":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("sgai resolve %q: must be >= 0", v)
			}
			cfg.ResolveOffsetS = n
		case "preroll":
			n, err := parseAdBreakSeconds(v)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("sgai preroll %q: must be > 0", v)
			}
			cfg.PrerollS = n
		case "ep":
			if !isCleanAbsPath(v) {
				return nil, fmt.Errorf("sgai ep %q: must be a clean absolute path (e.g. /sgai/ads)", v)
			}
			cfg.AdEndpoint = v
		default:
			return nil, fmt.Errorf("unknown sgai param %q", key)
		}
	}
	if cfg.empty() {
		return nil, fmt.Errorf("sgai config %q has no breaks", val)
	}
	if cfg.PrerollS > 0 && len(cfg.Breaks) > 0 && cfg.Breaks[0].OffsetS < cfg.PrerollS {
		return nil, fmt.Errorf("sgai config %q: first break overlaps the preroll", val)
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
	uri := fmt.Sprintf("%s%s?break=%d&dur=%d", cfg.Host, cfg.SGAI.AdEndpoint, id, durS)
	if cfg.SGAI.SVTA {
		// The ad-decisioning endpoint is stateless about the stream configuration, so the
		// request for SVTA2053 signaling in the List MPD rides on the URI.
		uri += "&svta=1"
	}
	return uri
}

// addSGAIReplaceEvents injects an Alternative-MPD Replace EventStream (one Event per break
// occurrence at nowMS) into the first Period, the sibling Annex I RequestParam, and the
// MPD-level urlparam:2025 marker.
//
// Each Event@presentationTime is an absolute offset from the availabilityStartTime, so it is
// stable across MPD refreshes. With a stream started at availabilityStartTime≈now (e.g.
// startrel_0) the break lands a fixed number of seconds after the viewer joins. Periodic
// configs instead anchor the breaks to the wall clock (see breakInstances).
//
// With a preroll configured, an extra Event (id 0) is signaled at presentationTime 0, i.e. at
// the period start. Players should treat an alternative-MPD event at time 0 as a
// preroll and execute it when playback starts, wherever in the live timeline that is.
func addSGAIReplaceEvents(mpd *m.MPD, period *m.Period, cfg *ResponseConfig, nowMS int) {
	if cfg.SGAI == nil {
		return
	}
	insts := cfg.SGAI.instances(nowMS, cfg.StartTimeS, cfg.SGAI.ResolveOffsetS, 0)
	if pre := cfg.SGAI.PrerollS; pre > 0 {
		// A periodic occurrence can land inside the preroll when the availabilityStartTime
		// is aligned with the cycle; two Replace events must not cover the same time.
		insts = slices.DeleteFunc(insts, func(b adBreakInst) bool { return b.offsetS < int64(pre) })
	}
	if len(insts) == 0 && cfg.SGAI.PrerollS == 0 {
		return
	}
	ts := sgaiEventTimescale
	es := &m.EventStreamType{
		SchemeIdUri: AlternativeMPDReplaceScheme,
		Timescale:   new(ts),
	}
	all := insts
	if cfg.SGAI.PrerollS > 0 {
		all = append([]adBreakInst{{id: sgaiPrerollID, offsetS: 0, durS: cfg.SGAI.PrerollS}}, insts...)
	}
	for _, b := range all {
		inner := &m.AlternativeMPDEventType{
			Uri:                          sgaiAdURI(cfg, b.id, b.durS),
			EarliestResolutionTimeOffset: new(uint64(cfg.SGAI.ResolveOffsetS) * uint64(ts)),
			MaxDuration:                  uint64(b.durS) * uint64(ts),
			ExecuteOnce:                  cfg.SGAI.ExecuteOnce,
			NoJump:                       cfg.SGAI.NoJump,
		}
		if cfg.SGAI.SkipAfterS != nil {
			inner.SkipAfter = m.Duration(time.Duration(*cfg.SGAI.SkipAfterS) * time.Second)
		}
		es.Events = append(es.Events, &m.EventType{
			PresentationTime: uint64(b.offsetS) * uint64(ts),
			Duration:         new(uint64(b.durS) * uint64(ts)),
			Id:               new(b.id),
			ReplacePresentation: &m.AlternativeMPDReplaceEventType{
				Clip:                    new(cfg.SGAI.Clip),
				AlternativeMPDEventType: inner,
			},
		})
	}
	// Annex I: carry the main MPD URL query (e.g. the session id) onto the altmpd request
	// and add the execution-delta state of the first signaled regular break (the preroll
	// only when there is none).
	prtaID := all[0].id
	if len(insts) > 0 {
		prtaID = insts[0].id
	}
	es.RequestParam = []*m.ExtendedUrlInfoType{{
		// prta first so the well-formed param is always present even when the MPD URL
		// carries no query ($querypart$ then expands to empty rather than leaving a "&&").
		QueryTemplate:     fmt.Sprintf("prta=$urn:mpeg:dash:state:execution-delta#%d$&$querypart$", prtaID),
		UseMPDUrlQuery:    true,
		IncludeInRequests: "altmpd",
	}}
	period.EventStreams = append(period.EventStreams, es)

	// MPD-level marker that Annex I (2025) URL parameters are used.
	mpd.EssentialProperties = append(mpd.EssentialProperties,
		m.NewDescriptor(UrlParam2025SchemeIdUri, "", ""))
}
