package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSegDataBuffer(t *testing.T) {
	b := newSegDataBuffer(3)
	err := b.add(1000, 0, 11, false, false, false)
	assert.NoError(t, err)
	b.setLatestDur(1000)
	err = b.add(2000, 1000, 12, false, false, false)
	assert.NoError(t, err)
	assert.Equal(t, 2, b.nrItems)
	item11, ok := b.getItem(11)
	assert.True(t, ok)
	assert.Equal(t, segItem{time: 1000, dur: 1000, isMissing: false, isSlate: false}, item11)
	err = b.add(3000, 1000, 13, false, false, false)
	assert.NoError(t, err)
	err = b.add(4000, 1000, 14, false, false, false)
	assert.NoError(t, err)
	assert.Equal(t, 3, b.nrItems)
	_, ok = b.getItem(11)
	assert.False(t, ok)
	err = b.add(6000, 1000, 16, false, false, false)
	assert.Error(t, err, "gap in sequence numbers, expected 15, got 16")
}
