package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/Dash-Industry-Forum/livesim2/pkg/cmaf"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

var mpdRegexp = regexp.MustCompile(`^\/(.*)\/[^\/]+\.mpd$`)
var streamsRegexp = regexp.MustCompile(`^\/(.*)\/Streams\((.*)(\.cmf[vatm])\)$`)
var segmentRegexp = regexp.MustCompile(`^\/((.*)\/)?([^\/]+)?\/([^\/]+)(\.cmf[vatm])$`)

type stream struct {
	asset     string
	name      string
	ext       string
	mediaType string
	assetDir  string
	repDir    string
}

func matchMPD(storagePath, path string) (assetDir string, ok bool) {
	matches := mpdRegexp.FindStringSubmatch(path)
	if len(matches) == 0 {
		return "", false
	}
	assetDir = filepath.Join(storagePath, matches[1])
	return assetDir, true
}

func findStreamMatch(storagePath, path string) (stream, bool) {
	str := stream{}
	var err error
	matches := streamsRegexp.FindStringSubmatch(path)
	if len(matches) > 0 {
		str.asset = matches[1]
		str.name = matches[2]
		str.ext = matches[3]
	} else {
		matches = segmentRegexp.FindStringSubmatch(path)
		if len(matches) > 1 {
			str.asset = matches[2]
			str.name = matches[3]
			str.ext = matches[5]
		}
	}
	if len(matches) == 0 {
		return str, false
	}
	str.mediaType, err = cmaf.ContentTypeFromCMAFExtension(str.ext)
	if err != nil {
		return str, false
	}
	str.assetDir = filepath.Join(storagePath, str.asset)
	str.repDir = filepath.Join(str.assetDir, str.name)
	slog.Info("Found stream", "stream", str)
	return str, true
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

type RepData struct {
	RepID         string
	ContentType   string
	Init          *mp4.InitSegment
	Lang          string
	TimeScale     int
	firstDTS      int
	NrSegs        uint32
	FirstSegSizeB uint32 // May need for bandwidth estimation
}

// FullStreamMgr is a manager for FullStream objects.
// The key is the asset name(oath).
type FullStreamMgr struct {
	mu                    sync.RWMutex
	timeShiftBufferDepthS int
	Streams               map[string]*FullStream
}

func NewFullStreamMgr(timeShiftBufferDepthS int) *FullStreamMgr {
	slog.Info("Creating FullStreamMgr", "timeShiftBufferDepthS", timeShiftBufferDepthS)
	return &FullStreamMgr{
		timeShiftBufferDepthS: timeShiftBufferDepthS,
		Streams:               make(map[string]*FullStream),
	}
}

func (fsm *FullStreamMgr) AddStream(assetPath string) {
	fsm.mu.Lock()
	fsm.Streams[assetPath] = NewFullStream(assetPath, fsm.timeShiftBufferDepthS)
	fsm.mu.Unlock()
}

func (fsm *FullStreamMgr) GetStream(assetPath string) (*FullStream, bool) {
	fsm.mu.RLock()
	fs, ok := fsm.Streams[assetPath]
	fsm.mu.RUnlock()
	return fs, ok
}

type FullStream struct {
	mu                    sync.RWMutex
	assetPath             string
	Reps                  map[string]*RepData
	MPD                   *m.MPD
	masterRepId           string
	masterTimescale       int
	masterSegDuration     int
	timeShiftBufferDepthS int
	maxNrBufSegs          int
}

func NewFullStream(assetPath string, timeShiftBufferDepthS int) *FullStream {
	mpd := m.NewMPD("dynamic")
	mpd.TimeShiftBufferDepth = m.Seconds2DurPtr(timeShiftBufferDepthS)
	mpd.Profiles = mpd.Profiles.AddProfile(m.PROFILE_LIVE)
	p := m.NewPeriod()
	p.Id = "P0"
	p.Start = m.Seconds2DurPtr(0)
	mpd.AppendPeriod(p)
	return &FullStream{
		assetPath:             assetPath,
		Reps:                  make(map[string]*RepData),
		MPD:                   mpd,
		timeShiftBufferDepthS: timeShiftBufferDepthS,
	}
}

// AddInitData adds representation data from the init segment of a stream to a FullStream and its MPD.
// If needed, a new adaptation set is created.
// If the MPD does not have availabilityTimeOffset set, it is
// set from the value of mvhd.CreationTime if later or equal to 1970-01-01.
func (fs *FullStream) AddInitData(stream stream, rawInitSeg []byte) error {
	sr := bits.NewFixedSliceReader(rawInitSeg)
	iSeg, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return fmt.Errorf("failed to decode init segment: %w", err)
	}
	init := iSeg.Init
	if init == nil {
		return fmt.Errorf("no moov box found in init segment")
	}
	r := &RepData{
		RepID:       stream.name,
		ContentType: stream.mediaType,
		Init:        init,
	}
	fs.addRep(r)
	moov := iSeg.Moov
	if len(moov.Traks) != 1 {
		return fmt.Errorf("expected one track, got %d", len(moov.Traks))
	}
	trak := moov.Traks[0]
	if fs.MPD.AvailabilityStartTime == "" {
		if moov.Mvhd.CreationTimeS() >= 0 {
			fs.MPD.AvailabilityStartTime = m.ConvertToDateTime(float64(moov.Mvhd.CreationTimeS()))
		}
	}

	if trak.Mdia == nil || trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil || trak.Mdia.Minf.Stbl.Stsd == nil {
		return fmt.Errorf("no mdia, minf, stbl, or stsd box not found in track")
	}
	r.TimeScale = int(trak.Mdia.Mdhd.Timescale)
	lang := getLang(trak.Mdia)
	r.Lang = lang
	stsd := trak.Mdia.Minf.Stbl.Stsd
	sampleEntry := stsd.Children[0].Type()

	switch sampleEntry {
	case "avc1", "hvc1", "mp4a", "stpp", "wvtt":
		// OK
	default:
		return fmt.Errorf("sample entry format %s not supported", sampleEntry)
	}

	p := fs.MPD.Periods[0]
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
		mimeType, err := cmaf.MimeTypeFromCMAFExtension(stream.ext)
		if err != nil {
			return err
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
	rep.Id = stream.name
	currAsSet.AppendRepresentation(rep)
	ext, err := cmaf.CMAFExtensionFromContentType(stream.mediaType)
	if err != nil {
		return err
	}
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
		vse := stsd.Children[0].(*mp4.VisualSampleEntryBox)
		rep.Width = uint32(vse.Width)
		rep.Height = uint32(vse.Height)
		switch sampleEntry {
		case "avc1":
			spsRaw := stsd.AvcX.AvcC.DecConfRec.SPSnalus[0]
			sps, err := avc.ParseSPSNALUnit(spsRaw, true)
			if err != nil {
				return fmt.Errorf("failed to parse avc1 SPS: %w", err)
			}
			rep.Codecs = avc.CodecString(sampleEntry, sps)
		case "hvc1":
			spsRaw := stsd.HvcX.HvcC.DecConfRec.GetNalusForType(hevc.NALU_SPS)[0]
			sps, err := hevc.ParseSPSNALUnit(spsRaw)
			if err != nil {
				return fmt.Errorf("failed to parse hvc1 SPS: %w", err)
			}
			rep.Codecs = hevc.CodecString(sampleEntry, sps)
		}
	case "audio":
		switch sampleEntry {
		case "mp4a":
			rep.Codecs = "mp4a.40.2"
		}
	case "text":
		switch sampleEntry {
		case "stpp":
			rep.Codecs = "stpp"
		case "wvtt":
			rep.Codecs = "wvtt"
		}
	}
	return nil
}

