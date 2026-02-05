package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/Eyevinn/dash-mpd/mpd"
)

const (
	timelineNrMPD = "manifest_timeline_nr.mpd"
)

type segmentTimelineGenerator struct {
	mu             sync.RWMutex
	segDataBuffers map[string]*segDataBuffer
	dstDir         string
	counters       *seqCounters
	latestSeqNr    uint32 // Used in segment times generation
	windowSize     uint32
	_nrTracks      uint32
	_started       bool
	_shifted       bool
}

func newSegmentTimelineGenerator(dstDir string, windowSize uint32) *segmentTimelineGenerator {
	return &segmentTimelineGenerator{
		segDataBuffers: make(map[string]*segDataBuffer),
		dstDir:         dstDir,
		counters:       newSeqCounters(windowSize),
		windowSize:     windowSize,
	}
}

func (s *segmentTimelineGenerator) addSegmentData(log *slog.Logger, item recSegData) (newSeqNr uint32, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s._shifted && !item.isShifted {
		return 0, nil
	}
	trName := item.name
	if _, ok := s.segDataBuffers[trName]; !ok {
		s.segDataBuffers[trName] = newSegDataBuffer(s.windowSize)
	}
	err = s.segDataBuffers[trName].add(item)
	if err != nil {
		return 0, err
	}
	s.counters.add(item.seqNr)
	if s._started {
		// Try generate segmentTimeline when all tracks have segments.
		log.Debug("Starting segmentTimeline generation", "nrTracks", len(s.segDataBuffers))
		newSeqNr = s.counters.newFullCounter(s._nrTracks, s.latestSeqNr)
		return newSeqNr, nil
	}
	return 0, nil
}

// resizeUnsafe is the internal version without locking.
// Must be called with the lock held.
func (s *segmentTimelineGenerator) resizeUnsafe(newWindowSize uint32) {
	for _, buf := range s.segDataBuffers {
		buf.resize(newWindowSize)
	}
	s.counters.resize(newWindowSize)
}

func (s *segmentTimelineGenerator) dropSeqNr(seqNr uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropSeqNrUnsafe(seqNr)
}

// dropSeqNrUnsafe is the internal version without locking.
func (s *segmentTimelineGenerator) dropSeqNrUnsafe(seqNr uint32) {
	for _, buf := range s.segDataBuffers {
		buf.dropSeqNr(seqNr)
	}
	s.counters.drop(seqNr)
}

func (s *segmentTimelineGenerator) start(newWindowSize uint32, isShifted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s._started = true
	s._shifted = isShifted
	s.resizeUnsafe(newWindowSize)
	s.windowSize = uint32(newWindowSize)
	if isShifted {
		for _, buf := range s.segDataBuffers {
			unshifted := buf.removeUnshifted()
			if len(unshifted) > 0 {
				for _, seqNr := range unshifted {
					s.dropSeqNrUnsafe(seqNr)
				}
			}
		}
	}
	s._nrTracks = uint32(len(s.segDataBuffers))
}

// getBufferFirstSeqNr returns the first sequence number in the buffer for the given track, if available.
// This is a thread-safe method for testing purposes.
func (sg *segmentTimelineGenerator) getBufferFirstSeqNr(trName string) (seqNr uint32, ok bool) {
	sg.mu.RLock()
	defer sg.mu.RUnlock()
	buf, exists := sg.segDataBuffers[trName]
	if !exists || buf.nrItems() == 0 {
		return 0, false
	}
	return buf.items[0].seqNr, true
}

// getBufferNrItems returns the number of items in the buffer for the given track.
// This is a thread-safe method for testing purposes.
func (sg *segmentTimelineGenerator) getBufferNrItems(trName string) uint32 {
	sg.mu.RLock()
	defer sg.mu.RUnlock()
	buf, exists := sg.segDataBuffers[trName]
	if !exists {
		return 0
	}
	return buf.nrItems()
}

