package app

import "fmt"

// segDataBuffer is a circular buffer for segment data.
// It has a limited range between its first and last sequence numbers.
// Holes are allowed.
type segDataBuffer struct {
	size     uint32
	_nrItems uint32
	items    []recSegData
}

func (c *segDataBuffer) nrItems() uint32 {
	return c._nrItems
}

const (
	initialSegmentsWindow = 8
)

func newSegDataBuffer(size uint32) *segDataBuffer {
	return &segDataBuffer{
		size:     size,
		items:    make([]recSegData, size),
		_nrItems: 0,
	}
}

// add add a segment item to the buffer.
// It returns if the seqNr is not bigger than the previous biggest seqNr.
func (c *segDataBuffer) add(item recSegData) error {
	if c._nrItems == 0 {
		c.items[0] = item
		c._nrItems++
		return nil
	}
	lastSeqNr := c.items[c._nrItems-1].seqNr
	if item.seqNr <= lastSeqNr {
		return fmt.Errorf("sequence number not increasing, expected %d, got %d", lastSeqNr+1, item.seqNr)
	}
	if c._nrItems < c.size {
		c.items[c._nrItems] = item
		c._nrItems++
		return nil
	}
	nrToDiscard := uint32(0)
	for i := uint32(0); i < c.size; i++ {
		if c.items[i].seqNr <= item.seqNr-uint32(c.size) {
			nrToDiscard++
		}
	}
	copy(c.items, c.items[nrToDiscard:])
	c._nrItems -= (nrToDiscard - 1)
	c.items[c._nrItems-1] = item
	return nil
}

func (c *segDataBuffer) getItem(seqNr uint32) (recSegData, bool) {
	if c.nrItems() == 0 {
		return recSegData{}, false
	}
	for i := int(c._nrItems - 1); i >= 0; i-- {
		if c.items[i].seqNr == seqNr {
			return c.items[i], true
		}
	}
	return recSegData{}, false
}

func (c *segDataBuffer) setLatestDur(dur uint32) {
	if c._nrItems == 0 {
		return
	}
	c.items[c._nrItems-1].dur = dur
}

func (c *segDataBuffer) resize(newSize uint32) {
	if newSize == c.size {
		return
	}
	if newSize < c._nrItems {
		shift := c._nrItems - newSize
		copy(c.items, c.items[shift:])
		c._nrItems = newSize
		c.items = c.items[:newSize]
		return
	}
	newItems := make([]recSegData, newSize)
	copy(newItems, c.items)
	c.items = newItems
	c.size = newSize
}

func (c *segDataBuffer) dropSeqNr(seqNr uint32) {
	if c._nrItems == 0 {
		return
	}
	for i := 0; i < int(c._nrItems); i++ {
		if c.items[i].seqNr == seqNr {
			copy(c.items[i:], c.items[i+1:])
			c._nrItems--
			return
		}
	}
}

// Remove unshifted at start of interval and return their sequence numbers.
func (c *segDataBuffer) removeUnshifted() []uint32 {
	if c._nrItems == 0 {
		return nil
	}
	unshifted := make([]uint32, 0, c._nrItems)
	nrToDrop := uint32(0)
	for i := 0; i < int(c._nrItems); i++ {
		if c.items[i].isShifted {
			break
		}
		unshifted = append(unshifted, c.items[i].seqNr)
		nrToDrop++
	}
	if nrToDrop == 0 {
		return nil
	}
	copy(c.items, c.items[nrToDrop:])
	c._nrItems -= nrToDrop
	return unshifted
}
