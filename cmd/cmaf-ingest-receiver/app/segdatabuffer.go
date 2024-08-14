package app

import "fmt"

// segDataBuffer is a circular buffer for segment data.
// It only allows adding segments with consecutive numbers.
type segDataBuffer struct {
	size       int
	firstSeqNr uint32
	nrItems    int
	items      []segItem
}

type segItem struct {
	time      uint64
	dur       uint32
	isSlate   bool
	isMissing bool
	isLmsg    bool
}

const (
	initialSegBufferSize = 8
)

func newSegDataBuffer(size int) *segDataBuffer {
	return &segDataBuffer{
		size:    size,
		items:   make([]segItem, size),
		nrItems: 0,
	}
}

func (c *segDataBuffer) getItem(seqNr uint32) (segItem, bool) {
	if c.nrItems == 0 {
		return segItem{}, false
	}
	if seqNr < c.firstSeqNr {
		return segItem{}, false
	}
	if seqNr >= c.firstSeqNr+uint32(c.nrItems) {
		return segItem{}, false
	}
	return c.items[seqNr-c.firstSeqNr], true
}

func (c *segDataBuffer) add(time uint64, dur, seqNr uint32, isMissing, isSlate, isLmsg bool) error {
	if c.nrItems == 0 {
		c.firstSeqNr = seqNr
		c.items[0] = segItem{time: time, dur: dur, isMissing: isMissing, isSlate: isSlate, isLmsg: isLmsg}
		c.nrItems++
		return nil
	}
	lastSeqNr := c.firstSeqNr + uint32(c.nrItems) - 1
	if seqNr != lastSeqNr+1 {
		return fmt.Errorf("gap in sequence numbers, expected %d, got %d", lastSeqNr+1, seqNr)
	}
	if c.nrItems < c.size {
		c.items[c.nrItems] = segItem{time: time, dur: dur, isMissing: isMissing, isSlate: isSlate}
		c.nrItems++
		return nil
	}
	copy(c.items, c.items[1:])
	c.firstSeqNr++
	c.items[c.size-1] = segItem{time: time, dur: dur, isMissing: isMissing, isSlate: isSlate}
	return nil
}

func (c *segDataBuffer) setLatestDur(dur uint32) {
	if c.nrItems == 0 {
		return
	}
	c.items[c.nrItems-1].dur = dur
}

func (c *segDataBuffer) reSize(newSize int) {
	if newSize == c.size {
		return
	}
	if newSize < c.nrItems {
		shift := c.nrItems - newSize
		copy(c.items, c.items[shift:])
		c.firstSeqNr += uint32(shift)
		c.nrItems = newSize
		c.items = c.items[:newSize]
		return
	}
	newItems := make([]segItem, newSize)
	copy(newItems, c.items)
	c.items = newItems
	c.size = newSize
}

func (c *segDataBuffer) dropFirst() {
	if c.nrItems == 0 {
		return
	}
	copy(c.items, c.items[1:])
	c.firstSeqNr++
	c.nrItems--
}
