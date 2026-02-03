// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	m "github.com/Eyevinn/dash-mpd/mpd"
)

const (
	ProfileAdvancedLinear = "urn:mpeg:dash:profile:advanced-linear:2025"
)

func hasExtraSpaces(config string) bool {
	if config != strings.TrimSpace(config) {
		return true
	}
	return strings.Contains(config, " ;") || strings.Contains(config, "; ") ||
		strings.Contains(config, " ,") || strings.Contains(config, ", ")
}

// addAdvancedLinearProfileIfMissing adds the AdvancedLinear profile to the profiles string if it's not already present
func addAdvancedLinearProfileIfMissing(profiles m.ListOfProfilesType) m.ListOfProfilesType {
	if strings.Contains(string(profiles), ProfileAdvancedLinear) {
		return profiles
	}
	return profiles.AddProfile(ProfileAdvancedLinear)
}

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
	wt.startWraps = (wt.startTimeMS - startTimeMS) / a.LoopDurMS
	wt.startWrapMS = wt.startWraps*a.LoopDurMS + startTimeMS
	wt.startRelMS = wt.startTimeMS - wt.startWrapMS

	wt.nowWraps = (nowMS - startTimeMS) / a.LoopDurMS
	wt.nowWrapMS = wt.nowWraps*a.LoopDurMS + startTimeMS
	wt.nowRelMS = nowMS - wt.nowWrapMS

	return wt
}

// maxPatternLengthBruteForce is the maximum pattern length tried during brute-force search
// when the expected pattern length cannot be computed mathematically.
const maxPatternLengthBruteForce = 12

// gcd returns the greatest common divisor of a and b.
func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// computeExpectedPatternLen calculates the expected audio pattern length
// from the video reference entries and audio parameters.
// Returns 0 if video durations are not constant (fall back to brute force),
// 1 if no pattern is needed (audio aligns perfectly),
// or the exact pattern length otherwise.
func computeExpectedPatternLen(refSE segEntries, audioTimescale uint32, audioSampleDur uint32) int {
	if len(refSE.entries) == 0 || audioSampleDur == 0 {
		return 0
	}

	// Check if all video segment durations are the same
	firstDur := refSE.entries[0].D
	for _, e := range refSE.entries {
		if e.D != firstDur {
			return 0 // not constant → fall back to brute force
		}
	}

	audioSamplesPerSeg := firstDur * uint64(audioTimescale) / uint64(refSE.mediaTimescale)
	return int(uint64(audioSampleDur) / gcd(audioSamplesPerSeg, uint64(audioSampleDur)))
}

func genLaURL(cfg *ResponseConfig) string {
	laURL := cfg.Host + strings.Join(cfg.URLParts[:cfg.URLContentIdx+1], "/") + laURLSuffix
	return laURL
}

