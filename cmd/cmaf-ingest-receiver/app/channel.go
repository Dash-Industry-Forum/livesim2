package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
	segDataBuffers        map[string]*segDataBuffer
	trIDs                 []string
	mpd                   *m.MPD
	startTime             int64
	startNr               int
	masterTrName          string
	masterTimescale       int
	masterSegDuration     int
	timeShiftBufferDepthS int
	maxNrBufSegs          int
	currSeqNr             int64
	recSegCh              chan recSegData
}

type trData struct {
	name        string
	contentType string
	init        *mp4.InitSegment
	lang        string
	timeScale   int
}

type recSegData struct {
	name       string
	dts        uint64
	seqNr      uint32
	chunkNr    uint32
	dur        uint32
	totDur     uint32
	totSize    uint32
	isMissing  bool
	isLmsg     bool
	isSlate    bool
	isComplete bool
}

func newChannel(ctx context.Context, chCfg ChannelConfig, chDir string) *channel {
	mpd := m.NewMPD("dynamic")
	mpd.TimeShiftBufferDepth = m.Seconds2DurPtr(chCfg.TimeShiftBufferDepthS)
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
		segDataBuffers:        make(map[string]*segDataBuffer),
		mpd:                   mpd,
		timeShiftBufferDepthS: chCfg.TimeShiftBufferDepthS,
		recSegCh:              make(chan recSegData, 10),
		startNr:               chCfg.StartNr,
		currSeqNr:             -1,
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

// addInitData adds track data from the init segment of a stream to a channel and its MPD.
// If needed, a new adaptation set is created.
// If the MPD does not have availabilityTimeOffset set, it is
// set from the value of mvhd.CreationTime if later or equal to 1970-01-01.
func (ch *channel) addInitData(stream stream, init *mp4.InitSegment) error {
	if init == nil {
		return fmt.Errorf("no moov box found in init segment")
	}
	r := &trData{
		name:        stream.trName,
		contentType: stream.mediaType,
		init:        init,
	}
	moov := init.Moov
	if len(moov.Traks) != 1 {
		return fmt.Errorf("expected one track, got %d", len(moov.Traks))
	}
	trak := moov.Traks[0]
	ch.startTime = moov.Mvhd.CreationTimeS()
	if ch.mpd.AvailabilityStartTime == "" {
		if moov.Mvhd.CreationTimeS() >= 0 {
			ch.mpd.AvailabilityStartTime = m.ConvertToDateTime(float64(moov.Mvhd.CreationTimeS()))
		}
	}

	if trak.Mdia == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil || trak.Mdia.Minf.Stbl.Stsd == nil {
		return fmt.Errorf("no mdia, minf, stbl, or stsd box not found in track")
	}
	r.timeScale = int(trak.Mdia.Mdhd.Timescale)
	lang := getLang(trak.Mdia)
	r.lang = lang

	stsd := trak.Mdia.Minf.Stbl.Stsd
	sampleEntry := stsd.Children[0].Type()

	ch.addTrDataAndSegDataBuffer(r)
	switch sampleEntry {
	case "avc1", "hvc1", "mp4a", "stpp", "wvtt":
		// OK
	case "evte": // Event stream. Don't add to MPD, but keep the segments.
		// TODO. Handle event streams for SCTE-35 events and other cases
		return nil
	default:
		return fmt.Errorf("sample entry format %s not supported", sampleEntry)
	}

	p := ch.mpd.Periods[0]
	var currAsSet *m.AdaptationSetType
	for _, asSet := range p.AdaptationSets {
		firstRep := asSet.Representations[0]
		if string(asSet.ContentType) == stream.mediaType && strings.HasPrefix(firstRep.Codecs, sampleEntry) && lang == asSet.Lang {
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

	btrt := trak.Mdia.Minf.Stbl.Stsd.GetBtrt()
	if btrt != nil {
		rep.Bandwidth = btrt.AvgBitrate
	}
	switch stream.mediaType {
	case "video":
		err := extractVideoData(stsd, rep)
		if err != nil {
			slog.Error("extractVideoData", "err", err.Error())
			return fmt.Errorf("failed to extract video data: %w", err)
		}
	case "audio":
		err := extractAudioData(stsd, rep)
		if err != nil {
			slog.Error("extractAudioData", "err", err.Error())
			return fmt.Errorf("failed to extract video data: %w", err)
		}
	case "text":
		err := extractTextData(stsd, rep)
		if err != nil {
			slog.Error("extractTextData", "err", err.Error())
			return fmt.Errorf("failed to extract text data: %w", err)
		}
	}
	return nil
}

func (ch *channel) addChunkData(rsd recSegData) {
	ch.recSegCh <- rsd
}

func (ch *channel) receivedSegData(rsd recSegData) {
	sdb, ok := ch.segDataBuffers[rsd.name]
	if !ok {
		slog.Error("received segData for unkown track", "chName", ch.name, "trName", rsd.name)
	}
	name := rsd.name
	switch {
	case rsd.chunkNr == 0:
		slog.Debug("Received new segment", "chName", ch.name, "trName", name, "seqNr", rsd.seqNr)
	case !rsd.isComplete:
		slog.Debug("Received chunk data", "chName", ch.name, "trName", name, "seqNr", rsd.seqNr, "chunkNr", rsd.chunkNr, "dur", rsd.dur)
	case rsd.isComplete:
		slog.Debug("Received final segment data", "chName", ch.name, "trName", name, "seqNr", rsd.seqNr, "nrChunks", rsd.chunkNr, "dur",
			rsd.dur, "totDur", rsd.totDur, "lsmg", rsd.isLmsg)
		if rsd.isLmsg {
			slog.Info("Received lsmg indicating last segment", "chName", ch.name, "trName", name, "seqNr", rsd.seqNr)
		}
		err := sdb.add(rsd.dts, rsd.totDur, rsd.seqNr, rsd.isMissing, rsd.isSlate, rsd.isLmsg)
		if err != nil {
			slog.Error("Failed to add segment data", "chName", ch.name, "trName", name, "seqNr", rsd.seqNr, "err", err)
		}
		if ch.masterSegDuration == 0 && name == ch.masterTrName {
			// Evaluate at least two durations to see if the are the same
			if sdb.nrItems < 2 {
				return
			}
			for i := 0; i < sdb.nrItems; i++ {
				if name == ch.masterTrName && ch.masterSegDuration == 0 {
					// Evaluate the first two durations to see if they are the same. If not, drop the first one.
					dur := sdb.items[1].dur
					if sdb.items[0].dur != dur {
						sdb.dropFirst()
						return
					}
					ch.masterSegDuration = int(dur)
					ch.mu.RLock()
					rd := ch.trDatas[name]
					ch.mu.RUnlock()
					ch.masterTimescale = rd.timeScale
					err = ch.updateAndWriteMPD()
					if err != nil {
						slog.Error("failed to write MPD", "err", err)
					}
					ch.maxNrBufSegs = ch.timeShiftBufferDepthS*ch.masterTimescale/ch.masterSegDuration + 2
					for _, name := range ch.trIDs {
						ch.resizeSegDataBuffer(name, ch.maxNrBufSegs-1)
					}
					ch.currSeqNr = int64(rsd.seqNr)
				}
			}
		}
	default:
		slog.Error("Unclassified recSegData", "channel", ch.name, "track", rsd.name, "seqNr", rsd.seqNr, "chunkNr", rsd.chunkNr, "dur",
			rsd.dur, "totDur", rsd.totDur)
	}
}

// addTrDataAndSegDataBuffer adds track data and a segment data buffer.
// If no previous video representation, this becomes the master track.
// TODO. Handle audio-only case (no video representation).
func (ch *channel) addTrDataAndSegDataBuffer(rd *trData) {
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
	ch.segDataBuffers[rd.name] = newSegDataBuffer(initialSegBufferSize)
	ch.trIDs = append(ch.trIDs, rd.name)
	sort.Strings(ch.trIDs)
	ch.mu.Unlock()
}

func (fs *channel) resizeSegDataBuffer(trName string, size int) {
	fs.mu.Lock()
	fs.segDataBuffers[trName].reSize(size)
	fs.mu.Unlock()
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

// extractAudioData extracts variant data from an audio sample entry.
// A lot of code similar to pkg/ingest.createAudioTrackFromMP4.
// May consider extracting common code to a shared function at some time.
// Bitrate is not extracted here, since it is supposed to be in the btrt box.
func extractAudioData(stsd *mp4.StsdBox, rep *m.RepresentationType) error {
	ase, ok := stsd.Children[0].(*mp4.AudioSampleEntryBox)
	if !ok {
		return fmt.Errorf("expected audio sample entry, got %T", stsd.Children[0])
	}
	var codec string
	nrChannels := uint32(ase.ChannelCount)
	sampleRate := uint32(ase.SampleRate)
	switch ase.Type() {
	case "mp4a":
		// Use heuristiscs to determine if AAC-LC or HE-AACv1/v2
		codec = "mp4a.40.2"      // AAC-LC is starting point
		if sampleRate == 24000 { // Interpret this as HE-AAC if samplerate is 24000
			codec = "mp4a.40.5" // HE-AACv1
			if nrChannels == 1 {
				codec = "mp4a.40.29" // HE-AACv2
			}
		}
	case "ac-3":
		codec = "ac-3"
		dac3 := ase.Dac3
		nrChannels, chanmap := dac3.ChannelInfo()
		slog.Info("ac-3", "nrChannels", nrChannels, "chanmap", fmt.Sprintf("%02x", chanmap))
	case "ec-3":
		codec = "ec-3"
		dec3 := ase.Dec3
		nrChannels, chanmap := dec3.ChannelInfo()
		slog.Info("ec-3", "nrChannels", nrChannels, "chanmap", fmt.Sprintf("%02x", chanmap))
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

func (ch *channel) updateAndWriteMPD() error {
	for _, asSet := range ch.mpd.Periods[0].AdaptationSets {
		stl := asSet.SegmentTemplate
		dur := ch.masterSegDuration * int(*stl.Timescale) / ch.masterTimescale
		stl.Duration = m.Ptr(uint32(dur))
		stl.StartNumber = m.Ptr(uint32(0))
	}

	err := writeMPD(ch.mpd, filepath.Join(ch.dir, "manifest.mpd"))
	if err != nil {
		return fmt.Errorf("failed to write MPD: %w", err)
	}
	slog.Debug("Updated MPD", "chDir", ch.dir)
	return nil
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
	defer fh.Close()
	_, err = mpd.Write(fh, "  ", true)
	if err != nil {
		return fmt.Errorf("failed to write MPD: %w", err)
	}
	return nil
}
