// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/rs/zerolog/log"
)

func newAssetMgr(vodFS fs.FS) *assetMgr {
	am := assetMgr{
		vodFS:  vodFS,
		assets: make(map[string]*asset),
	}
	return &am
}

type assetMgr struct {
	vodFS  fs.FS
	assets map[string]*asset
}

// findAsset finds the asset by matching the uri with all assets paths.
func (am *assetMgr) findAsset(uri string) (*asset, bool) {
	for assetPath := range am.assets {
		if strings.HasPrefix(uri, assetPath) {
			return am.assets[assetPath], true
		}
	}
	return nil, false
}

// addAssset adds or retrieves an asset.
func (am *assetMgr) addAsset(assetPath string) *asset {
	if ast, ok := am.assets[assetPath]; ok {
		return ast
	}
	ast := asset{
		AssetPath: assetPath,
		MPDs:      make(map[string]internal.MPDData),
		Reps:      make(map[string]*RepData),
	}
	am.assets[assetPath] = &ast
	return &ast
}

// discoverAssets walks the file tree and finds all directories containing MPD files.
func (am *assetMgr) discoverAssets() error {
	err := fs.WalkDir(am.vodFS, ".", func(p string, d fs.DirEntry, err error) error {
		if path.Ext(p) == ".mpd" {
			err := am.loadAsset(p)
			if err != nil {
				log.Warn().Err(err).Str("asset", p).Msg("Asset loading problem. Skipping")
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
	return nil
}

func (am *assetMgr) loadAsset(mpdPath string) error {
	assetPath, mpdName := path.Split(mpdPath)
	if assetPath != "" {
		assetPath = assetPath[:len(assetPath)-1]
	}
	asset := am.addAsset(assetPath)
	md := internal.ReadMPDData(am.vodFS, mpdPath)

	data, err := fs.ReadFile(am.vodFS, mpdPath)
	if err != nil {
		return fmt.Errorf("read MPD: %w", err)
	}
	md.MPDStr = string(data)
	asset.MPDs[mpdName] = md

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

	for _, as := range mpd.Periods[0].AdaptationSets {
		if as.SegmentTemplate == nil {
			return fmt.Errorf("no SegmentTemplate in adaptation set")
		}
		for _, rep := range as.Representations {
			if rep.SegmentTemplate != nil {
				return fmt.Errorf("segmentTemplate on Representation level. Only supported on AdaptationSet level.ß")
			}
			r, err := am.loadRep(assetPath, mpd, as, rep)
			if err != nil {
				return fmt.Errorf("getRep: %w", err)
			}
			if len(r.segments) == 0 {
				return fmt.Errorf("rep %s has no segments", rep.Id)
			}
			asset.Reps[r.ID] = r
			avgSegDurMS := (r.duration() * 1000) / (r.MediaTimescale * len(r.segments))
			if asset.SegmentDurMS == 0 || avgSegDurMS < asset.SegmentDurMS {
				asset.SegmentDurMS = avgSegDurMS
			}
		}
	}
	for _, rep := range asset.Reps {
		repDurMS := 1000 * rep.duration() / rep.MediaTimescale
		if repDurMS*rep.MediaTimescale != 1000*rep.duration() {
			log.Warn().Str("rep", rep.ID).Str("asset", mpdPath).Msg("not perfect loop")
		}
		asset.LoopDurMS = repDurMS
	}
	//TODO
	// Read init segment
	// Read all media segments and store metadata
	// Compare with MPD for segment timeline
	// Calculate loop duration
	// Finally fix
	return nil
}

func (am *assetMgr) loadRep(assetPath string, mpd *m.MPD, as *m.AdaptationSetType, rep *m.RepresentationType) (*RepData, error) {
	rp := RepData{ID: rep.Id,
		ContentType:  string(as.ContentType),
		Codecs:       as.Codecs,
		MpdTimescale: 1}
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

	rp.initURI = replaceIdentifiers(rep, st.Initialization)
	rp.mediaURI = replaceIdentifiers(rep, st.Media)
	switch {
	case strings.Contains(rp.mediaURI, "$Number$"):
		rp.typeURI = numberURI
		rexStr := strings.ReplaceAll(rp.mediaURI, "$Number$", `(\d+)`)
		rp.mediaRegexp = regexp.MustCompile(rexStr)
	case strings.Contains(rp.mediaURI, "$Time$"):
		rp.typeURI = timeURI
		rexStr := strings.ReplaceAll(rp.mediaURI, "$Time$", `(\d+)`)
		rp.mediaRegexp = regexp.MustCompile(rexStr)
	default:
		return nil, fmt.Errorf("neither $Number$, nor $Time$ found in media")
	}
	if st.Timescale != nil {
		rp.MpdTimescale = int(*st.Timescale)
	}

	data, err := fs.ReadFile(am.vodFS, path.Join(assetPath, rp.initURI))
	if err != nil {
		return nil, err
	}
	sr := bits.NewFixedSliceReader(data)
	initFile, err := mp4.DecodeFileSR(sr)
	if err != nil {
		return nil, fmt.Errorf("decode init: %w", err)
	}
	rp.initSeg = initFile.Init
	b := make([]byte, 0, rp.initSeg.Size())
	buf := bytes.NewBuffer(b)
	err = rp.initSeg.Encode(buf)
	if err != nil {
		return nil, fmt.Errorf("encode init seg: %w", err)
	}
	rp.initBytes = buf.Bytes()

	rp.MediaTimescale = int(rp.initSeg.Moov.Trak.Mdia.Mdhd.Timescale)
	trex := rp.initSeg.Moov.Mvex.Trex
	defaultSampleDuration := trex.DefaultSampleDuration

	switch {
	case st.SegmentTimeline != nil && rp.typeURI == timeURI:
		var t uint64
		nr := uint32(1)
		for _, s := range st.SegmentTimeline.S {
			if s.T != nil {
				t = *s.T
			}
			d := s.D
			uri := replaceTimeAndNr(rp.mediaURI, t, nr)
			rp.segments = append(rp.segments, segment{uri, t, t + d, nr})
			t += d
			for i := 0; i < s.R; i++ {
				nr++
				uri := replaceTimeAndNr(rp.mediaURI, t, nr)
				rp.segments = append(rp.segments, segment{uri, t, t + d, nr})
				t += d
			}
		}
	case st.SegmentTimeline != nil && rp.typeURI == numberURI:
		return nil, fmt.Errorf("SegmentTimeline with $Number$ not yet supported")
	case rp.typeURI == numberURI: // SegmentTemplate with Number$
		startNr := uint32(1)
		if st.StartNumber != nil {
			startNr = *st.StartNumber
		}
		endNr := startNr
		if st.EndNumber != nil {
			endNr = *st.EndNumber
		}
		nr := startNr
		var seg *mp4.File
		for {
			uri := replaceTimeAndNr(rp.mediaURI, 0, nr)
			repPath := path.Join(assetPath, uri)
			data, err := fs.ReadFile(am.vodFS, repPath)
			if err != nil {
				break // No more files
			}
			sr := bits.NewFixedSliceReader(data)
			seg, err = mp4.DecodeFileSR(sr)
			if err != nil {
				return nil, fmt.Errorf("decode %s: %w", repPath, err)
			}
			t := seg.Segments[0].Fragments[0].Moof.Traf.Tfdt.BaseMediaDecodeTime()
			if nr > startNr {
				rp.segments[len(rp.segments)-1].endTime = t
			}
			rp.segments = append(rp.segments, segment{uri, t, 0, nr})
			nr++

			if nr == endNr { // This only happens if endNumber is set
				break
			}
		}
		if nr == startNr {
			return nil, fmt.Errorf("no segments read for rep %s", path.Join(assetPath, rp.mediaURI))
		}
		nf := len(seg.Segments[0].Fragments)
		lastFragTraf := seg.Segments[0].Fragments[nf-1].Moof.Traf
		if lastFragTraf.Tfhd.HasDefaultSampleDuration() {
			defaultSampleDuration = lastFragTraf.Tfhd.DefaultSampleDuration
		}
		endTime := lastFragTraf.Tfdt.BaseMediaDecodeTime() + lastFragTraf.Trun.Duration(defaultSampleDuration)
		rp.segments[len(rp.segments)-1].endTime = endTime
	default:
		return nil, fmt.Errorf("unknown type of representation")
	}
	return &rp, nil
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
}

func (a *asset) getVodMPD(mpdName string) (*m.MPD, error) {
	md, ok := a.MPDs[mpdName]
	if !ok {
		return nil, fmt.Errorf("unknown mpd name")
	}
	return m.ReadFromString(md.MPDStr)
}

type lastSegInfo struct {
	timescale      uint64
	startTime, dur uint64
	nr             int
}

// availabilityTime returns the availability time of the last segment given ato.
func (l lastSegInfo) availabilityTime(ato float64) float64 {
	return math.Round(float64(l.startTime+l.dur)/float64(l.timescale)) - ato
}

func (a *asset) generateTimelineEntries(repID string, startWraps, startRelMS, nowWraps, nowRelMS, atoMS int) ([]*m.S, lastSegInfo) {
	var ss []*m.S
	rep := a.Reps[repID]
	segs := rep.segments
	nrSegs := len(segs)

	ato := uint64(atoMS * rep.MpdTimescale / 1000)

	relStartTime := uint64(startRelMS * rep.MediaTimescale / 1000)
	relStartIdx := 0
	if relStartTime+ato < segs[0].endTime {
		startWraps--
		relStartIdx = nrSegs - 1
	} else {
		relStartIdx = findFirstFinishedSegIdx(segs, relStartTime+ato)
		if relStartIdx < 0 {
			startWraps--
			relStartIdx = nrSegs - 1
		}
	}
	if startWraps < 0 { // Cannot go before start
		relStartIdx = 0
		startWraps = 0
	}

	relNowTime := uint64(nowRelMS * rep.MediaTimescale / 1000)
	relNowIdx := 0
	if relNowTime+ato < segs[0].endTime {
		nowWraps--
		relNowIdx = nrSegs - 1
	} else {
		relNowIdx = findFirstFinishedSegIdx(segs, relNowTime+ato)
		if relNowIdx < 0 {
			nowWraps--
			relNowIdx = nrSegs - 1
		}
	}
	if nowWraps < 0 { // end is before start.
		return nil, lastSegInfo{nr: -1, timescale: uint64(rep.MediaTimescale)}
	}

	startNr := startWraps*nrSegs + relStartIdx
	nowNr := nowWraps*nrSegs + relNowIdx
	t := uint64(rep.duration()*startWraps) + segs[relStartIdx].startTime
	d := segs[relStartIdx].dur()
	s := &m.S{T: Ptr(t), D: d}
	lsi := lastSegInfo{
		timescale: uint64(rep.MediaTimescale),
		startTime: t,
		dur:       d,
		nr:        startNr,
	}
	ss = append(ss, s)
	for nr := startNr + 1; nr <= nowNr; nr++ {
		lsi.startTime += d
		relIdx := nr % nrSegs
		seg := segs[relIdx]
		if seg.dur() == d {
			s.R++
			lsi.nr = nr
			continue
		}
		d = seg.dur()
		s = &m.S{D: d}
		ss = append(ss, s)
		lsi.dur = d
		lsi.nr = nr
	}
	return ss, lsi
}

// firstVideoRep returns the first (in alphabetical order) video rep if any present.
func (a *asset) firstVideoRep() (rep *RepData, ok bool) {
	keys := make([]string, 0, len(a.Reps))
	for k := range a.Reps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if a.Reps[key].ContentType == "video" {
			return a.Reps[key], true
		}
	}
	return nil, false
}

// findFirstFinishedSegIdx finds index of first finished segment.
// Returns -1 if none is finished
func findFirstFinishedSegIdx(segs []segment, t uint64) int {
	unfinishedIdx := sort.Search(len(segs), func(i int) bool {
		return segs[i].endTime > t
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
	ID             string           `json:"id"`
	ContentType    string           `json:"contentType"`
	Codecs         string           `json:"codecs"`
	MpdTimescale   int              `json:"mpdTimescale"`
	MediaTimescale int              `json:"mediaTimescale"` // Used in the segments
	initURI        string           `json:"-"`
	mediaURI       string           `json:"-"`
	typeURI        mediaURIType     `json:"-"`
	mediaRegexp    *regexp.Regexp   `json:"-"`
	initSeg        *mp4.InitSegment `json:"-"`
	initBytes      []byte           `json:"-"`
	segments       []segment        `json:"-"`
}

func (r RepData) duration() int {
	if len(r.segments) == 0 {
		return 0
	}
	return int(r.segments[len(r.segments)-1].endTime - r.segments[0].startTime)
}

func (r RepData) findSegmentIndexFromTime(t uint64) int {
	return sort.Search(len(r.segments), func(i int) bool {
		return r.segments[i].startTime >= t
	})
}

// SegmentTYpe returns MIME type for MP4 segment.
func (r RepData) SegmentType() string {
	var segType string
	switch r.ContentType {
	case "audio":
		segType = "audio/mp4"
	case "subtitle":
		segType = "application/mp4"
	default:
		segType = "video/mp4"
	}
	return segType
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

type segment struct {
	path      string
	startTime uint64
	endTime   uint64
	nr        uint32
}

func (s segment) dur() uint64 {
	return s.endTime - s.startTime
}