// LiveMPD generates a dynamic configured MPD for a VoD asset.
func LiveMPD(a *asset, mpdName string, cfg *ResponseConfig, drmCfg *drm.DrmConfig, nowMS int) (*m.MPD, error) {
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
	if cfg.PatchTTL > 0 && mpd.Id == "" {
		slog.Debug("Inserting ID for MPD for patch", "id", "auto-patch-id")
		mpd.Id = "auto-patch-id"
	}
	if cfg.AddLocationFlag {
		var strBuf strings.Builder
		strBuf.WriteString(cfg.Host)
		for i := 1; i < len(cfg.URLParts); i++ {
			strBuf.WriteString("/")
			switch {
			case strings.HasPrefix(cfg.URLParts[i], "startrel_"):
				strBuf.WriteString(fmt.Sprintf("start_%d", cfg.StartTimeS))
			case strings.HasPrefix(cfg.URLParts[i], "stoprel_"):
				strBuf.WriteString(fmt.Sprintf("stop_%d", *cfg.StopTimeS))
			default:
				strBuf.WriteString(cfg.URLParts[i])
			}
		}
		mpd.Location = []*m.LocationType{
			{
				Value: strBuf.String(),
			},
		}
	}

	// Parse SSR configuration
	ssrNextMap, ssrPrevMap, err := parseSSRAS(cfg.SSRAS)
	if err != nil {
		return nil, fmt.Errorf("parse SSRAS: %w", err)
	}
	chunkDurSSRMap, err := parseChunkDurSSR(cfg.ChunkDurSSR)
	if err != nil {
		return nil, fmt.Errorf("parse ChunkDurSSR: %w", err)
	}

	if cfg.SSRFlag {
		mpd.Profiles = addAdvancedLinearProfileIfMissing(mpd.Profiles)
	}

	if cfg.getAvailabilityTimeOffsetS() > 0 {
		if !cfg.AvailabilityTimeCompleteFlag {
			if cfg.LatencyTargetMS == nil {
				return nil, fmt.Errorf("latencyTargetMS (ltgt) not set")
			}
			latencyTargetMS := uint32(*cfg.LatencyTargetMS)
			mpd.ServiceDescription = createServiceDescription(latencyTargetMS)
		}
	}

	addUTCTimings(mpd, cfg)

	afterStop := false
	endTimeMS := nowMS
	if cfg.StopTimeS != nil {
		stopTimeMS := *cfg.StopTimeS * 1000
		if stopTimeMS < nowMS {
			endTimeMS = stopTimeMS
			afterStop = true
		}
	}

	wTimes := calcWrapTimes(a, cfg, endTimeMS, *mpd.TimeShiftBufferDepth)

	period := mpd.Periods[0]
	period.Duration = nil
	period.Id = "P0"
	period.Start = Ptr(m.Duration(0))
	for bNr := 0; bNr < len(cfg.Traffic); bNr++ {
		b := m.NewBaseURL(baseURL(bNr))
		period.BaseURLs = append(period.BaseURLs, b)
	}

	fillContentTypes(a.AssetPath, period)

	adaptationSets := orderAdaptationSetsByContentType(period.AdaptationSets)
	var refSegEntries segEntries
	for asIdx, as := range adaptationSets {
		if as.SegmentTemplate != nil {
			as.SegmentTemplate.EndNumber = nil // Never output endNumber
		}

		var explicitChunkDurS = (*float64)(nil)

		switch as.ContentType {
		case "video", "audio":
			if cfg.PatchTTL > 0 && as.Id == nil {
				slog.Debug("Inserting ID for AdaptationSet for patch", "contentType", as.ContentType, "id", asIdx+1)
				as.Id = Ptr(uint32(asIdx + 1))
			}
			if cfg.DRM != "" {
				if a.refRep.PreEncrypted {
					return nil, fmt.Errorf("drm parameter %q, but pre-encrypted asset %s cannot be encrypted again",
						cfg.DRM, a.AssetPath)
				}
				switch cfg.DRM {
				case "eccp-cenc", "eccp-cbcs":
					if a.refRep.PreEncrypted {
						return nil, fmt.Errorf("pre-encrypted asset %s cannot be encrypted again", a.AssetPath)
					}
					laURL := genLaURL(cfg)
					cp := m.NewContentProtection()
					cp.SchemeIdUri = "urn:mpeg:dash:mp4protection:2011"
					cp.Value = cfg.DRM[5:]
					cp.DefaultKID = kidFromString(laURL).String()
					as.ContentProtections = append(as.ContentProtections, cp)
					cp = m.NewContentProtection()
					cp.SchemeIdUri = m.DRM_CLEAR_KEY_DASHIF
					cp.Value = "ClearKey1.0"
					cp.LaURL = &m.LaURLType{
						LicenseType: "EME-1.0",
						Value:       m.AnyURI(laURL),
					}
					as.ContentProtections = append(as.ContentProtections, cp)
				default:
					if drmCfg == nil {
						return nil, fmt.Errorf("drm parameter %q, but no DRM configured", cfg.DRM)
					}
					d, ok := drmCfg.Map[cfg.DRM]
					if !ok {
						return nil, fmt.Errorf("drm parameter %q, but no matching  DRM configuration found", cfg.DRM)
					}
					key, err := d.CPIXData.GetContentKey(string(as.ContentType))
					if err != nil {
						return nil, fmt.Errorf("get content key: %w", err)
					}
					keyID := key.KeyID
					cp := m.NewContentProtection()
					cp.SchemeIdUri = "urn:mpeg:dash:mp4protection:2011"
					cp.DefaultKID = keyID.String()
					cp.Value = key.CommonEncryptionScheme
					as.ContentProtections = append(as.ContentProtections, cp)
					for _, drmSys := range d.CPIXData.DRMSystems {
						if !bytes.Equal(drmSys.KeyID, keyID) {
							continue
						}
						fullURN := fmt.Sprintf("urn:uuid:%s", drmSys.SystemID)
						drmSystem, ok := drm.DrmNames[fullURN]
						if !ok {
							return nil, fmt.Errorf("unknown DRM system %s", fullURN)
						}
						cpValue, ok := drm.ContentProtectionValues[fullURN]
						if !ok {
							return nil, fmt.Errorf("unknown DRM system %s", fullURN)
						}
						laURL := d.URLs[drmSystem].LaURL
						if laURL == "" {
							slog.Info("no LaURL for CPIX DRM", "DRM", drmSystem)
							continue
						}

						cp = m.NewContentProtection()
						cp.SchemeIdUri = m.AnyURI(fullURN)
						cp.Value = cpValue
						if drmSys.PSSH != "" {
							cp.Pssh = &m.PsshType{
								Value: drmSys.PSSH,
							}
						}
						cp.LaURL = &m.LaURLType{
							LicenseType: "EME-1.0",
							Value:       m.AnyURI(laURL),
						}
						if drmSys.SmoothStreamingProtectionHeaderData != "" {
							cp.MSPro = &m.MSProType{
								Value: drmSys.SmoothStreamingProtectionHeaderData,
							}
						}
						certURL := d.URLs[drmSystem].CertificateURL
						if certURL != "" {
							cu := m.CerturlType{Value: m.AnyURI(certURL)}
							cp.Certurls = []m.CerturlType{cu}
						}
						as.ContentProtections = append(as.ContentProtections, cp)
					}
				}
			}
		}
		if as.ContentType == "video" && cfg.Query != nil {
			ep := m.NewDescriptor(UrlParamSchemeIdUri, "", "")
			ep.UrlQueryInfo = &m.UrlQueryInfoType{
				QueryTemplate:  "$querypart$",
				UseMPDUrlQuery: true,
			}
			as.EssentialProperties = append(as.EssentialProperties, ep)
		}

		if as.ContentType == "video" && cfg.SCTE35PerMinute != nil {
			// Add SCTE35 signaling
			as.InbandEventStreams = append(as.InbandEventStreams,
				&m.EventStreamType{
					SchemeIdUri: scte35.SchemeIDURI,
					Value:       "",
				})
		}

		if cfg.SSRFlag && as.Id != nil {
			prevID, prevIDExists := ssrPrevMap[*as.Id]
			nextID, nextIDExists := ssrNextMap[*as.Id]

			var prevIDPtr *uint32
			if prevIDExists {
				prevIDPtr = &prevID
			}

			if nextIDExists {
				//Low Delay Adaptation Set
				updateSSRAdaptationSet(as, nextID, prevIDPtr, chunkDurSSRMap, &explicitChunkDurS)
			} else if prevIDExists {
				// Regular Adaptation Set for switching
				updateSwitchingAdaptationSet(as, prevID)
				// Low Latency Adaptation Set
				if cfg.ChunkDurS != nil {
					explicitChunkDurS = cfg.ChunkDurS //K calculation

					if as.SegmentTemplate != nil {
						as.SegmentTemplate.Media = strings.ReplaceAll(as.SegmentTemplate.Media, "$Number$", "$Number$_$SubNumber$")
					}

					as.StartWithSAP = 1

					if as.ContentType == "video" {
						ep := m.NewDescriptor(SsrSchemeIdUri, "", "")
						as.EssentialProperties = append(as.EssentialProperties, ep)
					}
				}

			}
		}

		// Update RepData with ChunkDurSSR if configured
		if explicitChunkDurS != nil {
			// Update all representations of this adaptation set
			for _, rep := range as.Representations {
				if repData, exists := a.Reps[rep.Id]; exists {
					repData.ChunkDurSSRS = explicitChunkDurS
				}
			}
		}

		atoMS, err := setOffsetInAdaptationSet(cfg, as)
		if err != nil {
			return nil, err
		}
		var se segEntries
		if asIdx == 0 {
			// Assume that first representation is as good as any, so can be reference
			var err error
			refSegEntries, err = a.generateTimelineEntries(as.Representations[0].Id, wTimes, atoMS, explicitChunkDurS)
			if err != nil {
				return nil, err
			}
			se = refSegEntries
		} else {
			switch as.ContentType {
			case "video", "text", "image":
				var err error
				se, err = a.generateTimelineEntries(as.Representations[0].Id, wTimes, atoMS, explicitChunkDurS)
				if err != nil {
					return nil, err
				}
			case "audio":
				var err error
				se, err = a.generateTimelineEntriesFromRef(refSegEntries, as.Representations[0].Id, explicitChunkDurS)
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unknown content type %s", as.ContentType)
			}
		}

		templateType := cfg.liveMPDType()
		if as.ContentType == "image" {
			templateType = segmentNumber
		}
		switch templateType {
		case timeLineTime, timeLineNumber:
			err := adjustAdaptationSetForTimeline(cfg, se, as, refSegEntries, a)
			if err != nil {
				return nil, fmt.Errorf("adjustASFor %s: %w", templateType, err)
			}
			if asIdx == 0 {
				mpd.PublishTime = m.ConvertToDateTime(calcPublishTime(cfg, se.lsi))
			}
		case segmentNumber:
			err := adjustAdaptationSetForSegmentNumber(cfg, a, as, explicitChunkDurS)
			if err != nil {
				return nil, fmt.Errorf("adjustASForSegmentNumber: %w", err)
			}
			mpd.PublishTime = mpd.AvailabilityStartTime
		default:
			return nil, fmt.Errorf("unknown mpd type")
		}
	}
	if len(cfg.TimeSubsStpp) > 0 {
		err = addTimeSubs(cfg, a, period, cfg.TimeSubsStpp, "stpp")
		if err != nil {
			return nil, fmt.Errorf("addTimeSubs stpp: %w", err)
		}
	}
	if len(cfg.TimeSubsWvtt) > 0 {
		err = addTimeSubs(cfg, a, period, cfg.TimeSubsWvtt, "wvtt")
		if err != nil {
			return nil, fmt.Errorf("addTimeSubs wvtt: %w", err)
		}
	}
	if cfg.PeriodsPerHour == nil {
		if afterStop {
			mpdDurS := *cfg.StopTimeS - cfg.StartTimeS
			makeMPDStatic(mpd, mpdDurS)
			return mpd, nil
		}
		addPatchLocation(mpd, cfg)
		return mpd, nil
	}

	// Split into multiple periods
	err = splitPeriod(mpd, a, cfg, wTimes)
	if err != nil {
		return nil, fmt.Errorf("splitPeriods: %w", err)
	}

	if cfg.liveMPDType() == segmentNumber {
		mpd.PublishTime, err = lastPeriodStartTime(mpd)
		if err != nil {
			return nil, fmt.Errorf("lastPeriodStartTime: %w", err)
		}
	}

	if afterStop {
		mpdDurS := *cfg.StopTimeS - cfg.StartTimeS
		makeMPDStatic(mpd, mpdDurS)
		return mpd, nil
	}
	addPatchLocation(mpd, cfg)

	return mpd, nil
}

