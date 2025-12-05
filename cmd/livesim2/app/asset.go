// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

func newAssetMgr(vodFS fs.FS, repDataDir string, writeRepData bool, writeMissingRepData bool) *assetMgr {
	am := assetMgr{
		vodFS:               vodFS,
		assets:              make(map[string]*asset),
		repDataDir:          repDataDir,
		writeRepData:        writeRepData,
		writeMissingRepData: writeMissingRepData,
	}
	return &am
}

type assetMgr struct {
	vodFS               fs.FS
	assets              map[string]*asset // the key is the asset path
	repDataDir          string
	writeRepData        bool
	writeMissingRepData bool
}

// findAsset finds the asset by matching the uri with all assets paths.
func (am *assetMgr) findAsset(uri string) (*asset, bool) {
	for assetPath := range am.assets {
		if uri == assetPath || strings.HasPrefix(uri, assetPath+"/") {
			return am.assets[assetPath], true
		}
	}
	return nil, false
}

// addAsset adds or retrieves an asset.
func (am *assetMgr) addAsset(assetPath string) *asset {
	if ast, ok := am.assets[assetPath]; ok {
		return ast
	}
	ast := newAsset(assetPath)
	am.assets[assetPath] = ast
	return ast
}

// discoverAssets walks the file tree and finds all directories containing MPD files.
func (am *assetMgr) discoverAssets(logger *slog.Logger) error {
	err := fs.WalkDir(am.vodFS, ".", func(p string, d fs.DirEntry, err error) error {
		if path.Ext(p) == ".mpd" {
			err := am.loadAsset(logger, p)
			if err != nil {
				logger.Warn("Asset loading problem. Skipping", "asset", p, "err", err.Error())
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("searching MPDs: %w", err)
	}
	if len(am.assets) == 0 {
		return fmt.Errorf("no compatible assets found")
	}

	for aID, a := range am.assets {
		logger := logger.With("assetPath", a.AssetPath)
		err := a.consolidateAsset(logger)
		if err != nil {
			logger.Warn("Asset consolidation problem. Skipping", "error", err.Error())
			delete(am.assets, aID) // This deletion should be safe
			continue
		}
		logger.Info("Asset consolidated", "loopDurMS", a.LoopDurMS)
	}
	return nil
}

func (am *assetMgr) loadAsset(logger *slog.Logger, mpdPath string) error {
	assetPath, mpdName := path.Split(mpdPath)
	if assetPath != "" {
		assetPath = assetPath[:len(assetPath)-1]
	}
	logger = logger.With("assetPath", assetPath, "mpdName", mpdName)
	asset := am.addAsset(assetPath)
	md := internal.ReadMPDData(am.vodFS, mpdPath)

	data, err := fs.ReadFile(am.vodFS, mpdPath)
	if err != nil {
		return fmt.Errorf("read MPD: %w", err)
	}
	md.MPDStr = string(data)

	mpd, err := m.ReadFromString(md.MPDStr)
	if err != nil {
		return fmt.Errorf("MPD %q: %w", mpdPath, err)
	}

	if len(mpd.Periods) != 1 {
		return fmt.Errorf("number of periods is %d, not 1", len(mpd.Periods))
	}

	if *mpd.Type != "static" {
		return fmt.Errorf("mpd type is not static")
	}

	if len(mpd.ProgramInformation) > 0 {
		pi := mpd.ProgramInformation[0]
		if pi.Title != "" {
			md.Title = pi.Title
		}
	}
	md.Dur = mpd.MediaPresentationDuration.String()
	asset.MPDs[mpdName] = md

	fillContentTypes(assetPath, mpd.Periods[0])

	for _, as := range mpd.Periods[0].AdaptationSets {
		if as.SegmentTemplate == nil {
			return fmt.Errorf("no SegmentTemplate in adaptation set")
		}
		for _, rep := range as.Representations {
			if rep.SegmentTemplate != nil {
				return fmt.Errorf("segmentTemplate on Representation level. Only supported on AdaptationSet level")
			}
			if _, ok := asset.Reps[rep.Id]; ok {
				logger.Debug("Representation already loaded", "rep", rep.Id)
				continue
			}
			r, err := am.loadRep(logger, assetPath, as, rep)
			if err != nil {
				return fmt.Errorf("getRep: %w", err)
			}
			if len(r.Segments) == 0 {
				return fmt.Errorf("rep %s of type %s has no segments", rep.Id, r.ContentType)
			}
			asset.Reps[r.ID] = r
			avgSegDurMS := int(math.Round(float64(r.duration()*1000.0)) / float64((r.MediaTimescale * len(r.Segments))))
			if asset.SegmentDurMS == 0 || avgSegDurMS < asset.SegmentDurMS {
				asset.SegmentDurMS = avgSegDurMS
			}
			if as.ContentType == "audio" {
				if r.ConstantSampleDuration == nil || *r.ConstantSampleDuration == 0 {
					return fmt.Errorf("asset %s audio rep %s does not have (known) constant sample duration", assetPath, r.ID)
				}
			}
		}
	}
	logger.Info("Asset MPD loaded")
	return nil
}

func (am *assetMgr) loadRep(logger *slog.Logger, assetPath string, as *m.AdaptationSetType, rep *m.RepresentationType) (*RepData, error) {
	logger = logger.With("rep", rep.Id)
	rp := RepData{
		Version:      0, // Default version for RepData format
		ID:           rep.Id,
		ContentType:  string(as.ContentType),
		Codecs:       as.Codecs,
		MpdTimescale: 1,
	}
	// Try to load from JSON unless writeRepData is true (which forces regeneration)
	shouldTryLoadJSON := !am.writeRepData
	jsonLoaded := false
	if shouldTryLoadJSON {
		ok, err := rp.loadFromJSON(logger, am.vodFS, am.repDataDir, assetPath)
		if ok {
			logger.Debug("Loaded representation data from JSON")
			return &rp, err
		}
	}
	logger.Debug("Loading full representation by reading all segments")
	st := as.SegmentTemplate
	if rep.SegmentTemplate != nil {
		st = rep.SegmentTemplate
	}
	if st == nil {
		return nil, fmt.Errorf("did not find a SegmentTemplate")
	}
	if rep.Codecs != "" {
		rp.Codecs = rep.Codecs
	}
	rp.InitURI = replaceIdentifiers(rep, st.Initialization)
	rp.MediaURI = replaceIdentifiers(rep, st.Media)
	if st.Timescale != nil {
		rp.MpdTimescale = int(*st.Timescale)
	}
	err := rp.addRegExpAndInit(logger, am.vodFS, assetPath)
	if err != nil {
		return nil, fmt.Errorf("addRegExpAndInit: %w", err)
	}
	switch {
	case st.SegmentTimeline != nil && rp.typeURI() == timeURI:
		var t uint64
		nr := uint32(1)
		for _, s := range st.SegmentTimeline.S {
			if s.T != nil {
				t = *s.T
			}
			d := s.D
			seg, err := rp.readMP4Segment(am.vodFS, assetPath, t, 0)
			if err != nil {
				return nil, fmt.Errorf("readMP4Segment: %w", err)
			}
			rp.Segments = append(rp.Segments, seg)
			t += d
			for i := 0; i < s.R; i++ {
				nr++
				seg, err := rp.readMP4Segment(am.vodFS, assetPath, t, 0)
				if err != nil {
					return nil, fmt.Errorf("readMP4Segment: %w", err)
				}
				rp.Segments = append(rp.Segments, seg)
				t += d
			}
		}
	case st.SegmentTimeline != nil && rp.typeURI() == numberURI:
		return nil, fmt.Errorf("SegmentTimeline with $Number$ not yet supported")
	case rp.typeURI() == numberURI: // SegmentTemplate with Number$
		startNr := uint32(1)
		if st.StartNumber != nil {
			startNr = *st.StartNumber
		}
		endNr := startNr - 1
		if st.EndNumber != nil {
			endNr = *st.EndNumber
		}
		nr := startNr
		var seg Segment
		var err error
		var segDur uint64
		if rp.ContentType == "image" && as.SegmentTemplate.Duration != nil {
			segDur = uint64(*as.SegmentTemplate.Duration)
			rp.MediaTimescale = int(as.SegmentTemplate.GetTimescale())
		}
		for {
			// Loop until we cannot find more files
			if rp.ContentType != "image" {
				seg, err = rp.readMP4Segment(am.vodFS, assetPath, 0, nr)
			} else {
				seg, err = rp.readThumbSegment(am.vodFS, assetPath, nr, startNr, segDur)
			}
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return nil, fmt.Errorf("readSegment %w", err)
				}
				endNr = nr - 1
				break
			}
			if nr > startNr {
				rp.Segments[len(rp.Segments)-1].EndTime = seg.StartTime
			}
			rp.Segments = append(rp.Segments, seg)
			if nr == endNr { // This only happens if endNumber is set
				break
			}
			nr++
		}
		if endNr < startNr {
			return nil, fmt.Errorf("no segments read for rep %s", path.Join(assetPath, rp.MediaURI))
		}
	default:
		return nil, fmt.Errorf("unknown type of representation")
	}
	commonSampleDur := -1
segLoop:
	for _, seg := range rp.Segments {
		switch {
		case commonSampleDur < 0:
			commonSampleDur = int(seg.CommonSampleDur)
		case commonSampleDur != int(seg.CommonSampleDur):
			commonSampleDur = 0
			break segLoop
		default:
			// Equal. Just continue
		}
	}
	if commonSampleDur >= 0 {
		rp.ConstantSampleDuration = Ptr(uint32(commonSampleDur))
	}
	// Write to JSON if:
	// - writeRepData is true (always write/overwrite), OR
	// - writeMissingRepData is true AND JSON wasn't loaded (write only if missing)
	shouldWriteJSON := am.writeRepData || (am.writeMissingRepData && !jsonLoaded)
	if !shouldWriteJSON {
		return &rp, nil
	}
	err = rp.writeToJSON(logger, am.repDataDir, assetPath)
	return &rp, err
}

// loadFromJSON reads the representation data from a gzipped or plain JSON file.
func (rp *RepData) loadFromJSON(logger *slog.Logger, vodFS fs.FS, repDataDir, assetPath string) (bool, error) {
	if repDataDir == "" {
		return false, nil
	}
	repDataPath := path.Join(repDataDir, assetPath, rp.repDataName())
	gzipPath := repDataPath + ".gz"
	var data []byte
	_, err := os.Stat(gzipPath)
	if err == nil {
		fh, err := os.Open(gzipPath)
		if err != nil {
			return true, err
		}
		defer fh.Close()
		gzr, err := gzip.NewReader(fh)
		if err != nil {
			return true, err
		}
		defer gzr.Close()
		data, err = io.ReadAll(gzr)
		if err != nil {
			return true, err
		}
		logger.Info("Read gzipped repdata", "path", gzipPath)
	}
	if len(data) == 0 {
		_, err := os.Stat(repDataPath)
		if err == nil {
			data, err = os.ReadFile(repDataPath)
			if err != nil {
				return true, err
			}
			logger.Info("Read repdata", "path", repDataPath)
		}
	}
	if len(data) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(data, &rp); err != nil {
		return true, err
	}
	err = rp.addRegExpAndInit(logger, vodFS, assetPath)
	if err != nil {
		return true, fmt.Errorf("addRegExpAndInit: %w", err)
	}
	return true, nil
}

func (rp *RepData) addRegExpAndInit(logger *slog.Logger, vodFS fs.FS, assetPath string) error {
	switch {
	case strings.Contains(rp.MediaURI, "$Number$"):
		rexStr := strings.ReplaceAll(rp.MediaURI, "$Number$", `(\d+)`)
		rexStr = "^" + rexStr + "$" // Anchor to match entire string
		rp.mediaRegexp = regexp.MustCompile(rexStr)
	case strings.Contains(rp.MediaURI, "$Time$"):
		rexStr := strings.ReplaceAll(rp.MediaURI, "$Time$", `(\d+)`)
		rexStr = "^" + rexStr + "$" // Anchor to match entire string
		rp.mediaRegexp = regexp.MustCompile(rexStr)
	default:
		return fmt.Errorf("neither $Number$, nor $Time$ found in media")
	}

	if rp.ContentType != "image" {
		err := rp.readInit(logger, vodFS, assetPath)
		if err != nil {
			return err
		}
	}
	return nil
}

// writeToJSON writes the representation data to a gzipped JSON file.
func (rp *RepData) writeToJSON(logger *slog.Logger, repDataDir, assetPath string) error {
	logger = logger.With("rep", rp.ID, "assetPath", assetPath)
	if repDataDir == "" {
		return nil
	}
	data, err := json.Marshal(rp)
	if err != nil {
		return err
	}
	outDir := path.Join(repDataDir, assetPath)
	if dirDoesNotExist(outDir) {
		err := os.MkdirAll(outDir, 0755)
		if err != nil {
			return fmt.Errorf("mkdir %s: %w", outDir, err)
		}
	}
	gzipPath := path.Join(outDir, rp.repDataName()+".gz")
	fh, err := os.Create(gzipPath)
	if err != nil {
		return err
	}
	defer fh.Close()
	gzw := gzip.NewWriter(fh)
	defer gzw.Close()
	_, err = gzw.Write(data)
	if err != nil {
		return err
	}
	logger.Info("Wrote repData", "path", gzipPath)
	return nil
}

func (rp *RepData) repDataName() string {
	return fmt.Sprintf("%s_data.json", rp.ID)
}

func dirDoesNotExist(dir string) bool {
	_, err := os.Stat(dir)
	return os.IsNotExist(err)
}

// An asset is a directory with at least one MPD file
// It is also associated with a number of representations
// that are referred to in the MPD files.
type asset struct {
	AssetPath    string                      `json:"assetPath"`
	MPDs         map[string]internal.MPDData `json:"mpds"`
	SegmentDurMS int                         `json:"segmentDurMS"`
	LoopDurMS    int                         `json:"loopDurationMS"`
	Reps         map[string]*RepData         `json:"representations"`
	refRep       *RepData                    `json:"-"` // First video or audio representation
}

func newAsset(assetPath string) *asset {
	return &asset{
		AssetPath: assetPath,
		MPDs:      make(map[string]internal.MPDData),
		Reps:      make(map[string]*RepData),
	}
}

func (a *asset) getVodMPD(mpdName string) (*m.MPD, error) {
	md, ok := a.MPDs[mpdName]
	if !ok {
		return nil, fmt.Errorf("unknown mpd name")
	}
	return m.ReadFromString(md.MPDStr)
}

// lastSegInfo is info about latest generated segment. Used for publishTime in some cases.
type lastSegInfo struct {
	timescale      uint64
	startTime, dur uint64
	nr             int
}

// availabilityTime returns the availability time of the last segment given ato.
func (l lastSegInfo) availabilityTime(ato float64) float64 {
	return math.Round(float64(l.startTime+l.dur)/float64(l.timescale)) - ato
}

func calculateK(segmentDuration uint64, mediaTimescale int, chunkDurS *float64) (*uint64, error) {
	if chunkDurS == nil || *chunkDurS <= 0 {
		return nil, nil
	}
	chunkDurInTimescale := *chunkDurS * float64(mediaTimescale)
	if chunkDurInTimescale <= 0 {
		return nil, nil
	}

	// Validate that chunk duration is not greater than segment duration
	segmentDurS := float64(segmentDuration) / float64(mediaTimescale)
	if *chunkDurS > segmentDurS {
		return nil, fmt.Errorf("chunk duration %.2fs must be less than or equal to segment duration %.2fs", *chunkDurS, segmentDurS)
	}

	kVal := uint64(math.Round(float64(segmentDuration) / chunkDurInTimescale))
	if kVal > 1 {
		return &kVal, nil
	}
	return nil, nil
}

// generateTimelineEntries generates timeline entries for the given representation.
// If no segments are available, startNr and lsi.nr are set to -1.
func (a *asset) generateTimelineEntries(repID string, wt wrapTimes, atoMS int, explicitChunkDurS *float64) (segEntries, error) {
	rep := a.Reps[repID]
	segs := rep.Segments
	nrSegs := len(segs)
	se := segEntries{
		mediaTimescale: uint32(rep.MediaTimescale),
	}

	ato := uint64(atoMS * rep.MediaTimescale / 1000)

	relStartTime := uint64(wt.startRelMS * rep.MediaTimescale / 1000)
	relStartIdx := 0
	if relStartTime+ato < segs[0].EndTime {
		wt.startWraps--
		relStartIdx = nrSegs - 1
	} else {
		relStartIdx = findFirstFinishedSegIdx(segs, relStartTime+ato)
		if relStartIdx < 0 {
			wt.startWraps--
			relStartIdx = nrSegs - 1
		}
	}
	if wt.startWraps < 0 { // Cannot go before start
		relStartIdx = 0
		wt.startWraps = 0
	}

	relNowTime := uint64(wt.nowRelMS * rep.MediaTimescale / 1000)
	relNowIdx := 0
	if relNowTime+ato < segs[0].EndTime {
		wt.nowWraps--
		relNowIdx = nrSegs - 1
	} else {
		relNowIdx = findFirstFinishedSegIdx(segs, relNowTime+ato)
		if relNowIdx < 0 {
			wt.nowWraps--
			relNowIdx = nrSegs - 1
		}
	}
	if wt.nowWraps < 0 { // no segment finished yet. Return an empty list and set startNr and lsi.nr = -1
		se.startNr = -1
		se.lsi.nr = -1
		return se, nil
	}

	se.startNr = wt.startWraps*nrSegs + relStartIdx
	nowNr := wt.nowWraps*nrSegs + relNowIdx
	t := uint64(rep.duration()*wt.startWraps) + segs[relStartIdx].StartTime
	d := segs[relStartIdx].dur()

	k, err := calculateK(d, rep.MediaTimescale, explicitChunkDurS)
	if err != nil {
		return se, err
	}

	s := &m.S{T: Ptr(t), D: d, CommonSegmentSequenceAttributes: m.CommonSegmentSequenceAttributes{K: k}}
	lsi := lastSegInfo{
		timescale: uint64(rep.MediaTimescale),
		startTime: t,
		dur:       d,
		nr:        se.startNr,
	}
	se.entries = append(se.entries, s)
	for nr := se.startNr + 1; nr <= nowNr; nr++ {
		lsi.startTime += d
		relIdx := nr % nrSegs
		seg := segs[relIdx]
		if seg.dur() == d {
			s.R++
			lsi.nr = nr
			continue
		}
		d = seg.dur()
		s = &m.S{D: d, CommonSegmentSequenceAttributes: m.CommonSegmentSequenceAttributes{K: k}}
		se.entries = append(se.entries, s)
		lsi.dur = d
		lsi.nr = nr
	}
	se.lsi = lsi
	return se, nil
}

// generateTimelineEntriesFromRef generates timeline entries for the given representation given reference.
// This is based on sample duration and the type of media.
func (a *asset) generateTimelineEntriesFromRef(refSE segEntries, repID string, explicitChunkDurS *float64) (segEntries, error) {
	rep := a.Reps[repID]
	nrSegs := 0
	for _, rs := range refSE.entries {
		nrSegs += int(rs.R) + 1
	}
	se := segEntries{
		mediaTimescale: uint32(rep.MediaTimescale),
		startNr:        refSE.startNr,
		lsi:            refSE.lsi, // This is good enough since availability time should be very close
		entries:        make([]*m.S, 0, nrSegs),
	}

	if refSE.startNr < 0 {
		return se, nil
	}

	sampleDur := uint64(rep.sampleDur())
	timeScale := uint64(rep.MediaTimescale)

	refTimescale := uint64(refSE.mediaTimescale)
	refT := *refSE.entries[0].T
	nextRefT := refT
	t := calcAudioTimeFromRef(refT, refTimescale, sampleDur, timeScale)

	// Apply editListOffset adjustment to presentation time
	editListOffset := uint64(rep.EditListOffset)
	expectedTime := t // Track what the time should be without explicit T
	var s *m.S
	var k *uint64
	for _, rs := range refSE.entries {
		refD := rs.D
		for j := 0; j <= rs.R; j++ {
			nextRefT += refD
			nextT := calcAudioTimeFromRef(nextRefT, refTimescale, sampleDur, timeScale)
			d := nextT - t

			if s == nil {
				var err error
				k, err = calculateK(d, rep.MediaTimescale, explicitChunkDurS)
				if err != nil {
					return se, err
				}

				// First segment: apply editListOffset adjustment
				adjustedT := t
				adjustedD := d

				if editListOffset > 0 {
					if t >= editListOffset {
						// Normal case: shift time down by editListOffset
						adjustedT = t - editListOffset
					} else {
						// Special case: time would be negative, so clamp to 0 and shorten duration
						adjustedT = 0
						if d > editListOffset-t {
							adjustedD = d - (editListOffset - t)
						} else {
							adjustedD = 0
						}
					}
				}

				s = &m.S{T: m.Ptr(adjustedT), D: adjustedD, CommonSegmentSequenceAttributes: m.CommonSegmentSequenceAttributes{K: k}}
				se.entries = append(se.entries, s)
				expectedTime = adjustedT + adjustedD // Update expected time after first segment
			} else {
				// Subsequent segments
				if s.D != d {
					var err error
					k, err = calculateK(d, rep.MediaTimescale, explicitChunkDurS)
					if err != nil {
						return se, err
					}

					// New segment with different duration
					adjustedT := t
					if editListOffset > 0 && t >= editListOffset {
						adjustedT = t - editListOffset
					}

					// Only add explicit T if the time is not continuous
					if adjustedT == expectedTime {
						// Time is continuous, no need for explicit T
						s = &m.S{D: d, CommonSegmentSequenceAttributes: m.CommonSegmentSequenceAttributes{K: k}}
					} else {
						// Time is discontinuous, need explicit T
						s = &m.S{T: m.Ptr(adjustedT), D: d, CommonSegmentSequenceAttributes: m.CommonSegmentSequenceAttributes{K: k}}
					}
					se.entries = append(se.entries, s)
					expectedTime = adjustedT + d
				} else {
					s.R++
					expectedTime += d
				}
			}
			t = nextT
		}
	}
	return se, nil
}

func (a *asset) setReferenceRep() error {
	keys := make([]string, 0, len(a.Reps))
	for k := range a.Reps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if a.Reps[key].ContentType == "video" {
			a.refRep = a.Reps[key]
			return nil
		}
	}
	for _, key := range keys {
		if a.Reps[key].ContentType == "audio" {
			a.refRep = a.Reps[key]
			return nil
		}
	}
	return fmt.Errorf("no video or audio representation found")
}