// generateSegmentTimelineNrMPD generates the SegmentTimelineNr MPD for the channel and writes it to disk.
// The times are taken from the longest ending consecutive range of sequence numbers of all segments.
// Latest number is >= newLatestSeqNr depending on the highest number in the buffers.
// s.latestSeqNr is updated to the highest number used in the segment times.
func (sg *segmentTimelineGenerator) generateSegmentTimelineNrMPD(log *slog.Logger, newLatestSeqNr uint32, ch *channel, nowMS int64) error {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	firstNr, lastNr := sg.counters.fullRange(sg._nrTracks)
	if newLatestSeqNr <= sg.latestSeqNr {
		return fmt.Errorf("newLatestSeqNr %d is not bigger than latestSeqNr %d", newLatestSeqNr, sg.latestSeqNr)
	}
	if newLatestSeqNr > lastNr {
		return fmt.Errorf("newLatestSeqNr %d is bigger than highest buffer number %d", newLatestSeqNr, lastNr)
	}
	manifest := mpd.Clone(ch.mpd)
	for _, as := range manifest.Periods[0].AdaptationSets {
		err := sg.modifySegmentTemplate(as, firstNr, lastNr)
		if err != nil {
			return err
		}
	}
	sg.latestSeqNr = lastNr
	tmpFile := filepath.Join(ch.dir, timelineNrMPD+".tmp")
	ofh, err := os.Create(tmpFile)
	if err != nil {
		log.Error("Failed to create tmp file", "err", err)
		return err
	}
	_, err = manifest.Write(ofh, "  ", true)
	if err != nil {
		log.Error("Failed to write MPD", "err", err)
		finalClose(ofh)
		return err
	}
	finalClose(ofh)

	mpdFileName := filepath.Join(ch.dir, timelineNrMPD)
	err = os.Rename(tmpFile, mpdFileName)
	if err != nil {
		log.Error("Failed to rename segment times", "err", err)
	}
	endTime := int64(ch.startTime*1000 + int64(newLatestSeqNr+1)*int64(ch.masterSegDuration)*1000/int64(ch.masterTimescale))
	log.Info("Wrote MPD", "name", timelineNrMPD, "oldestNr", firstNr, "latestNr", lastNr, "nowMS", nowMS, "endTime", endTime,
		"diff", nowMS-endTime)
	return nil
}

// modifySegmentTemplate modifies the segment template to use the segment times for an adaptation set.
func (sg *segmentTimelineGenerator) modifySegmentTemplate(as *mpd.AdaptationSetType, firstNr, lastNr uint32) error {
	if as == nil || as.SegmentTemplate == nil || len(as.Representations) == 0 {
		return fmt.Errorf("adaptationSet or segmentTemplate is nil or no representations")
	}
	st := as.SegmentTemplate
	st.Duration = nil // Remove duration since we will use SegmentTimeline
	st.StartNumber = mpd.Ptr(uint32(firstNr))
	rep := as.Representations[0]
	repName := rep.Id
	sdb, ok := sg.segDataBuffers[repName]
	if !ok {
		return fmt.Errorf("no segment data buffer for representation %s", repName)
	}
	stl := mpd.SegmentTimelineType{}
	st.SegmentTimeline = &stl
	var s *mpd.S
	for seqNr := firstNr; seqNr <= lastNr; seqNr++ {
		sd, ok := sdb.getItem(seqNr)
		if !ok {
			return fmt.Errorf("no segment data for seqNr %d", seqNr)
		}
		if s == nil {
			s = &mpd.S{
				T: mpd.Ptr(uint64(sd.dts)),
				D: uint64(sd.dur),
				R: 0,
			}
			continue
		}
		if uint64(sd.dur) == s.D {
			s.R++
			continue
		}
		stl.S = append(stl.S, s)
		s = &mpd.S{
			D: uint64(sd.dur),
			R: 0,
		}
	}
	stl.S = append(stl.S, s)
	return nil
}

// seqCounter is a counter for a sequence number and counts how many tracks have the same sequence number.

type seqCounter struct {
	seqNr uint32
	count uint32
}

type seqCounters struct {
	counters    []seqCounter
	_nrCounters uint32
	windowSize  uint32
}

func newSeqCounters(windowSize uint32) *seqCounters {
	return &seqCounters{
		counters:    make([]seqCounter, windowSize),
		_nrCounters: 0,
		windowSize:  windowSize,
	}

}