func updateSSRAdaptationSet(as *m.AdaptationSetType, nextID uint32, prevID *uint32,
	chunkDurSSRMap map[uint32]float64, explicitChunkDurS **float64) {
	// Add SubNumber to SegmentTemplate
	if as.SegmentTemplate != nil {
		as.SegmentTemplate.Media = strings.ReplaceAll(as.SegmentTemplate.Media, "$Number$", "$Number$_$SubNumber$")
	}

	// SupplementalProperty schemeIdUri="urn:mpeg:dash:adaptation-set-switching:2016"
	var switchingValue string
	if prevID != nil {
		switchingValue = strconv.FormatUint(uint64(nextID), 10) + "," + strconv.FormatUint(uint64(*prevID), 10)
	} else {
		switchingValue = strconv.FormatUint(uint64(nextID), 10)
	}
	sp := m.NewDescriptor(AdaptationSetSwitchingSchemeIdUri, switchingValue, "")
	as.SupplementalProperties = append(as.SupplementalProperties, sp)

	if chunkDur, exists := chunkDurSSRMap[*as.Id]; exists {
		*explicitChunkDurS = &chunkDur
	}

	if as.ContentType == "video" {
		// Add SegmentSequenceProperties to signal Low-Delay
		as.SegmentSequenceProperties = &m.SegmentSequencePropertiesType{
			SapType: 1,
			Cadence: 1,
		}

		// AdaptationSet@startWithSAP = 1
		as.StartWithSAP = 1

		// EssentialProperty schemeIdUri="urn:mpeg:dash:ssr:2023"
		ssrValue := strconv.FormatUint(uint64(nextID), 10)
		ep := m.NewDescriptor(SsrSchemeIdUri, ssrValue, "")
		as.EssentialProperties = append(as.EssentialProperties, ep)
	}
}

func updateSwitchingAdaptationSet(as *m.AdaptationSetType, prevID uint32) {
	sp := m.NewDescriptor(AdaptationSetSwitchingSchemeIdUri, strconv.FormatUint(uint64(prevID), 10), "")
	as.SupplementalProperties = append(as.SupplementalProperties, sp)
}