// consolidateAsset sets up reference track and loop duration if possible
func (a *asset) consolidateAsset(logger *slog.Logger) error {
	logger = logger.With("assetPath", a.AssetPath)
	err := a.setReferenceRep()
	if err != nil {
		return fmt.Errorf("setReferenceRep: %w", err)
	}
	refRep := a.refRep
	a.LoopDurMS = 1000 * refRep.duration() / refRep.MediaTimescale
	if a.LoopDurMS*refRep.MediaTimescale != 1000*refRep.duration() {
		// This is not an integral number of milliseconds, so we should drop this asset
		return fmt.Errorf("cannot match loop duration %d for asset %s rep %s", a.LoopDurMS, a.AssetPath, refRep.ID)
	}
	badPreEncrypted := false
	for _, rep := range a.Reps {
		if rep.ContentType != refRep.ContentType && !rep.PreEncrypted {
			continue
		}
		repDurMS := 1000 * rep.duration() / rep.MediaTimescale
		if repDurMS != a.LoopDurMS {
			logger.Warn("Duration differs", "representation", rep.ID, "referenceRepresentation", refRep.ID, "refDurMS",
				a.LoopDurMS, "repDurMS", repDurMS)
			badPreEncrypted = true
		}
	}
	if badPreEncrypted {
		return fmt.Errorf("pre-encrypted representations do not all have same duration")
	}

	// Validate editListOffset consistency for audio representations
	err = a.validateEditListOffsetConsistency(logger)
	if err != nil {
		return fmt.Errorf("editListOffset validation failed: %w", err)
	}

	return nil
}