func (s *seqCounters) resize(newWindowSize uint32) {
	switch {
	case newWindowSize > s.windowSize:
		newCounters := make([]seqCounter, newWindowSize)
		copy(newCounters, s.counters)
		s.counters = newCounters
	case newWindowSize < s.windowSize:
		copy(s.counters, s.counters[:newWindowSize])
		s.counters = s.counters[:newWindowSize]
	default:
		// No change
	}
	s.windowSize = newWindowSize
}

func (s *seqCounters) add(seqNr uint32) {
	if s._nrCounters == 0 {
		s.counters[0] = seqCounter{seqNr: seqNr, count: 1}
		s._nrCounters++
		return
	}
	currMaxSeqNr := s.counters[s._nrCounters-1].seqNr
	currMinSeqNr := s.minFromMax(currMaxSeqNr)
	switch {
	case seqNr < currMinSeqNr:
		return // Ignore this seqNr
	case seqNr > currMaxSeqNr:
		// New number, we may need to truncate min value
		currMinSeqNr = s.minFromMax(seqNr)
		nrToDrop := uint32(0)
		for i := 0; i < int(s._nrCounters); i++ {
			if s.counters[i].seqNr < uint32(currMinSeqNr) {
				nrToDrop++
			}
		}
		if s._nrCounters == s.windowSize {
			nrToDrop++
		}
		if nrToDrop > 0 {
			copy(s.counters, s.counters[nrToDrop:])
			s._nrCounters -= nrToDrop
		}
		s.counters[s._nrCounters] = seqCounter{seqNr: seqNr, count: 1}
		s._nrCounters++
	default: // seqNr is in the current window
		for i := 0; i < int(s._nrCounters); i++ {
			if seqNr == s.counters[i].seqNr {
				s.counters[i].count++
				return
			}
		}
		// seqNr is not in the counters, we need to insert it
		// We can insert in the middle and keep all previous counters
		for i := s._nrCounters - 1; i >= 1; i-- {
			if seqNr > s.counters[i-1].seqNr {
				if s._nrCounters < s.windowSize {
					// Shift counters i to s._nrCounters-1 to i+1 to s._nrCounters
					copy(s.counters[i+1:s._nrCounters], s.counters[i:s._nrCounters-1])
				} else {
					// Shift counters 1 to i-1 to 0 to i-2
					copy(s.counters[1:i], s.counters[:i-1])
				}
				s.counters[i-1] = seqCounter{seqNr: seqNr, count: 1}
				return
			}
		}
	}
}

// newFullCounter returns the seqNr of a new full counter if bigger than maxSeqNr.
// If not, zero is returned.
func (s *seqCounters) newFullCounter(nrTracks, maxSeqNr uint32) uint32 {
	if s._nrCounters == 0 {
		return 0
	}
	for i := int(s._nrCounters - 1); i >= 0; i-- {
		if s.counters[i].count == nrTracks && s.counters[i].seqNr > maxSeqNr {
			return s.counters[i].seqNr
		}
		if s.counters[i].seqNr <= maxSeqNr {
			return 0
		}
	}
	return 0
}

// fullRange returns the first and last seqNr of a range of full counters.
// No holes are allowed in the range, and the search is done from the end of the counters.
func (s *seqCounters) fullRange(nrTracks uint32) (first, last uint32) {
	if s._nrCounters == 0 {
		return 0, 0
	}
	lastIdx := 0
	for i := int(s._nrCounters - 1); i >= 0; i-- {
		if s.counters[i].count < nrTracks {
			if last == 0 {
				continue // Continue until we find a full counter
			}
			break // We have found the last full counter
		}
		seqNr := s.counters[i].seqNr
		if last == 0 {
			last = seqNr
			lastIdx = i
		}

		if int(seqNr) != int(last)-(lastIdx-i) {
			break
		}
		first = seqNr
	}
	return first, last
}

func (s *seqCounters) minFromMax(maxSeqNr uint32) uint32 {
	if maxSeqNr < s.windowSize {
		return 0
	}
	return maxSeqNr - s.windowSize + 1
}

func (s *seqCounters) drop(seqNr uint32) {
	for i := 0; i < int(s._nrCounters); i++ {
		if s.counters[i].seqNr == seqNr {
			if i < int(s.windowSize)-1 {
				copy(s.counters[i:], s.counters[i+1:])
			}
			s._nrCounters--
			return
		}
	}
}
