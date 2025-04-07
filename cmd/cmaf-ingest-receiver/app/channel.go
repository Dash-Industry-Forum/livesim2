package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

type channel struct {
	mu                    sync.RWMutex
	name                  string
	dir                   string
	authUser              string
	authPswd              string
	trDatas               map[string]*trData
	segTimesGen           *segmentTimelineGenerator
	trIDs                 []string
	mpd                   *m.MPD
	startTime             int64
	startNr               int
	masterTrName          string
	masterTimescale       uint32
	masterSegDuration     uint32
	masterSeqNrShift      int64 // Segment number != (time + masterTimeShift)/ duration
	masterTimeShift       int64 // duration - (time % duration) if time%duration != 0
	timeShiftBufferDepthS uint32
	maxNrBufSegs          uint32
	receiveNrRaws         uint32 // Nr raw segments to receive
	currSeqNr             int64
	recSegCh              chan recSegData
	repsCfg               map[string]RepresentationConfig
	ignore                bool // Ignore (drop) channel. Don't process any content, just return 200 OK
}

type trData struct {
	name           string
	contentType    string
	init           *mp4.InitSegment
	lang           string
	timeScaleIn    uint32
	timeScaleOut   uint32
	nrSegsReceived uint32
}

type recSegData struct {
	name            string
	dts             uint64
	seqNr           uint32
	seqNrIn         uint32
	chunkNr         uint32
	dur             uint32
	totDur          uint32
	totSize         uint32
	nrSamples       uint16
	isMissing       bool
	isLmsg          bool
	isSlate         bool
	isComplete      bool
	shouldBeShifted bool // Set by receiver
	isShifted       bool // Is modified by the receiver
}

func newChannel(ctx context.Context, chCfg ChannelConfig, chDir string) *channel {
	mpd := m.NewMPD("dynamic")
	mpd.TimeShiftBufferDepth = m.Seconds2DurPtr(int(chCfg.TimeShiftBufferDepthS))
	mpd.Profiles = mpd.Profiles.AddProfile(m.PROFILE_LIVE)
	p := m.NewPeriod()
	p.Id = "P0"
	p.Start = m.Seconds2DurPtr(0)
	mpd.AppendPeriod(p)
	ch := channel{
		name:                  chCfg.Name,
		authUser:              chCfg.AuthUser,
		authPswd:              chCfg.AuthPswd,
		dir:                   chDir,
		trDatas:               make(map[string]*trData),
		segTimesGen:           newSegmentTimelineGenerator(chDir, initialSegmentsWindow),
		mpd:                   mpd,
		timeShiftBufferDepthS: chCfg.TimeShiftBufferDepthS,
		recSegCh:              make(chan recSegData, 10),
		startNr:               chCfg.StartNr,
		currSeqNr:             -1,
		receiveNrRaws:         chCfg.ReceiveNrRawSegments,
		repsCfg:               make(map[string]RepresentationConfig),
		ignore:                chCfg.Ignore,
	}
	for _, repCfg := range chCfg.Reps {
		ch.repsCfg[repCfg.Name] = repCfg
	}
	go ch.run(ctx)
	return &ch
}

func (ch *channel) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("Channel done", "chName", ch.name)
			return
		case rsd := <-ch.recSegCh:
			ch.receivedSegData(rsd)
		}
	}
}