// validateEditListOffsetConsistency validates that for audio segments with editListOffset,
// the base media decode time - editListOffset maps to the MPD timestamps.
// This test fails when editListOffset is not taken into account properly.
func (a *asset) validateEditListOffsetConsistency(logger *slog.Logger) error {
	// Check audio representations with editListOffset
	for _, rep := range a.Reps {
		if rep.ContentType != "audio" || rep.EditListOffset <= 0 {
			continue
		}

		logger.Debug("Validating editListOffset consistency", "rep", rep.ID, "editListOffset", rep.EditListOffset)

		// Find the corresponding SegmentTemplate in the original MPDs
		// Check all MPDs as a representation may exist in multiple MPDs
		var segmentTemplates []*m.SegmentTemplateType
		for mpdName := range a.MPDs {
			mpd, err := a.getVodMPD(mpdName)
			if err != nil {
				continue
			}
			for _, as := range mpd.Periods[0].AdaptationSets {
				for _, r := range as.Representations {
					if r.Id == rep.ID && as.SegmentTemplate != nil && as.SegmentTemplate.SegmentTimeline != nil {
						segmentTemplates = append(segmentTemplates, as.SegmentTemplate)
					}
				}
			}
		}

		if len(segmentTemplates) == 0 {
			logger.Debug("No SegmentTimeline found for representation", "rep", rep.ID)
			continue
		}

		// Extract times from the original MPD SegmentTimeline (use first template found)
		segmentTemplate := segmentTemplates[0]
		var mpdTimes []uint64
		var t uint64
		for _, s := range segmentTemplate.SegmentTimeline.S {
			if s.T != nil {
				t = *s.T
			}
			mpdTimes = append(mpdTimes, t)
			t += s.D
			for i := 0; i < s.R; i++ {
				mpdTimes = append(mpdTimes, t)
				t += s.D
			}
		}

		editListOffset := uint64(rep.EditListOffset)

		// Compare BMDT values with MPD times, accounting for editListOffset
		for i := 0; i < len(rep.Segments) && i < len(mpdTimes) && i < 3; i++ {
			seg := rep.Segments[i]
			actualBMDT := seg.StartTime
			originalMPDTime := mpdTimes[i]

			// Calculate what the live MPD presentation time should be with editListOffset applied
			var expectedLiveMPDTime uint64
			if i == 0 && originalMPDTime < editListOffset {
				// First segment: time would be negative, so clamp to 0
				expectedLiveMPDTime = 0
			} else {
				// Normal case: shift time down by editListOffset
				expectedLiveMPDTime = originalMPDTime - editListOffset
			}

			// Key validation: Check the relationship between BMDT and MPD times
			// For segment 0, BMDT should equal original MPD time (usually 0)
			// For other segments, BMDT should equal original MPD time + editListOffset
			var expectedBMDT uint64
			if i == 0 {
				expectedBMDT = originalMPDTime // Segment 0 doesn't include editListOffset in BMDT
			} else {
				expectedBMDT = originalMPDTime + editListOffset // Other segments include editListOffset
			}

			if actualBMDT != expectedBMDT {
				return fmt.Errorf("segment %d BMDT %d does not match expected %d (original MPD time %d, editListOffset %d)",
					i, actualBMDT, expectedBMDT, originalMPDTime, editListOffset)
			}

			logger.Debug("EditListOffset validation passed", "segment", i, "BMDT", actualBMDT,
				"originalMPDTime", originalMPDTime, "editListOffset", editListOffset,
				"liveMPDTime", expectedLiveMPDTime)
		}
	}

	return nil
}