// lastPeriodStartTime returns the absolute startTime of the last Period.
func lastPeriodStartTime(mpd *m.MPD) (m.DateTime, error) {
	lastPeriod := mpd.Periods[len(mpd.Periods)-1]
	lastRelStartS := time.Duration(*lastPeriod.Start).Seconds()
	ast, err := mpd.AvailabilityStartTime.ConvertToSeconds()
	if err != nil {
		return "", err
	}
	lastAbsStart := ast + lastRelStartS
	return m.ConvertToDateTime(lastAbsStart), nil
}

func addPatchLocation(mpd *m.MPD, cfg *ResponseConfig) {
	// Still live. Add patch location if specified
	if cfg.PatchTTL > 0 {
		baseURL := "/patch" + strings.Replace(strings.Join(cfg.URLParts, "/"), ".mpd", ".mpp", 1)
		escapedPublishTime := url.QueryEscape(string(mpd.PublishTime))
		fullURL := fmt.Sprintf("%s?publishTime=%s", baseURL, escapedPublishTime)
		mpd.PatchLocation = append(mpd.PatchLocation,
			&m.PatchLocationType{
				Ttl:   float64(cfg.PatchTTL),
				Value: m.AnyURI(fullURL),
			},
		)
	}
}

func makeMPDStatic(mpd *m.MPD, mpdDurS int) {
	mpd.Type = Ptr(m.STATIC_TYPE)
	mpd.TimeShiftBufferDepth = nil
	mpd.MinimumUpdatePeriod = nil
	mpd.SuggestedPresentationDelay = nil
	mpd.MediaPresentationDuration = m.Seconds2DurPtr(mpdDurS)
}

// splitPeriod splits the single-period MPD into multiple periods given cfg.PeriodsPerHour
// continuity is signalled if configured.
func splitPeriod(mpd *m.MPD, a *asset, cfg *ResponseConfig, wTimes wrapTimes) error {
	if len(mpd.Periods) != 1 {
		return fmt.Errorf("not exactly one period in the MPD")
	}
	if cfg.PeriodsPerHour == nil {
		return nil
	}
	periodDur := 3600 / *cfg.PeriodsPerHour
	if periodDur*1000%a.SegmentDurMS != 0 {
		return fmt.Errorf("period duration %ds not a multiple of segment duration %dms", periodDur, a.SegmentDurMS)
	}

	startPeriodNr := wTimes.startTimeMS / (periodDur * 1000)
	endPeriodNr := wTimes.nowMS / (periodDur * 1000)
	inPeriod := mpd.Periods[0]
	nrPeriods := endPeriodNr - startPeriodNr + 1
	periods := make([]*m.Period, 0, nrPeriods)
	for pNr := startPeriodNr; pNr <= endPeriodNr; pNr++ {
		p := inPeriod.Clone()
		p.Id = fmt.Sprintf("P%d", pNr)
		p.Start = m.Seconds2DurPtr(pNr * periodDur)
		for aNr, as := range p.AdaptationSets {
			inAS := inPeriod.AdaptationSets[aNr]
			timeScale := int(as.SegmentTemplate.GetTimescale())
			pto := Ptr(uint64(pNr * periodDur * timeScale))
			templateType := cfg.liveMPDType()
			if as.ContentType == "image" {
				templateType = segmentNumber
			}
			switch templateType {
			case segmentNumber:
				as.SegmentTemplate.PresentationTimeOffset = pto
				segDur := int(*as.SegmentTemplate.Duration)
				startNr := uint32(pNr * periodDur * timeScale / segDur)
				as.SegmentTemplate.StartNumber = Ptr(startNr)
			case timeLineTime:
				as.SegmentTemplate.PresentationTimeOffset = pto
				inS := inAS.SegmentTemplate.SegmentTimeline.S
				periodStart, periodEnd := uint64(pNr*periodDur), uint64((pNr+1)*periodDur)
				as.SegmentTemplate.SegmentTimeline.S, _ = reduceS(inS, nil, timeScale, periodStart, periodEnd)
			case timeLineNumber:
				as.SegmentTemplate.PresentationTimeOffset = pto
				inS := inAS.SegmentTemplate.SegmentTimeline.S
				startNr := inAS.SegmentTemplate.StartNumber
				periodStart, periodEnd := uint64(pNr*periodDur), uint64((pNr+1)*periodDur)
				as.SegmentTemplate.SegmentTimeline.S, as.SegmentTemplate.StartNumber = reduceS(inS, startNr, timeScale, periodStart, periodEnd)
			default:
				return fmt.Errorf("unknown mpd type")
			}
			if cfg.ContMultiPeriodFlag {
				periodContinuity := m.DescriptorType{
					SchemeIdUri: "urn:mpeg:dash:period-continuity:2015",
					Value:       "1",
				}
				as.SupplementalProperties = append(as.SupplementalProperties, &periodContinuity)
			}
		}
		periods = append(periods, p)
	}
	mpd.Periods = nil
	for _, p := range periods {
		mpd.AppendPeriod(p)
	}
	return nil
}