// addInitDataAndUpdateTimescale adds track data from the init segment of a stream to a channel and its MPD.
// The init segments timescale may also be changed, so both timeScaleIn and timeScaleOut are set.
// Finally, a changed init segment must be written to disk.
// If needed, a new adaptation set is created.
// If the MPD does not have availabilityTimeOffset set, it is
// set from the value of mvhd.CreationTime if later or equal to 1970-01-01
// and the start of a year.
func (ch *channel) addInitDataAndUpdateTimescale(stream stream, init *mp4.InitSegment) error {
	if init == nil {
		return fmt.Errorf("no moov box found in init segment")
	}
	r := &trData{
		name:        stream.trName,
		contentType: stream.mediaType,
		init:        init,
	}
	log := slog.Default().With("chName", stream.chName, "trName", stream.trName)
	moov := init.Moov
	if len(moov.Traks) != 1 {
		return fmt.Errorf("expected one track, got %d", len(moov.Traks))
	}
	trak := moov.Traks[0]

	ch.startTime = 0 // 1970-01-01T00:00:00Z

	creationTimeS := moov.Mvhd.CreationTimeS()
	if creationTimeS >= 0 && creationTimeS%86400 == 0 { // Allow other start times but only full days (MediaLive)
		ch.startTime = creationTimeS
	}

	ch.mpd.AvailabilityStartTime = m.ConvertToDateTime(float64(ch.startTime))

	if trak.Mdia == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil || trak.Mdia.Minf.Stbl.Stsd == nil {
		return fmt.Errorf("no mdia, minf, stbl, or stsd box not found in track")
	}
	r.timeScaleIn = trak.Mdia.Mdhd.Timescale
	r.timeScaleOut = r.timeScaleIn
	switch stream.mediaType {
	case "video":
		//r.timeScaleOut = 180_000
	case "audio":
		//r.timeScaleOut = sampling_frequency, or 48000 for HE-AAC
	case "text":
		r.timeScaleOut = 1_000
	}
	trak.Mdia.Mdhd.Timescale = r.timeScaleOut
	lang := getLang(trak.Mdia)
	var bitrate uint32
	var role, displayName string
	if rCfg, ok := ch.repsCfg[stream.trName]; ok {
		if rCfg.Language != "" {
			lang = rCfg.Language
		}
		if rCfg.Bitrate != 0 {
			bitrate = rCfg.Bitrate
		}
		role = rCfg.Role
		displayName = rCfg.DisplayName
	}
	r.lang = lang

	stsd := trak.Mdia.Minf.Stbl.Stsd
	sampleEntry := stsd.Children[0].Type()

	ch.addTrData(r)
	switch sampleEntry {
	case "avc1", "hvc1", "mp4a", "ac-3", "ec-3", "stpp", "wvtt":
		// OK
	case "evte": // Event stream. Don't add to MPD or contentinfo, but keep the segments.
		// TODO. Handle event streams for SCTE-35 events and other cases
		return nil
	default:
		return fmt.Errorf("sample entry format %s not supported", sampleEntry)
	}

	p := ch.mpd.Periods[0]
	var currAsSet *m.AdaptationSetType
	for _, asSet := range p.AdaptationSets {
		asRole := ""
		if len(asSet.Roles) > 0 {
			asRole = asSet.Roles[0].Value
		}
		firstRep := asSet.Representations[0]
		if string(asSet.ContentType) == stream.mediaType && strings.HasPrefix(firstRep.Codecs, sampleEntry) &&
			lang == asSet.Lang && role == asRole {
			currAsSet = asSet
			break
		}
	}

	if currAsSet == nil {
		currAsSet = m.NewAdaptationSet()
		currAsSet.Lang = lang
		currAsSet.Id = m.Ptr(uint32(len(p.AdaptationSets) + 1))
		currAsSet.ContentType = m.RFC6838ContentTypeType(stream.mediaType)
		mimeType, ok := mimeTypeFromMediaType[stream.mediaType]
		if !ok {
			return fmt.Errorf("unknown mime type for %s", stream.mediaType)
		}
		currAsSet.MimeType = mimeType
		if role != "" {
			currAsSet.Roles = append(currAsSet.Roles, m.NewRole(role))
		}
		p.AppendAdaptationSet(currAsSet)
	}
	for _, c := range trak.Children {
		switch box := c.(type) {
		case *mp4.UdtaBox:
			for _, c2 := range box.Children {
				switch box2 := c2.(type) {
				case *mp4.KindBox:
					if box2.SchemeURI == "urn:mpeg:dash:role:2011" {
						currAsSet.Roles = append(currAsSet.Roles, m.NewRole(box2.Value))
					}
				}
			}
		}
	}
	rep := m.NewRepresentation()
	rep.Id = stream.trName
	currAsSet.AppendRepresentation(rep)
	ext := extFromMediaType[stream.mediaType]
	if currAsSet.SegmentTemplate == nil {
		currAsSet.SegmentTemplate = m.NewSegmentTemplate()
		currAsSet.SegmentTemplate.Timescale = m.Ptr(uint32(trak.Mdia.Mdhd.Timescale))
		currAsSet.SegmentTemplate.Media = fmt.Sprintf("$RepresentationID$/$Number$%s", ext)
		currAsSet.SegmentTemplate.Initialization = fmt.Sprintf("$RepresentationID$/init%s", ext)
	}
	if uint32(trak.Mdia.Mdhd.Timescale) != *currAsSet.SegmentTemplate.Timescale {
		return fmt.Errorf("timescale mismatch between track and adaptation set")
	}

	if bitrate == 0 {
		if btrt := trak.Mdia.Minf.Stbl.Stsd.GetBtrt(); btrt != nil {
			bitrate = btrt.AvgBitrate
		}
	}
	if bitrate != 0 {
		rep.Bandwidth = bitrate
	}

	if displayName != "" {
		rep.Labels = append(rep.Labels, &m.LabelType{Value: displayName})
	}

	switch stream.mediaType {
	case "video":
		err := extractVideoData(stsd, rep)
		if err != nil {
			log.Error("extractVideoData", "err", err)
			return fmt.Errorf("failed to extract video data: %w", err)
		}
	case "audio":
		err := extractAudioData(stsd, rep)
		if err != nil {
			log.Error("extractAudioData", "err", err)
			return fmt.Errorf("failed to extract video data: %w", err)
		}
	case "text":
		err := extractTextData(stsd, rep)
		if err != nil {
			log.Error("extractTextData", "err", err)
			return fmt.Errorf("failed to extract text data: %w", err)
		}
	}
	return nil
}