// getRefSegMeta returns the segment metadata for reference representation at nrOrTime.
func (a *asset) getRefSegMeta(nrOrTime int, cfg *ResponseConfig, nowMS int) (ref segMeta, err error) {
	switch cfg.liveMPDType() {
	case segmentNumber, timeLineNumber:
		nr := uint32(nrOrTime)
		ref, err = findSegMetaFromNr(a, a.refRep, nr, cfg, nowMS)
	case timeLineTime:
		videoTime := uint64(nrOrTime * a.refRep.MediaTimescale / SUBS_TIME_TIMESCALE)
		ref, err = findSegMetaFromTime(a, a.refRep, videoTime, cfg, nowMS)
	default:
		return ref, fmt.Errorf("unknown liveMPDtype")
	}
	if err != nil {
		return ref, fmt.Errorf("findSegMeta: %w", err)
	}
	return ref, nil
}

// findFirstFinishedSegIdx finds index of first finished segment.
// Returns -1 if none is finished
func findFirstFinishedSegIdx(segs []Segment, t uint64) int {
	unfinishedIdx := sort.Search(len(segs), func(i int) bool {
		return segs[i].EndTime > t
	})
	return unfinishedIdx - 1
}

type mediaURIType int

const (
	numberURI mediaURIType = iota
	timeURI
)

