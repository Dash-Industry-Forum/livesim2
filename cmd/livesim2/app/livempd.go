// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"strings"

	m "github.com/Eyevinn/dash-mpd/mpd"
)

type wrapTimes struct {
	startWraps  int
	startWrapMS int
	startTimeMS int
	startRelMS  int
	nowMS       int
	nowWraps    int
	nowWrapMS   int
	nowRelMS    int
}

func calcWrapTimes(a *asset, cfg *ResponseConfig, nowMS int, tsbd m.Duration) wrapTimes {
	wt := wrapTimes{nowMS: nowMS}
	wt.startTimeMS = nowMS - int(tsbd)/1_000_000
	startTimeMS := cfg.StartTimeS * 1000
	if wt.startTimeMS < startTimeMS {
		wt.startTimeMS = startTimeMS
	}
	wt.startWraps = (wt.startTimeMS - cfg.StartTimeS*1000) / a.LoopDurMS
	wt.startWrapMS = wt.startWraps * a.LoopDurMS
	wt.startRelMS = wt.startTimeMS - wt.startWrapMS

	wt.nowWraps = (nowMS - cfg.StartTimeS*1000) / a.LoopDurMS
	wt.nowWrapMS = wt.nowWraps * a.LoopDurMS
	wt.nowRelMS = nowMS - wt.nowWrapMS

	return wt
}

// LiveMPD generates a dynamic configured MPD for a VoD asset.
func LiveMPD(a *asset, mpdName string, cfg *ResponseConfig, nowMS int) (*m.MPD, error) {
	mpd, err := a.getVodMPD(mpdName)
	if err != nil {
		return nil, err
	}
	mpd.Type = Ptr("dynamic")
	mpd.MediaPresentationDuration = nil
	mpd.AvailabilityStartTime = m.ConvertToDateTime(float64(cfg.StartTimeS))
	mpd.MinimumUpdatePeriod = Ptr(m.Duration(a.SegmentDurMS * 1_000_000))
	if cfg.MinimumUpdatePeriodS != nil {
		mpd.MinimumUpdatePeriod = m.Seconds2DurPtr(*cfg.MinimumUpdatePeriodS)
	}
	if cfg.SuggestedPresentationDelayS != nil {
		mpd.SuggestedPresentationDelay = m.Seconds2DurPtr(*cfg.SuggestedPresentationDelayS)
	}
	if cfg.TimeShiftBufferDepthS != nil {
		mpd.TimeShiftBufferDepth = m.Seconds2DurPtr(*cfg.TimeShiftBufferDepthS)
	}

	if cfg.getAvailabilityTimeOffsetS() > 0 {
		if cfg.LatencyTargetMS == nil {
			return nil, fmt.Errorf("latencyTargetMS (ltgt) not set")
		}
		latencyTargetMS := uint32(*cfg.LatencyTargetMS)
		mpd.ServiceDescription = createServiceDescription(latencyTargetMS)
	}

	//TODO. Replace this with configured list of different UTCTiming methods
	mpd.UTCTimings = []*m.DescriptorType{
		{
			SchemeIdUri: "urn:mpeg:dash:utc:http-iso:2014",
			Value:       "https://time.akamai.com/?isoms",
		},
	}

	wTimes := calcWrapTimes(a, cfg, nowMS, *mpd.TimeShiftBufferDepth)

	period := mpd.Periods[0]
	period.Duration = nil
	period.Id = "P0" // To evolve. Set period name depending on start relative AST.
	period.Start = Ptr(m.Duration(0))

	switch cfg.liveMPDType() {
	case timeLineTime:
		for i, as := range period.AdaptationSets {
			lsi, err := adjustAdaptationSetForTimelineTime(cfg, a, as, wTimes)
			if err != nil {
				return nil, fmt.Errorf("adjustASForTimelineTime: %w", err)
			}
			if i == 0 {
				mpd.PublishTime = m.ConvertToDateTime(calcPublishTime(cfg, lsi))
			}
		}
	case timeLineNumber:
		return nil, fmt.Errorf("segmentTimeline with $Number$ not yet supported")
	case segmentNumber:
		for _, as := range period.AdaptationSets {
			err := adjustAdaptationSetForSegmentNumber(cfg, a, as, wTimes)
			if err != nil {
				return nil, fmt.Errorf("adjustASForSegmentNumber: %w", err)
			}
		}
		mpd.PublishTime = mpd.AvailabilityStartTime
	default:
		return nil, fmt.Errorf("unknown mpd type")
	}

	if len(cfg.TimeSubsStpp) > 0 {
		err = addTimeSubsStpp(cfg, a, period)
		if err != nil {
			return nil, fmt.Errorf("addTimeSubsStpp: %w", err)
		}
	}
	return mpd, nil
}

