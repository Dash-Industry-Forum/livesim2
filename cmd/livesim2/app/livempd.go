package app

import (
	"fmt"
	"strings"

	m "github.com/Eyevinn/dash-mpd/mpd"
)

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
		mpd.ServiceDescription = []*m.ServiceDescriptionType{
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

	//TODO. Replace this with configured list of different UTCTiming methods
	mpd.UTCTimings = []*m.DescriptorType{
		{
			SchemeIdUri: "urn:mpeg:dash:utc:http-iso:2014",
			Value:       "https://time.akamai.com/?isoms",
		},
	}

	startTimeMS := nowMS - int(*mpd.TimeShiftBufferDepth)/1_000_000

	startWraps := (startTimeMS - cfg.StartTimeS*1000) / a.LoopDurMS
	startWrapMS := startWraps * a.LoopDurMS
	startRelMS := startTimeMS - startWrapMS

	nowWraps := (nowMS - cfg.StartTimeS*1000) / a.LoopDurMS
	nowWrapMS := nowWraps * a.LoopDurMS
	nowRelMS := nowMS - nowWrapMS

	period := mpd.Periods[0]
	period.Duration = nil
	period.Id = "P0" // TODO. set name to reflect start time
	period.Start = Ptr(m.Duration(0))

	switch cfg.liveMPDType() {
	case timeLineTime:
		for _, as := range period.AdaptationSets {
			if as.SegmentTemplate == nil {
				return nil, fmt.Errorf("no SegmentTemplate in AdapationSet")
			}
			if !cfg.AvailabilityTimeCompleteFlag {
				as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
				if cfg.AvailabilityTimeOffsetS != nil {
					as.SegmentTemplate.AvailabilityTimeOffset = *cfg.AvailabilityTimeOffsetS
					as.ProducerReferenceTimes = []*m.ProducerReferenceTimeType{
						{
							Id:               0,
							PresentationTime: 0,
							Type:             "encoder",
							WallClockTime:    string(m.ConvertToDateTime(float64(cfg.StartTimeS))),
							UTCTiming: &m.DescriptorType{
								SchemeIdUri: "urn:mpeg:dash:utc:http-iso:2014",
								Value:       "https://time.akamai.com/?isoms",
							},
						},
					}
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
			stl.S = a.generateTimelineEntry(r.Id, startWraps, startRelMS, nowWraps, nowRelMS)
		}
	case timeLineNumber:
		return nil, fmt.Errorf("segmentTimeline with $Number$ not yet supported")
	case segmentNumber:
		for _, as := range period.AdaptationSets {
			if as.SegmentTemplate == nil {
				return nil, fmt.Errorf("no SegmentTemplate in AdapationSet %d", as.Id)
			}
			if !cfg.AvailabilityTimeCompleteFlag {
				as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
				if cfg.AvailabilityTimeOffsetS != nil {
					as.SegmentTemplate.AvailabilityTimeOffset = *cfg.AvailabilityTimeOffsetS
					as.ProducerReferenceTimes = []*m.ProducerReferenceTimeType{
						{
							Id:               0,
							PresentationTime: 0,
							Type:             "encoder",
							WallClockTime:    string(m.ConvertToDateTime(float64(cfg.StartTimeS))),
							UTCTiming: &m.DescriptorType{
								SchemeIdUri: "urn:mpeg:dash:utc:http-iso:2014",
								Value:       "http://time.akamai.com/?iso",
							},
						},
					}
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
		}
	default:
		return nil, fmt.Errorf("unknown mpd type")
	}

	//TODO. Output SegmentTemplate + Number of SegmentTemplate + SegmentTimeline depending on URL

	// Set UTC timing
	// Copy periods
	// Possibly insert new periods
	// Copy adaptations sets
	// Copy SegmentTemplate
	// Copy Representations and modify them
	return mpd, nil
}
