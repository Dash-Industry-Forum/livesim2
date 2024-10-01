package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSegDataBuffer(t *testing.T) {
	b := newSegDataBuffer(3)
	si := recSegData{seqNr: 11, dts: 1000, dur: 0, totSize: 25, isMissing: false, isSlate: false}
	err := b.add(si)
	assert.NoError(t, err)
	b.setLatestDur(1000)
	si.seqNr = 12
	si.dts = 2000
	si.dur = 900
	err = b.add(si)
	assert.NoError(t, err)
	assert.Equal(t, 2, int(b.nrItems()))
	item11, ok := b.getItem(11)
	assert.True(t, ok)
	assert.Equal(t, recSegData{seqNr: 11, dts: 1000, dur: 1000, totSize: 25}, item11)
	si.seqNr = 13
	si.dts = 2900
	si.dur = 1000
	err = b.add(si)
	assert.NoError(t, err)
	si.seqNr = 14
	si.dts = 3900
	err = b.add(si)
	assert.NoError(t, err)
	assert.Equal(t, 3, int(b.nrItems()))
	_, ok = b.getItem(11)
	assert.False(t, ok)
	si.seqNr = 16 // Jumping over 15
	err = b.add(si)
	assert.NoError(t, err)
	assert.Equal(t, 2, int(b.nrItems()))
	err = b.add(si) // Repeated seqNr
	assert.Error(t, err)
	assert.Equal(t, 2, int(b.nrItems()))
}