func reduceS(entries []*m.S, startNr *uint32, timescale int, periodStartS, periodEndS uint64) ([]*m.S, *uint32) {
	var t uint64
	pStart := periodStartS * uint64(timescale)
	pEnd := periodEndS * uint64(timescale)
	nr := uint32(0)
	if startNr != nil {
		nr = *startNr
	}
	outStartNr := nr
	newS := make([]*m.S, 0, len(entries))
	var currS *m.S
	for _, e := range entries {
		if e.T != nil {
			t = *e.T
		}
		d := e.D
		for i := 0; i <= e.R; i++ {
			if t < pStart {
				t += d
				nr++
				continue
			}
			if t >= pEnd {
				return newS, &nr
			}
			if currS == nil {
				currS = &m.S{
					T: Ptr(t),
					D: d,
				}
				outStartNr = nr
				newS = append(newS, currS)
			} else {
				if d == currS.D {
					currS.R++
				} else {
					currS = &m.S{
						T: Ptr(t),
						D: d,
					}
					newS = append(newS, currS)
				}
			}
			t += d
		}
	}
	return newS, &outStartNr
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

func createProducerReferenceTimes(startTimeS int) []*m.ProducerReferenceTimeType {
	return []*m.ProducerReferenceTimeType{
		{
			Id:               0,
			PresentationTime: 0,
			Type:             "encoder",
			WallClockTime:    string(m.ConvertToDateTime(float64(startTimeS))),
			UTCTiming: &m.DescriptorType{
				SchemeIdUri: UtcTimingHttpXSDateScheme,
				Value:       UtcTimingXSDateHttpServerMS,
			},
		},
	}
}

type segEntries struct {
	entries        []*m.S
	lsi            lastSegInfo
	startNr        int
	mediaTimescale uint32
}

func (s segEntries) lastNr() int {
	nrSegs := 0
	for _, e := range s.entries {
		nrSegs += int(e.R) + 1
	}
	return s.startNr + nrSegs - 1
}

func (s segEntries) lastTime() uint64 {
	t := uint64(0)
	lastD := uint64(0)
	for _, e := range s.entries {
		if e.T != nil {
			t = *e.T
		}
		t += e.D * (uint64(e.R) + 1)
		lastD = e.D
	}
	return t - lastD
}

// setOffsetInAdaptationSet sets the availabilityTimeOffset in the AdaptationSet.
// Returns ErrAtoInfTimeline if infinite ato set with timeline.
func setOffsetInAdaptationSet(cfg *ResponseConfig, as *m.AdaptationSetType) (atoMS int, err error) {
	if as.SegmentTemplate == nil {
		return 0, fmt.Errorf("no SegmentTemplate in AdaptationSet")
	}
	ato := cfg.getAvailabilityTimeOffsetS()
	if cfg.liveMPDType() != segmentNumber {
		if ato == math.Inf(+1) {
			return 0, ErrAtoInfTimeline
		}
	}
	if ato != 0 {
		as.SegmentTemplate.AvailabilityTimeOffset = m.FloatInf64(ato)
	}
	if !cfg.AvailabilityTimeCompleteFlag {
		as.SegmentTemplate.AvailabilityTimeComplete = Ptr(false)
		if cfg.getAvailabilityTimeOffsetS() > 0 {
			as.SegmentTemplate.AvailabilityTimeOffset = m.FloatInf64(cfg.getAvailabilityTimeOffsetS())
			as.ProducerReferenceTimes = createProducerReferenceTimes(cfg.StartTimeS)
		}
	}
	atoMS = int(1000 * ato)
	return atoMS, nil
}

func adjustAdaptationSetForTimeline(cfg *ResponseConfig, se segEntries, as *m.AdaptationSetType,
	refSE segEntries, a *asset) error {
	if as.SegmentTemplate.SegmentTimeline == nil {
		as.SegmentTemplate.SegmentTimeline = &m.SegmentTimelineType{}
	}

	as.SegmentTemplate.Duration = nil
	as.SegmentTemplate.Timescale = Ptr(se.mediaTimescale)

	isNumberBased := cfg.SegTimelineMode == SegTimelineModeNr || cfg.SegTimelineMode == SegTimelineModeNrPattern
	isPatternBased := cfg.SegTimelineMode == SegTimelineModePattern || cfg.SegTimelineMode == SegTimelineModeNrPattern

	if isNumberBased {
		as.SegmentTemplate.Media = strings.ReplaceAll(as.SegmentTemplate.Media, "$Time$", "$Number$")
		if se.startNr >= 0 {
			as.SegmentTemplate.StartNumber = Ptr(uint32(se.startNr))
		}
	} else {
		as.SegmentTemplate.Media = strings.ReplaceAll(as.SegmentTemplate.Media, "$Number$", "$Time$")
		as.SegmentTemplate.StartNumber = nil
	}

	if as.ContentType == "audio" && isPatternBased {
		// Compute expected pattern length from video/audio parameters
		expectedPatternLen := 0
		if a != nil && len(as.Representations) > 0 {
			if repData, ok := a.Reps[as.Representations[0].Id]; ok {
				audioSampleDur := repData.sampleDur()
				if audioSampleDur > 0 {
					expectedPatternLen = computeExpectedPatternLen(refSE, se.mediaTimescale, audioSampleDur)
				}
			}
		}
		// Try to detect and apply pattern based on cyclic alignment
		if patternSTL := detectAndApplyPattern(se, expectedPatternLen); patternSTL != nil {
			as.SegmentTemplate.SegmentTimeline = patternSTL
			// Add EssentialProperty for pattern support
			as.EssentialProperties = append(as.EssentialProperties, &m.DescriptorType{
				SchemeIdUri: "urn:mpeg:dash:pattern:2024",
			})
			return nil
		}
	}

	// Default: use regular segment entries
	as.SegmentTemplate.SegmentTimeline.S = se.entries
	return nil
}

func adjustAdaptationSetForSegmentNumber(cfg *ResponseConfig, a *asset, as *m.AdaptationSetType, explicitChunkDurS *float64) error {
	if as.SegmentTemplate.Duration == nil {
		r0 := as.Representations[0]
		rep0 := a.Reps[r0.Id]
		timeScale := rep0.MediaTimescale
		var dur uint32
		switch as.ContentType {
		case "audio":
			dur = uint32(a.refRep.duration() * timeScale / len(a.refRep.Segments) / a.refRep.MediaTimescale)
		default:
			dur = uint32(rep0.duration() / len(rep0.Segments))
		}
		as.SegmentTemplate.Duration = Ptr(uint32(dur))
		as.SegmentTemplate.Timescale = Ptr(uint32(timeScale))
	}
	as.SegmentTemplate.SegmentTimeline = nil
	if cfg.StartNr != nil {
		startNr := Ptr(uint32(*cfg.StartNr))
		as.SegmentTemplate.StartNumber = startNr
	}
	as.SegmentTemplate.Media = strings.ReplaceAll(as.SegmentTemplate.Media, "$Time$", "$Number$")

	if cfg.SSRFlag && explicitChunkDurS != nil {
		k, err := calculateK(uint64(*as.SegmentTemplate.Duration), int(as.SegmentTemplate.GetTimescale()), explicitChunkDurS)
		if err != nil {
			return err
		}
		as.SegmentTemplate.K = k
	}

	return nil
}

func addTimeSubs(cfg *ResponseConfig, a *asset, period *m.Period, languages []string, kind string) error {
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
	typicalWvttSegSizeBits := 200 * 8
	vST := vAS.SegmentTemplate
	for i, lang := range languages {
		rep := m.NewRepresentation()
		rep.StartWithSAP = 1
		st := m.NewSegmentTemplate()
		st.Initialization = "$RepresentationID$/init.mp4"
		if cfg.HasSegmentTimelineTime() {
			st.Media = "$RepresentationID$/$Time$.m4s"
		} else {
			st.Media = "$RepresentationID$/$Number$.m4s"
		}
		st.SetTimescale(SUBS_TIME_TIMESCALE)

		if vST.Duration != nil {
			st.Duration = Ptr(*vST.Duration * 1000 / vST.GetTimescale())
		}
		if vST.StartNumber != nil {
			st.StartNumber = vST.StartNumber
		}
		if vST.SegmentTimeline != nil {
			// Create segmentTimeline for subtitles from vST
			st.SegmentTimeline = changeTimelineTimescale(vST.SegmentTimeline, int(*vST.Timescale), SUBS_TIME_TIMESCALE)
		}
		as := m.NewAdaptationSet()
		as.Id = Ptr(uint32(100 + i))
		as.Lang = lang
		as.ContentType = "text"
		as.MimeType = "application/mp4"
		as.SegmentAlignment = true
		switch kind {
		case "stpp":
			rep.Id = SUBS_STPP_PREFIX + "-" + lang
			rep.Bandwidth = uint32(typicalStppSegSizeBits*1000) / uint32(segDurMS)
			as.Codecs = "stpp"
		case "wvtt":
			rep.Id = SUBS_WVTT_PREFIX + "-" + lang
			rep.Bandwidth = uint32(typicalWvttSegSizeBits*1000) / uint32(segDurMS)
			as.Codecs = "wvtt"
		}
		as.Roles = append(as.Roles,
			&m.DescriptorType{SchemeIdUri: "urn:mpeg:dash:role:2011", Value: "subtitle"})
		as.SegmentTemplate = st
		as.AppendRepresentation(rep)
		period.AppendAdaptationSet(as)
	}
	return nil
}

// calcPublishTime calculates the last time there was a change in the manifest in seconds.
func calcPublishTime(cfg *ResponseConfig, lsi lastSegInfo) float64 {
	switch cfg.liveMPDType() {
	case segmentNumber:
		// For single-period case, nothing change after startTime
		return float64(cfg.StartTimeS)
	case timeLineTime, timeLineNumber:
		// Here we need the availabilityTime of the last segment
		return lastSegAvailTimeS(cfg, lsi)
	default:
		panic("liveMPD type not yet implemented")
	}
}

// lastSegAvailTimeS returns the availabilityTime of the last segment,
// including the availabilityTimeOffset.
func lastSegAvailTimeS(cfg *ResponseConfig, lsi lastSegInfo) float64 {
	ast := float64(cfg.StartTimeS)
	if lsi.nr < 0 {
		return ast
	}
	availTime := lsi.availabilityTime(cfg.AvailabilityTimeOffsetS) + ast
	if availTime < ast {
		return ast
	}
	return availTime
}

// addUTCTimings adds or keeps the UTCTiming elements to the MPD.
func addUTCTimings(mpd *m.MPD, cfg *ResponseConfig) {
	switch {
	case len(cfg.UTCTimingMethods) == 0:
		// default if none is set. Use HTTP with ms precision.
		mpd.UTCTimings = []*m.DescriptorType{
			{
				SchemeIdUri: UtcTimingHttpXSDateScheme,
				Value:       UtcTimingXSDateHttpServerMS,
			},
		}
		return
	case len(cfg.UTCTimingMethods) == 1 && cfg.UTCTimingMethods[0] == UtcTimingKeep:
		// keep the UTCTiming elements in the MPD
		return
	default:
		for _, utcTiming := range cfg.UTCTimingMethods {
			var ut *m.DescriptorType
			switch utcTiming {
			case UtcTimingDirect:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingDirectScheme,
					Value:       string(mpd.PublishTime),
				}
			case UtcTimingNtp:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingNtpDateScheme,
					Value:       UtcTimingNtpServer,
				}
			case UtcTimingSntp:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingSntpDateScheme,
					Value:       UtcTimingSntpServer,
				}
			case UtcTimingHttpXSDate:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingHttpXSDateScheme,
					Value:       UtcTimingXSDateHttpServer,
				}
			case UtcTimingHttpXSDateMs:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingHttpXSDateScheme,
					Value:       UtcTimingXSDateHttpServerMS,
				}
			case UtcTimingHttpISO:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingHttpISOScheme,
					Value:       UtcTimingISOHttpServer,
				}
			case UtcTimingHttpISOMs:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingHttpISOScheme,
					Value:       UtcTimingISOHttpServerMS,
				}
			case UtcTimingHttpHead:
				ut = &m.DescriptorType{
					SchemeIdUri: UtcTimingHttpHeadScheme,
					Value:       fmt.Sprintf("%s%s", cfg.Host, UtcTimingHeadAsset),
				}

			case UtcTimingNone:
				cfg.UTCTimingMethods = nil
				return // no UTCTiming elements
			}
			mpd.UTCTimings = append(mpd.UTCTimings, ut)
		}
	}
}

