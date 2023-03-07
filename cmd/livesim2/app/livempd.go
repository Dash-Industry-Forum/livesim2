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
	mpd.PublishTime = m.ConvertToDateTimeS(int64(nowMS / 1000)) //TODO. Make this update with change in MPD
	if cfg.SuggestedPresentationDelayS != nil {
		mpd.SuggestedPresentationDelay = m.Seconds2DurPtr(*cfg.SuggestedPresentationDelayS)
	}
	if cfg.TimeShiftBufferDepthS != nil {
		mpd.TimeShiftBufferDepth = m.Seconds2DurPtr(*cfg.TimeShiftBufferDepthS)
	}

	if cfg.AvailabilityTimeOffsetS != nil {
		mpd.ServiceDescription = createServiceDescription()
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
	period.Id = "P0" // TODO. set name to reflect start time
	period.Start = Ptr(m.Duration(0))

	switch cfg.liveMPDType() {
	case timeLineTime:
		for _, as := range period.AdaptationSets {
			err := adjustAdaptationSetForTimelineTime(cfg, a, as, wTimes)
			if err != nil {
				return nil, fmt.Errorf("adjustASForTimelineTime: %w", err)
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
func createServiceDescription() []*m.ServiceDescriptionType {
	return []*m.ServiceDescriptionType{
		{
			Id: 0,
			Latencies: []*m.LatencyType{
				{
					ReferenceId: 0,
					Max:         Ptr[uint32](6000),
					Min:         Ptr[uint32](2000),
					Target:      Ptr[uint32](3500),
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

func adjustAdaptationSetForTimelineTime(cfg *ResponseConfig, a *asset, as *m.AdaptationSetType, wt wrapTimes) error {
	if as.SegmentTemplate == nil {
		return fmt.Errorf("no SegmentTemplate in AdapationSet")
	}
	if !cfg.AvailabilityTimeCompleteFlag {
		as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
		if cfg.AvailabilityTimeOffsetS != nil {
			as.SegmentTemplate.AvailabilityTimeOffset = *cfg.AvailabilityTimeOffsetS
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
	stl.S = a.generateTimelineEntry(r.Id, wt.startWraps, wt.startRelMS, wt.nowWraps, wt.nowRelMS)
	return nil
}

func adjustAdaptationSetForSegmentNumber(cfg *ResponseConfig, a *asset, as *m.AdaptationSetType, wt wrapTimes) error {
	if as.SegmentTemplate == nil {
		return fmt.Errorf("no SegmentTemplate in AdapationSet %d", as.Id)
	}
	if !cfg.AvailabilityTimeCompleteFlag {
		as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
		if cfg.AvailabilityTimeOffsetS != nil {
			as.SegmentTemplate.AvailabilityTimeOffset = *cfg.AvailabilityTimeOffsetS
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