func (fs *FullStream) AddSegData(stream stream, seqNr uint32, dts uint64) error {
	fs.mu.RLock()
	rd, ok := fs.Reps[stream.name]
	if !ok {
		fs.mu.RUnlock()
		return fmt.Errorf("representation %s not found", stream.name)
	}
	fs.mu.RUnlock()
	if stream.name == fs.masterRepId && fs.masterSegDuration == 0 {
		switch rd.NrSegs {
		case 0:
			rd.firstDTS = int(dts)
		case 1:
			fs.masterSegDuration = int(dts) - rd.firstDTS
			fs.masterTimescale = rd.TimeScale
			err := fs.updateAndWriteMPD()
			if err != nil {
				return fmt.Errorf("failed to write MPD: %w", err)
			}
			fs.maxNrBufSegs = fs.timeShiftBufferDepthS*fs.masterTimescale/fs.masterSegDuration + 2
		default:
			// Should not happen
		}
	}
	rd.NrSegs++

	return nil
}

func (fs *FullStream) addRep(rd *RepData) {
	fs.mu.Lock()
	firstVideoRep := true
	for _, rep := range fs.Reps {
		if rep.ContentType == "video" {
			firstVideoRep = false
			break
		}
	}
	if firstVideoRep {
		fs.masterRepId = rd.RepID
	}
	fs.Reps[rd.RepID] = rd
	fs.mu.Unlock()
}

func (fs *FullStream) updateAndWriteMPD() error {
	for _, asSet := range fs.MPD.Periods[0].AdaptationSets {
		stl := asSet.SegmentTemplate
		dur := fs.masterSegDuration * int(*stl.Timescale) / fs.masterTimescale
		stl.Duration = m.Ptr(uint32(dur))
		stl.StartNumber = m.Ptr(uint32(0))
	}

	err := writeMPD(fs.MPD, filepath.Join(fs.assetPath, "manifest.mpd"))
	slog.Info("Updated MPD", "assetPath", fs.assetPath)
	if err != nil {
		return fmt.Errorf("failed to write MPD: %w", err)
	}
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
