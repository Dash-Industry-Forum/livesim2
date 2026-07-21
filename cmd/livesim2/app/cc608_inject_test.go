// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/go-608/cta608"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// avccSample packs NALUs into a length-prefixed (AVCC) sample.
func avccSample(nalus ...[]byte) []byte {
	var out []byte
	var l [4]byte
	for _, n := range nalus {
		binary.BigEndian.PutUint32(l[:], uint32(len(n)))
		out = append(out, l[:]...)
		out = append(out, n...)
	}
	return out
}

// cc608RowText concatenates the text of a decoded screen row.
func cc608RowText(s cta608.Screen, idx int) string {
	for _, r := range s.Rows {
		if r.Index != idx {
			continue
		}
		var text string
		for _, run := range r.Runs {
			text += run.Text
		}
		return text
	}
	return ""
}

type cc608Flip struct {
	frame        int
	row14, row15 string
}

// decodeSamples extracts the CTA-608 field pairs from each sample's NALUs and
// feeds them to a decoder, returning the on-screen changes.
func decodeSamples(t *testing.T, samples []mp4.FullSample, codec carriage.Codec) []cc608Flip {
	t.Helper()
	var dec cta608.Decoder
	var flips []cc608Flip
	for i := range samples {
		nalus, err := avc.GetNalusFromSample(samples[i].Data)
		require.NoError(t, err)
		f1, _, err := carriage.FieldPairs(nalus, codec)
		require.NoError(t, err)
		require.NoError(t, dec.Feed(f1))
		if dec.Changed() {
			flips = append(flips, cc608Flip{i, cc608RowText(dec.Screen(), 14), cc608RowText(dec.Screen(), 15)})
		}
	}
	return flips
}

func TestCC608CodecFor(t *testing.T) {
	cases := map[string]struct {
		codec carriage.Codec
		ok    bool
	}{
		"avc1.640028":      {carriage.CodecAVC, true},
		"avc3.42c01e":      {carriage.CodecAVC, true},
		"hev1.2.4.L120.90": {carriage.CodecHEVC, true},
		"hvc1.1.6.L93.90":  {carriage.CodecHEVC, true},
		"mp4a.40.2":        {0, false},
		"stpp":             {0, false},
	}
	for codecs, want := range cases {
		got, ok := cc608CodecFor(codecs)
		require.Equal(t, want.ok, ok, codecs)
		if want.ok {
			require.Equal(t, want.codec, got, codecs)
		}
	}
}

// TestSpliceSEIBeforeVCL checks the SEI lands just before the first VCL NALU,
// after any SPS/PPS.
func TestSpliceSEIBeforeVCL(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x00}       // AVC SPS (type 7)
	pps := []byte{0x68, 0xce, 0x3c}       // AVC PPS (type 8)
	idr := []byte{0x65, 0x88, 0x80, 0x00} // AVC IDR slice (type 5, VCL)
	sample := avccSample(sps, pps, idr)
	sei := []byte{0x06, 0x04, 0x02, 0xb5, 0x00} // fake SEI NALU (type 6)

	out, err := spliceSEIBeforeVCL(sample, sei, carriage.CodecAVC)
	require.NoError(t, err)
	nalus, err := avc.GetNalusFromSample(out)
	require.NoError(t, err)
	require.Len(t, nalus, 4)
	require.Equal(t, avc.NALU_SPS, avc.GetNaluType(nalus[0][0]))
	require.Equal(t, avc.NALU_PPS, avc.GetNaluType(nalus[1][0]))
	require.Equal(t, avc.NALU_SEI, avc.GetNaluType(nalus[2][0]))
	require.True(t, avc.IsVideoNaluType(avc.GetNaluType(nalus[3][0])))
}

func TestInjectCC608AVC(t *testing.T) {
	testInjectCC608(t, carriage.CodecAVC, []byte{0x65, 0x88, 0x80, 0x00})
}

func TestInjectCC608HEVC(t *testing.T) {
	// HEVC IDR_W_RADL slice: type 19 -> header byte0 = 19<<1 = 0x26, byte1 = 0x01.
	testInjectCC608(t, carriage.CodecHEVC, []byte{0x26, 0x01, 0x80, 0x00})
}

func testInjectCC608(t *testing.T, codec carriage.Codec, vclNalu []byte) {
	t.Helper()
	const fps = 30.0
	const nFrames = 60 // 2 s at 30 fps -> 2 cues
	unitStart := time.Date(2026, 7, 20, 14, 23, 44, 0, time.UTC).UnixMilli()

	samples := make([]mp4.FullSample, nFrames)
	origSize := len(avccSample(vclNalu))
	for i := range samples {
		samples[i] = mp4.FullSample{
			Sample: mp4.Sample{Size: uint32(origSize)},
			Data:   avccSample(vclNalu),
		}
	}

	require.NoError(t, injectCC608(samples, fps, unitStart, 42, codec))

	// Every sample gained an SEI NALU placed before the VCL, and Size was updated.
	for i := range samples {
		nalus, err := avc.GetNalusFromSample(samples[i].Data)
		require.NoError(t, err, "sample %d", i)
		require.Len(t, nalus, 2, "sample %d: SEI + VCL", i)
		if codec == carriage.CodecHEVC {
			require.Equal(t, hevc.NALU_SEI_PREFIX, hevc.GetNaluType(nalus[0][0]))
		} else {
			require.Equal(t, avc.NALU_SEI, avc.GetNaluType(nalus[0][0]))
		}
		require.True(t, isVCLNalu(nalus[1], codec), "sample %d VCL after SEI", i)
		require.Equal(t, uint32(len(samples[i].Data)), samples[i].Size, "sample %d Size", i)
		require.Greater(t, len(samples[i].Data), origSize, "sample %d grew", i)
	}

	// The injected captions decode to the two per-second cues with the seg number.
	flips := decodeSamples(t, samples, codec)
	require.Len(t, flips, 2, "one flip per cue")
	require.Equal(t, "14:23:44.000", flips[0].row14)
	require.Equal(t, "SEG 42", flips[0].row15)
	require.Equal(t, "14:23:45.000", flips[1].row14)
	require.Equal(t, "SEG 42", flips[1].row15)
}