func changeTimelineTimescale(inSTL *m.SegmentTimelineType, oldTimescale, newTimescale int) *m.SegmentTimelineType {
	factor := float64(newTimescale) / float64(oldTimescale)
	round := func(t uint64) uint64 {
		return uint64(math.Round(float64(t) * factor))
	}
	o := m.SegmentTimelineType{}
	o.S = make([]*m.S, 0, len(inSTL.S))
	for _, s := range inSTL.S {
		outS := m.S{
			T: m.Ptr(round(*s.T)),
			N: nil,
			D: round(s.D),
			R: s.R,
		}
		o.S = append(o.S, &outS)
	}
	return &o
}

// orderAdaptationSetsByContentType creates a new slice of adaptation sets with video first, and then audio.
func orderAdaptationSetsByContentType(aSets []*m.AdaptationSetType) []*m.AdaptationSetType {
	outASets := make([]*m.AdaptationSetType, 0, len(aSets))
	for _, as := range aSets {
		if as.ContentType == "video" {
			outASets = append(outASets, as)
		}
	}
	for _, as := range aSets {
		if as.ContentType == "audio" {
			outASets = append(outASets, as)
		}
	}
	for _, as := range aSets {
		if as.ContentType != "video" && as.ContentType != "audio" {
			outASets = append(outASets, as)
		}
	}

	return outASets
}