func (ch *channel) addChunkData(rsd recSegData) {
	slog.Debug("addChunkData", "chName", ch.name, "trName", rsd.name, "seqNr", rsd.seqNr, "chunkNr", rsd.chunkNr, "dur", rsd.dur)
	ch.recSegCh <- rsd
}

func (ch *channel) receivedSegData(rsd recSegData) {
	log := slog.Default().With("chName", ch.name, "trName", rsd.name, "seqNr", rsd.seqNr)
	if _, ok := ch.trDatas[rsd.name]; !ok {
		log.Error("received segData for unknown track")
		return
	}
	name := rsd.name
	switch {
	case rsd.chunkNr == 0:
		log.Debug("Received new segment")
	case !rsd.isComplete:
		log.Debug("Received chunk data", "chunkNr", rsd.chunkNr, "dur", rsd.dur)
	case rsd.isComplete:
		log.Debug("Received final segment data", "nrChunks", rsd.chunkNr, "dur",
			rsd.dur, "totDur", rsd.totDur, "size", rsd.totSize, "lsmg", rsd.isLmsg)
		if rsd.isLmsg {
			log.Info("Received lsmg indicating last segment")
		}
		newSeqNr, err := ch.segTimesGen.addSegmentData(log, rsd)
		if err != nil {
			log.Error("Failed to add segment data", "err", err)
		}
		if newSeqNr != 0 {
			nowMS := time.Now().UnixNano() / 1_000_000
			err := ch.segTimesGen.generateSegmentTimelineNrMPD(log, newSeqNr, ch, nowMS)
			if err != nil {
				log.Error("Failed to generate segment times", "err", err)
			}
		}

		if ch.masterSegDuration == 0 && name == ch.masterTrName {
			// Evaluate at least two durations to see if the are the same
			sdb := ch.segTimesGen.segDataBuffers[name]
			if sdb.nrItems() < 2 {
				return
			}
			for i := uint32(0); i < sdb.nrItems(); i++ {
				if name == ch.masterTrName && ch.masterSegDuration == 0 {
					// Evaluate the first two durations to see if they are consecutive with same duration. If not, drop the oldest one.
					if sdb.items[1].seqNr != sdb.items[0].seqNr+1 || sdb.items[1].dur != sdb.items[0].dur {
						ch.segTimesGen.dropSeqNr(sdb.items[0].seqNr)
						return
					}
					dur := sdb.items[1].dur
					ch.masterSegDuration = dur
					ch.mu.Lock()
					rd := ch.trDatas[name]
					ch.masterTimescale = rd.timeScaleOut
					segTime0 := int64(sdb.items[0].dts)
					seqNr0 := int64(sdb.items[0].seqNr)
					expectedSeqNr0 := segTime0 / int64(ch.masterSegDuration)
					if expectedSeqNr0 != seqNr0 {
						ch.masterSeqNrShift = expectedSeqNr0 - seqNr0
					}
					overShoot := segTime0 % int64(ch.masterSegDuration)
					if overShoot != 0 {
						ch.masterTimeShift = int64(ch.masterSegDuration) - overShoot
						ch.masterSeqNrShift++
					}
					if ch.masterSeqNrShift != 0 || ch.masterTimeShift != 0 {
						log.Info("Initial segment time", "seqNr0", seqNr0, "segTime0", segTime0,
							"seqNrShift", ch.masterSeqNrShift, "timeShift", ch.masterTimeShift)
					}
					ch.mu.Unlock()
					ch.deriveAndSetBitrates()
					ch.deriveAndSetFrameRates(log)
					err = ch.updateAndWriteMPD(log)
					if err != nil {
						log.Error("failed to write MPD", "err", err)
					}
					ch.maxNrBufSegs = ch.timeShiftBufferDepthS*ch.masterTimescale/ch.masterSegDuration + 2
					windowSize := ch.maxNrBufSegs - 1
					log.Info("Starting channel", "windowSize", windowSize, "seqNrShift", ch.masterSeqNrShift,
						"timeShift", ch.masterTimeShift)
					ch.segTimesGen.start(windowSize, ch.isShifted())
				}
			}
		}
	default:
		log.Error("Unclassified recSegData", "chunkNr", rsd.chunkNr, "dur", rsd.dur, "totDur", rsd.totDur)
	}
	if ch.masterTimescale == 0 {
		return // not ready yet
	}
}