// createServiceDescription creates a fixed service description for low-latency
func createServiceDescription(latencyTargetMS uint32) []*m.ServiceDescriptionType {
	minLatency := latencyTargetMS * 3 / 4
	maxLatency := latencyTargetMS * 2
	return []*m.ServiceDescriptionType{
		{
			Id: 0,
			Latencies: []*m.LatencyType{
				{
					ReferenceId: 0,
					Max:         Ptr(maxLatency),
					Min:         Ptr(minLatency),
					Target:      Ptr(latencyTargetMS),
				},
			},
			PlaybackRates: []*m.PlaybackRateType{
				{
					Max: 1.04,
					Min: 0.96,
				},
			},
		},
	}
}

func createProducerRefenceTimes(startTimeS int) []*m.ProducerReferenceTimeType {
	return []*m.ProducerReferenceTimeType{
		{
			Id:               0,
			PresentationTime: 0,
			Type:             "encoder",
			WallClockTime:    string(m.ConvertToDateTime(float64(startTimeS))),
			UTCTiming: &m.DescriptorType{
				SchemeIdUri: "urn:mpeg:dash:utc:http-iso:2014",
				Value:       "http://time.akamai.com/?iso",
			},
		},
	}
}

func adjustAdaptationSetForTimelineTime(cfg *ResponseConfig, a *asset, as *m.AdaptationSetType, wt wrapTimes) (lastSegInfo, error) {
	lsi := lastSegInfo{}
	if as.SegmentTemplate == nil {
		return lsi, fmt.Errorf("no SegmentTemplate in AdapationSet")
	}
	atoMS := 0 //availabilityTimeOffset in ms
	if !cfg.AvailabilityTimeCompleteFlag {
		as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
		ato := cfg.getAvailabilityTimeOffsetS()
		if ato > 0 {
			as.SegmentTemplate.AvailabilityTimeOffset = ato
			atoMS = int(1000 * ato)
			as.ProducerReferenceTimes = createProducerRefenceTimes(cfg.StartTimeS)
		}
	}
	r := as.Representations[0] // Assume that any representation will be fine
	if as.SegmentTemplate.SegmentTimeline == nil {
		newST := m.SegmentTimelineType{}
		as.SegmentTemplate.SegmentTimeline = &newST
	}
	as.SegmentTemplate.StartNumber = nil
	as.SegmentTemplate.Duration = nil
	as.SegmentTemplate.Media = strings.Replace(as.SegmentTemplate.Media, "$Number$", "$Time$", -1)
	// Must have timescale from media segments here
	mediaTimescale := uint32(a.Reps[r.Id].MediaTimescale)
	as.SegmentTemplate.Timescale = &mediaTimescale
	stl := as.SegmentTemplate.SegmentTimeline

	stl.S, lsi = a.generateTimelineEntries(r.Id, wt.startWraps, wt.startRelMS, wt.nowWraps, wt.nowRelMS, atoMS)
	return lsi, nil
}