// detectAndApplyPattern detects cyclic patterns in audio segments
// that align with video segments at regular intervals.
// expectedPatternLen is a hint from computeExpectedPatternLen:
//   - 1 means no pattern needed (audio aligns perfectly) → returns nil immediately
//   - >1 means try only that specific length (skip brute force)
//   - 0 means fall back to brute force with maxPatternLengthBruteForce
//
// Returns a SegmentTimelineType with Pattern elements if pattern is found, nil otherwise.
func detectAndApplyPattern(se segEntries, expectedPatternLen int) *m.SegmentTimelineType {
	if expectedPatternLen == 1 {
		return nil // audio aligns perfectly, no pattern needed
	}

	if len(se.entries) < 2 {
		return nil
	}

	// Collect segment durations from the entries
	var durations []uint64
	for _, entry := range se.entries {
		// Expand the entry based on repeat count
		for i := 0; i <= int(entry.R); i++ {
			durations = append(durations, entry.D)
		}
	}

	if len(durations) < 4 { // Need at least 4 segments to detect a pattern
		return nil
	}

	// Determine which pattern lengths to try.
	// Require at least one segment beyond one full cycle to confirm the pattern repeats.
	var minLen, maxLen int
	if expectedPatternLen > 1 {
		// Try only the mathematically expected length
		minLen = expectedPatternLen
		maxLen = expectedPatternLen
	} else {
		// Brute force: try all lengths from 2 to maxPatternLengthBruteForce
		minLen = 2
		maxLen = maxPatternLengthBruteForce
	}

	for patternLen := minLen; patternLen <= maxLen && patternLen < len(durations); patternLen++ {
		// Check if pattern of this length repeats
		isRepeating := true
		for i := 0; i < len(durations); i++ {
			if durations[i] != durations[i%patternLen] {
				isRepeating = false
				break
			}
		}

		if isRepeating {
			// Extract the pattern of this length
			pattern := durations[:patternLen]

			// Rotate pattern to start with the longest duration
			canonicalPattern, _ := findCanonicalPattern(pattern)

			// Create Pattern element
			stl := &m.SegmentTimelineType{}
			patternElement := &m.PatternType{
				Id: 1,
				P:  make([]*m.RunLengthType, 0, len(canonicalPattern)),
			}

			// Build the pattern with run-length encoding
			i := 0
			for i < len(canonicalPattern) {
				dur := canonicalPattern[i]
				r := uint64(0)
				// Count consecutive occurrences
				for j := i + 1; j < len(canonicalPattern) && canonicalPattern[j] == dur; j++ {
					r++
				}
				p := &m.RunLengthType{
					D: dur,
					R: r,
				}
				patternElement.P = append(patternElement.P, p)
				i += int(r) + 1
			}

			stl.Pattern = []*m.PatternType{patternElement}

			// Calculate how many complete patterns we have in the sliding window
			numPatterns := len(durations) / patternLen
			if numPatterns > 0 {
				// Start with the first timestamp from original entries
				startTime := uint64(0)
				if len(se.entries) > 0 && se.entries[0].T != nil {
					startTime = *se.entries[0].T
				}

				// Calculate PE by searching for the best match between sliding window durations
				// and the canonical pattern. This is more robust than simple modulo calculation.
				patternEntryOffset, err := findPatternEntryOffset(durations, canonicalPattern)
				if err != nil {
					// This should not happen, but if it does, we can't use patterns
					return nil
				}

				// Calculate the total duration of the pattern
				var patternDuration uint64
				for _, dur := range canonicalPattern {
					patternDuration += dur
				}

				s := &m.S{
					T: &startTime,
					D: patternDuration, // Total duration of the pattern
					R: numPatterns - 1,
					CommonDurationPatternAttributes: m.CommonDurationPatternAttributes{
						P:  Ptr(uint32(1)),                  // Reference pattern id="1"
						PE: Ptr(uint32(patternEntryOffset)), // Start at the correct offset in the canonical pattern
					},
				}
				stl.S = []*m.S{s}
			}

			return stl
		}
	}

	return nil
}

// findCanonicalPattern rotates the pattern to start with the longest consecutive run of max values
// Returns the rotated pattern and the rotation offset
func findCanonicalPattern(pattern []uint64) ([]uint64, int) {
	if len(pattern) == 0 {
		return pattern, 0
	}

	// Find the maximum duration
	maxDur := pattern[0]
	for _, d := range pattern[1:] {
		if d > maxDur {
			maxDur = d
		}
	}

	// Find the position with the longest consecutive run of max values
	bestStart := 0
	bestRunLen := 0

	for start := 0; start < len(pattern); start++ {
		if pattern[start] == maxDur {
			// Count consecutive max values from this position
			runLen := 0
			for i := 0; i < len(pattern); i++ {
				if pattern[(start+i)%len(pattern)] == maxDur {
					runLen++
				} else {
					break
				}
			}
			if runLen > bestRunLen {
				bestRunLen = runLen
				bestStart = start
			}
		}
	}

	// Rotate pattern to start at bestStart
	canonical := make([]uint64, len(pattern))
	for i := 0; i < len(pattern); i++ {
		canonical[i] = pattern[(bestStart+i)%len(pattern)]
	}

	return canonical, bestStart
}