// addTrData adds track data and a segment.
// If no previous video representation, this becomes the master track.
// TODO. Handle audio-only case (no video representation).
func (ch *channel) addTrData(rd *trData) {
	ch.mu.Lock()
	firstVideoTrack := true
	for _, rep := range ch.trDatas {
		if rep.contentType == "video" {
			firstVideoTrack = false
			break
		}
	}
	if firstVideoTrack {
		ch.masterTrName = rd.name
	}
	ch.trDatas[rd.name] = rd
	ch.trIDs = append(ch.trIDs, rd.name)
	sort.Strings(ch.trIDs)
	ch.mu.Unlock()
}

func extractVideoData(stsd *mp4.StsdBox, rep *m.RepresentationType) error {
	vse, ok := stsd.Children[0].(*mp4.VisualSampleEntryBox)
	if !ok {
		return fmt.Errorf("expected video sample entry, got %T", stsd.Children[0])
	}
	sampleEntry := vse.Type()
	rep.Width = uint32(vse.Width)
	rep.Height = uint32(vse.Height)
	var codecs string
	switch sampleEntry {
	case "avc1":
		decConfRec := stsd.AvcX.AvcC.DecConfRec
		spsRaw := decConfRec.SPSnalus[0]
		sps, err := avc.ParseSPSNALUnit(spsRaw, true)
		if err != nil {
			return fmt.Errorf("failed to parse avc1 SPS: %w", err)
		}
		codecs = avc.CodecString(sampleEntry, sps)
	case "hvc1":
		decConfRec := stsd.HvcX.HvcC.DecConfRec
		spsRaw := decConfRec.GetNalusForType(hevc.NALU_SPS)[0]
		sps, err := hevc.ParseSPSNALUnit(spsRaw)
		if err != nil {
			return fmt.Errorf("failed to parse hvc1 SPS: %w", err)
		}
		codecs = hevc.CodecString(sampleEntry, sps)
	}
	rep.Codecs = codecs
	return nil
}

// extractAudioData extracts representation data from an audio sample entry.
// Bitrate is not extracted here, since it is supposed to be in the btrt box.
func extractAudioData(stsd *mp4.StsdBox, rep *m.RepresentationType) error {
	ase, ok := stsd.Children[0].(*mp4.AudioSampleEntryBox)
	if !ok {
		return fmt.Errorf("expected audio sample entry, got %T", stsd.Children[0])
	}
	var codec string
	switch ase.Type() {
	case "mp4a":
		codec = "mp4a.40.2"          // AAC-LC is starting point
		if ase.SampleRate == 24000 { // Interpret this as HE-AAC if samplerate is 24000
			codec = "mp4a.40.5" // HE-AACv1
			if ase.ChannelCount == 1 {
				codec = "mp4a.40.29" // HE-AACv2
			}
		}
	case "ac-3":
		codec = "ac-3"
	case "ec-3":
		codec = "ec-3"
	}
	rep.Codecs = codec
	return nil
}

func extractTextData(stsd *mp4.StsdBox, rep *m.RepresentationType) error {
	switch stsd.Children[0].Type() {
	case "wvtt":
		rep.Codecs = "wvtt"
	case "stpp":
		rep.Codecs = "stpp"

	}
	return nil
}