// RepData provides information about a representation
type RepData struct {
	Version                int              `json:"version"` // Version of RepData format (default: 0)
	ID                     string           `json:"id"`
	ContentType            string           `json:"contentType"`
	Codecs                 string           `json:"codecs"`
	MpdTimescale           int              `json:"mpdTimescale"`
	MediaTimescale         int              `json:"mediaTimescale"` // Used in the segments
	InitURI                string           `json:"initURI"`
	MediaURI               string           `json:"mediaURI"`
	Segments               []Segment        `json:"segments"`
	DefaultSampleDuration  uint32           `json:"defaultSampleDuration"`            // Read from trex or tfhd
	ConstantSampleDuration *uint32          `json:"constantSampleDuration,omitempty"` // Non-zero if all samples have the same duration
	EditListOffset         int64            `json:"editListOffset,omitempty"`
	PreEncrypted           bool             `json:"preEncrypted"`
	ChunkDurSSRS           *float64         `json:"chunkDurSSRS,omitempty"` // Low delay chunk duration in seconds
	mediaRegexp            *regexp.Regexp   `json:"-"`
	initSeg                *mp4.InitSegment `json:"-"`
	initBytes              []byte           `json:"-"`
	encData                *repEncData      `json:"-"`
}

type repEncData struct {
	keyID   id16   // Should be common within one AdaptationSet, but for now common for one asset
	key     id16   // Should be common within one AdaptationSet, but for now common for one asset
	iv      []byte // Can be random, but we use a constant default value at start
	initEnc map[string]initEncData
}