// findPatternEntryOffset finds where the sliding window starts in the canonical pattern
func findPatternEntryOffset(slidingWindowDurations, canonicalPattern []uint64) (int, error) {
	if len(canonicalPattern) == 0 || len(slidingWindowDurations) == 0 {
		return 0, nil
	}

	// Try each offset until we find a match
	patternLen := len(canonicalPattern)
	for offset := 0; offset < patternLen; offset++ {
		// Check if pattern matches at this offset
		match := true
		for i := 0; i < len(slidingWindowDurations) && i < patternLen; i++ {
			if slidingWindowDurations[i] != canonicalPattern[(offset+i)%patternLen] {
				match = false
				break
			}
		}
		if match {
			return offset, nil
		}
	}

	// This should never happen if pattern detection is working correctly
	slog.Error("No pattern entry offset found - pattern detection may be broken",
		"canonicalPattern", canonicalPattern,
		"slidingWindowDurations", slidingWindowDurations[:min(len(slidingWindowDurations), patternLen)])
	return 0, fmt.Errorf("internal error: no matching pattern entry offset found")
}

// fillContentTypes fills contentType if not set based on mimeType
func fillContentTypes(assetPath string, period *m.Period) {
	for _, as := range period.AdaptationSets {
		if as.ContentType == "" {
			as.ContentType = m.RFC6838ContentTypeType(contentTypeFromMimeType(as.MimeType))
			if as.ContentType == "" {
				as.ContentType = m.RFC6838ContentTypeType(guessContentTypeForAS(as))
				if as.ContentType == "" {
					asID := "not set"
					if as.Id != nil {
						asID = fmt.Sprintf("%d", *as.Id)
					}
					slog.Warn("no contentType, unknown mimeType, and no known codecs", "asset", assetPath, "adaptationSetID", asID)
				}
			}
		}
	}
}

var videoCodecPrefixes = []string{"avc", "hev", "hvc"}
var audioCodecPrefixes = []string{"mp4a", "ac-3", "ec-3", "ac-4"}
var textCodecPrefixes = []string{"stpp", "wvtt"}

func matchesPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// guessContentTypeForAS guesses the content type based on codecs and other data in the AdaptationSet or its Representations.
func guessContentTypeForAS(as *m.AdaptationSetType) string {
	if as.Codecs != "" {
		switch {
		case matchesPrefix(as.Codecs, videoCodecPrefixes):
			return "video"
		case matchesPrefix(as.Codecs, audioCodecPrefixes):
			return "audio"
		case matchesPrefix(as.Codecs, textCodecPrefixes):
			return "text"
		}
	}

	for _, rep := range as.Representations {
		contentType := contentTypeFromMimeType(rep.MimeType)
		if contentType != "" {
			return contentType
		}
		if rep.Codecs != "" {
			switch {
			case matchesPrefix(rep.Codecs, videoCodecPrefixes):
				return "video"
			case matchesPrefix(rep.Codecs, audioCodecPrefixes):
				return "audio"
			case matchesPrefix(rep.Codecs, textCodecPrefixes):
				return "text"
			}
		}
	}
	return ""
}

func contentTypeFromMimeType(mimeType string) string {
	switch mimeType {
	case "video/mp4":
		return "video"
	case "audio/mp4":
		return "audio"
	case "application/mp4":
		return "text"
	default:
		return ""
	}
}

// parseSSRAS parses the ssrAS configuration once
// and returns maps for next and previous adaptation set relationships.
func parseSSRAS(config string) (nextMap, prevMap map[uint32]uint32, err error) {
	if config == "" {
		return nil, nil, nil
	}

	nextMap = make(map[uint32]uint32)
	prevMap = make(map[uint32]uint32)

	if hasExtraSpaces(config) {
		return nil, nil, fmt.Errorf("configuration contains extra spaces: use exact format 'adaptationSetId,ssrValue;...' without spaces")
	}

	pairs := strings.Split(config, ";")
	for _, pair := range pairs {
		parts := strings.Split(pair, ",")
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("invalid format in pair '%s': expected 'adaptationSetId,ssrValue'", pair)
		}
		adaptationSetIDStr := strings.TrimSpace(parts[0])
		ssrValueStr := strings.TrimSpace(parts[1])

		adaptationSetID, err := parseUint32(adaptationSetIDStr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid adaptationSetId '%s' in pair '%s': must be a valid unsigned integer", adaptationSetIDStr, pair)
		}

		ssrValue, err := parseUint32(ssrValueStr)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid ssrValue '%s' in pair '%s': must be a valid unsigned integer", ssrValueStr, pair)
		}

		nextMap[adaptationSetID] = ssrValue
		prevMap[ssrValue] = adaptationSetID
	}

	return
}

// parseChunkDurSSR parses the ChunkDurSSR configuration
// and returns a map where the key is adaptationSetId and value is chunkDuration in seconds.
func parseChunkDurSSR(config string) (map[uint32]float64, error) {
	if config == "" {
		return nil, nil
	}

	if hasExtraSpaces(config) {
		return nil, fmt.Errorf("configuration contains extra spaces: use exact format 'adaptationSetId,chunkDuration;...' without spaces")
	}

	chunkDurMap := make(map[uint32]float64)
	pairs := strings.Split(config, ";")
	for _, pair := range pairs {
		parts := strings.Split(pair, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format in pair '%s': expected 'adaptationSetId,chunkDuration'", pair)
		}
		adaptationSetIDStr := strings.TrimSpace(parts[0])
		chunkDurationStr := strings.TrimSpace(parts[1])

		adaptationSetID, err := parseUint32(adaptationSetIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid adaptationSetId '%s' in pair '%s': must be a valid unsigned integer", adaptationSetIDStr, pair)
		}

		chunkDuration, err := strconv.ParseFloat(chunkDurationStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid chunkDuration '%s' in pair '%s': must be a valid number", chunkDurationStr, pair)
		}

		chunkDurMap[adaptationSetID] = chunkDuration
	}

	return chunkDurMap, nil
}