func (ch *channel) updateAndWriteMPD(log *slog.Logger) error {
	for _, asSet := range ch.mpd.Periods[0].AdaptationSets {
		stl := asSet.SegmentTemplate
		dur := uint64(ch.masterSegDuration) * uint64((*stl.Timescale)) / uint64(ch.masterTimescale)
		stl.Duration = m.Ptr(uint32(dur))
		stl.StartNumber = m.Ptr(uint32(0))
	}
	mpdPath := filepath.Join(ch.dir, "manifest.mpd")
	err := writeMPD(ch.mpd, mpdPath)
	if err != nil {
		return fmt.Errorf("failed to write MPD: %w", err)
	}
	log.Debug("Wrote MPD", "mpdPath", mpdPath)
	return nil
}

// deriveAndSetBitrates estimates bitrates for variants without bitrate information.
// Only count unshifted or shifted segments, not both.
func (ch *channel) deriveAndSetBitrates() {
	for name, trd := range ch.trDatas {
		if trd.init.Moov.Trak.Mdia.Minf.Stbl.Stsd.GetBtrt() == nil {
			// Estimate bitrate from the segments available
			sdb := ch.segTimesGen.segDataBuffers[name]
			totDur := uint64(0)
			totSize := uint64(0)
			var timeScale uint32 = 0
		itemLoop:
			for i := uint32(0); i < sdb.nrItems(); i++ {
				switch {
				case timeScale == 0 && !sdb.items[i].isShifted:
					timeScale = trd.timeScaleIn
				case timeScale == 0 && sdb.items[i].isShifted:
					timeScale = trd.timeScaleOut
				case timeScale != 0 && sdb.items[i].isShifted && timeScale != trd.timeScaleOut:
					break itemLoop
				}
				totDur += uint64(sdb.items[i].dur)
				totSize += uint64(sdb.items[i].totSize)
			}
			bitrate := uint32(totSize * 8 * uint64(timeScale) / totDur)
		repLoop:
			for _, asSet := range ch.mpd.Periods[0].AdaptationSets {
				for _, rep := range asSet.Representations {
					if rep.Id == name {
						rep.Bandwidth = bitrate
						break repLoop
					}
				}
			}
		}
	}
}

func (ch *channel) deriveAndSetFrameRates(log *slog.Logger) {
	for name, trd := range ch.trDatas {
		sdb := ch.segTimesGen.segDataBuffers[name]
		if trd.contentType != "video" {
			continue
		}
		if sdb.nrItems() == 0 {
			log.Warn("Cannot derive frame rate since no segments for track", "trName", name)
			continue
		}
		firstSeg := sdb.items[0]

		nrFrames := uint32(firstSeg.nrSamples)
		dur := uint32(firstSeg.dur)
		timeScale := trd.timeScaleIn
		if firstSeg.isShifted {
			timeScale = trd.timeScaleOut
		}
		prod := nrFrames * timeScale
		frCGD := GCDuint32(prod, dur)
		nom := prod / frCGD
		denom := dur / frCGD
	repLoop:
		for _, asSet := range ch.mpd.Periods[0].AdaptationSets {
			for _, rep := range asSet.Representations {
				if rep.Id == name {
					rep.FrameRate = m.FrameRateType(fmt.Sprintf("%d/%d", nom, denom))
					break repLoop
				}
			}
		}
	}
}

func (ch *channel) isShifted() bool {
	return ch.masterSeqNrShift != 0 || ch.masterTimeShift != 0
}

func getLang(mdia *mp4.MdiaBox) string {
	if mdia == nil || mdia.Mdhd == nil {
		return "und"
	}
	lang := mdia.Mdhd.GetLanguage()
	if lang == "```" {
		lang = "und"
	}
	if lang[2] == 0x60 { // Backtick in language code is zero byte, drop it to make two-letter code
		lang = lang[:2]
	}
	if mdia.Elng != nil {
		lang = mdia.Elng.Language
	}
	return lang
}

func writeMPD(mpd *m.MPD, filePath string) error {
	fh, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create MPD file: %w", err)
	}
	defer finalClose(fh)
	_, err = mpd.Write(fh, "  ", true)
	if err != nil {
		return fmt.Errorf("failed to write MPD: %w", err)
	}
	return nil
}

func finalClose(closer io.Closer) {
	err := closer.Close()
	if err != nil {
		slog.Error("Failed to close", "err", err)
	}
}