type initEncData struct {
	scheme  string
	pd      *mp4.InitProtectData
	init    *mp4.InitSegment
	initRaw []byte
}

var defaultIV = []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}

func (r RepData) duration() int {
	if len(r.Segments) == 0 {
		return 0
	}
	return int(r.Segments[len(r.Segments)-1].EndTime - r.Segments[0].StartTime)
}

// sampleDur returns sample duration if known or can easily be derived.
func (r RepData) sampleDur() uint32 {
	if r.DefaultSampleDuration != 0 {
		return r.DefaultSampleDuration
	}
	switch {
	case strings.HasPrefix(r.Codecs, "mp4a.40") && r.MediaTimescale == 48000:
		return 1024
	// TODO support other timescale such as 32kHz and 44.1kHz
	case (strings.HasPrefix(r.Codecs, "ac-3") || strings.HasPrefix(r.Codecs, "ec-3")) && r.MediaTimescale == 48000:
		return 1536
	default:
		return 0
	}
}

func (r RepData) findSegmentIndexFromTime(t uint64) int {
	return sort.Search(len(r.Segments), func(i int) bool {
		return r.Segments[i].StartTime >= t
	})
}

// SegmentType returns MIME type for MP4 segment.
func (r RepData) SegmentType() string {
	var segType string
	switch r.ContentType {
	case "audio":
		segType = "audio/mp4"
	case "subtitle", "text":
		segType = "application/mp4"
	case "video":
		segType = "video/mp4"
	case "image":
		segType = "image/jpeg"
	default:
		segType = "unknown_content_type"
	}
	return segType
}