func adjustAdaptationSetForSegmentNumber(cfg *ResponseConfig, a *asset, as *m.AdaptationSetType, wt wrapTimes) error {
	if as.SegmentTemplate == nil {
		return fmt.Errorf("no SegmentTemplate in AdapationSet %d", as.Id)
	}
	if !cfg.AvailabilityTimeCompleteFlag {
		as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
		if cfg.getAvailabilityTimeOffsetS() > 0 {
			as.SegmentTemplate.AvailabilityTimeOffset = cfg.getAvailabilityTimeOffsetS()
			as.ProducerReferenceTimes = createProducerRefenceTimes(cfg.StartTimeS)
		}
	}
	if as.SegmentTemplate.Duration == nil {
		r0 := as.Representations[0]
		rep0 := a.Reps[r0.Id]
		dur := rep0.duration() / len(rep0.segments)
		timeScale := rep0.MediaTimescale
		as.SegmentTemplate.Duration = Ptr(uint32(dur))
		as.SegmentTemplate.Timescale = Ptr(uint32(timeScale))
	}
	as.SegmentTemplate.SegmentTimeline = nil
	if cfg.StartNr != nil {
		startNr := Ptr(uint32(*cfg.StartNr))
		as.SegmentTemplate.StartNumber = startNr
	}
	as.SegmentTemplate.Media = strings.Replace(as.SegmentTemplate.Media, "$Time$", "$Number$", -1)
	return nil
}

func addTimeSubsStpp(cfg *ResponseConfig, a *asset, period *m.PeriodType) error {
	var vAS *m.AdaptationSetType
	for _, as := range period.AdaptationSets {
		if as.ContentType == "video" {
			vAS = as
			break
		}
	}
	if vAS == nil {
		return fmt.Errorf("no video adaptation set found")
	}
	segDurMS := a.SegmentDurMS
	typicalStppSegSizeBits := 2000 * 8 // 2kB
	vST := vAS.SegmentTemplate
	for i, lang := range cfg.TimeSubsStpp {
		rep := m.NewRepresentation()
		rep.Id = SUBS_STPP_PREFIX + lang
		rep.Bandwidth = uint32(typicalStppSegSizeBits*1000) / uint32(segDurMS)
		rep.StartWithSAP = 1
		st := m.NewSegmentTemplate()
		st.Initialization = "$RepresentationID$/init.mp4"
		st.Media = "$RepresentationID$/$Number$.m4s"
		st.SetTimescale(1000)

		if vST.Duration != nil {
			st.Duration = Ptr(*vST.Duration * 1000 / vST.GetTimescale())
		}
		if vST.StartNumber != nil {
			st.StartNumber = vST.StartNumber
		}
		as := m.NewAdaptationSet()
		as.Id = Ptr(uint32(100 + i))
		as.Lang = lang
		as.ContentType = "text"
		as.MimeType = "application/mp4"
		as.SegmentAlignment = true
		as.Codecs = "stpp"
		as.Roles = append(as.Roles,
			&m.DescriptorType{SchemeIdUri: "urn:mpeg:dash:role:2011", Value: "subtitle"})
		as.SegmentTemplate = st
		as.Representations = append(as.Representations, rep)
		period.AdaptationSets = append(period.AdaptationSets, as)
	}
	return nil
}

// calcPublishTime calculates the last time there was a change in the manifest in seconds.
// availabilityTimeOffset > 0 influences the publishTime to be earlier with that value.
func calcPublishTime(cfg *ResponseConfig, lsi lastSegInfo) float64 {
	switch cfg.liveMPDType() {
	case segmentNumber:
		// For single-period case, nothing change after startTime
		return float64(cfg.StartTimeS)
	case timeLineTime:
		// Here we need the publish time of the last segment
		return lastSegAvailTimeS(cfg, lsi)
	default: // timeLineNumber
		panic("liveMPD type not yet implemented")
	}
}

// lastSegAvailTimeS returns the availabilityTime of the last segment.
func lastSegAvailTimeS(cfg *ResponseConfig, lsi lastSegInfo) float64 {
	availTimeS := float64(lsi.startTime) / float64(lsi.timescale)
	if cfg.AvailabilityTimeOffsetS != nil {
		availTimeS -= *cfg.AvailabilityTimeOffsetS
	} else {
		availTimeS += float64(lsi.dur) / float64(lsi.timescale)
	}
	return availTimeS
}
