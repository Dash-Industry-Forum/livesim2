package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateAudioSegRecipe(t *testing.T) {
	rd := RepData{ConstantSampleDuration: Ptr(uint32(1024)), MediaTimescale: 48000}
	cases := []struct {
		desc              string
		refNr             uint32
		refStart          uint64
		refEnd            uint64
		refTotDur         uint64
		refTimescale      uint64
		audioRep          RepData
		wantedAudioRecipe audioRecipe
	}{
		{
			"first 6s segment with 12 total duration",
			0, 0, 6000, 12000, 1000, rd,
			audioRecipe{&rd, 0, 0, 288768, 0, 288768, 0},
		},
		{
			"second 6s segment with 12 total duration",
			1, 6000, 12000, 12000, 1000, rd,
			audioRecipe{&rd, 1, 288768, 576512, 288768, 576512, 0},
		},
		{
			"third 6s segment with 12 total duration",
			2, 12000, 18000, 12000, 1000, rd,
			audioRecipe{&rd, 2, 576512, 864256, 0, 287744, 0},
		},
		{
			"fourth 6s with 12 total duration",
			3, 18000, 24000, 12000, 1000, rd,
			audioRecipe{&rd, 3, 864256, 1152000, 287744, 575488, 0},
		},
		{
			"seg 0, 750ms segment, 3s loop",
			0, 0, 750, 3000, 1000, rd,
			audioRecipe{&rd, 0, 0, 36864, 0, 36864, 0},
		},
		{
			"seg 3, 750ms segment, 3s loop",
			3, 2250, 3000, 3000, 1000, rd,
			audioRecipe{&rd, 3, 108544, 144384, 108544, 144384, 0},
		},
		{
			"seg 3, 2002ms segments, 8008ms loop",
			3, 6006, 8008, 8008, 1000, rd,
			audioRecipe{&rd, 3, 288768, 385024, 288768, 385024, 0},
		},
	}

	for _, c := range cases {
		gotRecipe := calcAudioSegRecipe(c.refNr, c.refStart, c.refEnd, c.refTotDur, c.refTimescale, &c.audioRep)
		assert.Equal(t, c.wantedAudioRecipe, gotRecipe, "recipeMismatch %s", c.desc)
	}
}