func (r RepData) typeURI() mediaURIType {
	switch {
	case strings.Contains(r.MediaURI, "$Number$"):
		return numberURI
	case strings.Contains(r.MediaURI, "$Time$"):
		return timeURI
	default:
		panic("unknown type of media URI")
	}
}

func prepareForEncryption(codec string) bool {
	encryptionCodecPrefixes := []string{"avc", "hev", "hvc", "mp4", "ac-3", "ec-3", "ac-4"}
	for _, prefix := range encryptionCodecPrefixes {
		if strings.HasPrefix(codec, prefix) {
			return true
		}
	}
	return false
}

func (r *RepData) readInit(logger *slog.Logger, vodFS fs.FS, assetPath string) error {
	rawInit, err := fs.ReadFile(vodFS, path.Join(assetPath, r.InitURI))
	if err != nil {
		return fmt.Errorf("read initURI %q: %w", r.InitURI, err)
	}
	r.initSeg, err = getInitSeg(rawInit)
	if err != nil {
		return fmt.Errorf("decode init: %w", err)
	}
	editListOffset, err := getCmafElstOffset(r.initSeg)
	if err != nil {
		return fmt.Errorf("getElstOffset: %w", err)
	}
	r.EditListOffset = editListOffset
	r.initBytes, err = getInitBytes(r.initSeg)
	if err != nil {
		return fmt.Errorf("getInitBytes: %w", err)
	}

	if prepareForEncryption(r.Codecs) {
		assetName := path.Base(assetPath)
		err = r.addEncryption(logger, assetName)
		if err != nil {
			return fmt.Errorf("addEncryption: %w", err)
		}
	}

	if r.MediaTimescale != 0 {
		return nil // Already set
	}

	r.MediaTimescale = int(r.initSeg.Moov.Trak.Mdia.Mdhd.Timescale)
	trex := r.initSeg.Moov.Mvex.Trex
	r.DefaultSampleDuration = trex.DefaultSampleDuration

	return nil
}

func checkPreEncrypted(logger *slog.Logger, rawInit []byte) (bool, error) {
	initSeg, err := getInitSeg(rawInit)
	if err != nil {
		return false, fmt.Errorf("decode init: %w", err)
	}
	stsd := initSeg.Moov.Trak.Mdia.Minf.Stbl.Stsd
	for _, c := range stsd.Children {
		switch box := c.(type) {
		case *mp4.VisualSampleEntryBox:
			if box.Type() == "encv" && box.Sinf != nil && box.Sinf.Schm != nil {
				logger.Info("Video pre-encrypted", "scheme", box.Sinf.Schm.SchemeType)
				return true, nil
			}
		case *mp4.AudioSampleEntryBox:
			if box.Type() == "enca" && box.Sinf != nil && box.Sinf.Schm != nil {
				logger.Info("Audio pre-encrypted", "scheme", box.Sinf.Schm.SchemeType)
				return true, nil
			}
		}
	}
	return false, nil
}

// genEncInit generates an init segment adapted for encrypted content
func genEncInit(rawInit []byte, kid id16, iv []byte, scheme string) (*mp4.InitProtectData, *mp4.InitSegment, error) {
	initSeg, err := getInitSeg(rawInit)
	if err != nil {
		return nil, nil, fmt.Errorf("decode init: %w", err)
	}
	kidUUI, err := mp4.NewUUIDFromString(hex.EncodeToString(kid[:]))
	if err != nil {
		return nil, nil, fmt.Errorf("new uuid: %w", err)
	}
	ipd, err := mp4.InitProtect(initSeg, nil, iv, scheme, kidUUI, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("init protect %s: %w", scheme, err)
	}
	return ipd, initSeg, nil
}

func (r *RepData) addEncryption(logger *slog.Logger, assetName string) error {
	logger = logger.With("init", r.InitURI)
	// Set up the encryption data for this representation given asset
	kid := kidFromString(assetName)
	red := repEncData{
		keyID:   kid,
		key:     kidToKey(kid),
		iv:      defaultIV,
		initEnc: make(map[string]initEncData, 2),
	}

	preEncrypted, err := checkPreEncrypted(logger, r.initBytes)
	if err != nil {
		return fmt.Errorf("checkPreEncrypted: %w", err)
	}
	if preEncrypted {
		r.PreEncrypted = true
		return nil
	}

	rawInit := r.initBytes
	for _, scheme := range []string{"cbcs", "cenc"} {
		initProtect, initSeg, error := genEncInit(rawInit, red.keyID, red.iv, scheme)
		if error != nil {
			return fmt.Errorf("genEncInit: %w", error)
		}
		logger.Info("Generated init segment for encryption", "scheme", scheme)
		rawEncInit, err := getInitBytes(initSeg)
		if err != nil {
			return fmt.Errorf("getInitBytes: %w", err)
		}
		rd := initEncData{
			scheme:  scheme,
			pd:      initProtect,
			init:    initSeg,
			initRaw: rawEncInit,
		}
		red.initEnc[scheme] = rd
	}
	r.encData = &red
	return nil
}

func getInitSeg(data []byte) (*mp4.InitSegment, error) {
	sr := bits.NewFixedSliceReader(data)
	initFile, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return nil, fmt.Errorf("decode init: %w", err)
	}
	initSeg := initFile.Init
	if initSeg == nil {
		return nil, fmt.Errorf("no init segment found")
	}
	err = initSeg.TweakSingleTrakLive()
	if err != nil {
		return nil, fmt.Errorf("tweak single trak live: %w", err)
	}
	return initSeg, nil
}

func getInitBytes(initSeg *mp4.InitSegment) ([]byte, error) {
	sw := bits.NewFixedSliceWriter(int(initSeg.Size()))
	err := initSeg.EncodeSW(sw)
	if err != nil {
		return nil, fmt.Errorf("encode init: %w", err)
	}
	return sw.Bytes(), nil
}

// readMP4Segment extracts segment data and returns an error if file does not exist.
func (r *RepData) readMP4Segment(vodFS fs.FS, assetPath string, time uint64, nr uint32) (Segment, error) {
	var seg Segment
	uri := replaceTimeAndNr(r.MediaURI, time, nr)
	repPath := path.Join(assetPath, uri)

	data, err := fs.ReadFile(vodFS, repPath)
	if err != nil {
		return seg, err
	}
	sr := bits.NewFixedSliceReader(data)
	mp4Seg, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return seg, fmt.Errorf("decode %s: %w", repPath, err)
	}

	if len(mp4Seg.Segments) != 1 {
		return seg, fmt.Errorf("number of segments is %d, not 1", len(mp4Seg.Segments))
	}
	s := mp4Seg.Segments[0]

	t := s.Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
	nf := len(s.Fragments)
	lastFragTraf := s.Fragments[nf-1].Moof.Traf
	if lastFragTraf.Tfhd.HasDefaultSampleDuration() {
		r.DefaultSampleDuration = lastFragTraf.Tfhd.DefaultSampleDuration
	}
	endTime := lastFragTraf.Tfdt.BaseMediaDecodeTime() + lastFragTraf.Trun.Duration(r.DefaultSampleDuration)
	seg = Segment{StartTime: t, EndTime: endTime, Nr: nr}
	commonSampleDur, err := s.CommonSampleDuration(r.initSeg.Moov.Mvex.Trex)
	if err == nil {
		seg.CommonSampleDur = commonSampleDur
	}

	return seg, nil
}

// readThumbSegment reads a thumbnail segment, and returns an error if file does not exist.
func (r *RepData) readThumbSegment(vodFS fs.FS, assetPath string, nr, startNr uint32, dur uint64) (Segment, error) {
	var seg Segment
	uri := replaceTimeAndNr(r.MediaURI, 0, nr)
	repPath := path.Join(assetPath, uri)

	_, err := fs.Stat(vodFS, repPath)
	if err != nil {
		return seg, err
	}
	deltaNr := nr - startNr
	startTime := uint64(deltaNr) * dur
	return Segment{StartTime: startTime, EndTime: startTime + dur, Nr: nr}, nil
}

func replaceIdentifiers(r *m.RepresentationType, str string) string {
	str = strings.ReplaceAll(str, "$RepresentationID$", r.Id)
	str = strings.ReplaceAll(str, "$Bandwidth$", strconv.Itoa(int(r.Bandwidth)))
	return str
}

func replaceTimeAndNr(str string, time uint64, nr uint32) string {
	str = strings.ReplaceAll(str, "$Time$", strconv.Itoa(int(time)))
	str = strings.ReplaceAll(str, "$Number$", strconv.Itoa(int(nr)))
	return str
}

func replaceTimeOrNr(str string, val int) string {
	str = strings.ReplaceAll(str, "$Time$", strconv.Itoa(val))
	str = strings.ReplaceAll(str, "$Number$", strconv.Itoa(val))
	return str
}

type Segment struct {
	StartTime       uint64 `json:"startTime"`
	EndTime         uint64 `json:"endTime"`
	Nr              uint32 `json:"nr"`
	CommonSampleDur uint32 `json:"-"`
}

func (s Segment) dur() uint64 {
	return s.EndTime - s.StartTime
}

// getCmafElstOffset returns the offset of the elst box in the init segment.
// The offset is the mediaTime in an elst box compliant with CMAF to
// have 1 entry, segment_duration = 0, and media_rate = 1.
// Currently, edit lists only support for audio tracks.
func getCmafElstOffset(initSeg *mp4.InitSegment) (int64, error) {
	if initSeg == nil || initSeg.Moov == nil || len(initSeg.Moov.Traks) != 1 {
		return 0, fmt.Errorf("invalid init segment")
	}
	if initSeg.Moov.Traks[0].Edts == nil {
		return 0, nil
	}
	trak := initSeg.Moov.Traks[0]
	edts := trak.Edts
	if hdlrType := trak.Mdia.Hdlr.HandlerType; hdlrType != "soun" && hdlrType != "vide" {
		return 0, fmt.Errorf("found handler type %q. elst offset only supported for audio and video tracks", hdlrType)
	}
	if len(edts.Elst) != 1 {
		return 0, fmt.Errorf("expected exactly one elst entry, got %d", len(edts.Elst))
	}
	if len(edts.Elst[0].Entries) != 1 {
		return 0, fmt.Errorf("expected exactly one entry in elst, got %d", len(edts.Elst[0].Entries))
	}
	e := edts.Elst[0].Entries[0]
	if e.SegmentDuration != 0 || e.MediaRateInteger != 1 || e.MediaRateFraction != 0 {
		return 0, fmt.Errorf("invalid CMAF elst entry")
	}
	return e.MediaTime, nil
}
