// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strings"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
)

// The inbandps_1 URL option makes AVC and HEVC video carry its parameter sets in-band. The
// avc1 and hvc1 sample entries become avc3 and hev1, which allow parameter sets in the
// samples (ISO/IEC 14496-15), the codecs strings change likewise, and the parameter sets of
// the sample entry are added in front of the slices of each sync sample. The sample entry
// keeps them too, which avc3 and hev1 allow. All MPDs of an asset share its Representations,
// so this makes avc3 and hev1 versions of the content without copies of it.

// inbandSampleEntries maps the sample entries with out-of-band parameter sets to those with in-band.
var inbandSampleEntries = map[string]string{"avc1": "avc3", "hvc1": "hev1"}

// inbandCodecs returns codecs, a comma-separated list, with avc1 and hvc1 replaced by avc3 and hev1.
func inbandCodecs(codecs string) string {
	parts := strings.Split(codecs, ",")
	for i, c := range parts {
		for from, to := range inbandSampleEntries {
			if c == from || strings.HasPrefix(c, from+".") {
				parts[i] = to + c[len(from):]
			}
		}
	}
	return strings.Join(parts, ",")
}

// inbandInit returns the init segment of rep for cfg: with inbandps_1, the avc1 or hvc1 sample
// entry of a video Representation is renamed avc3 or hev1, and otherwise init is returned as is.
func inbandInit(cfg *ResponseConfig, rep *RepData, init []byte) ([]byte, error) {
	if !cfg.InbandPS || rep.ContentType != "video" || rep.PreEncrypted {
		return init, nil
	}
	out, err := setInbandSampleEntry(init)
	if err != nil {
		return nil, fmt.Errorf("inbandps: %w", err)
	}
	return out, nil
}

// visualSampleEntryHeaderSize is the size of the fields of a VisualSampleEntry before its child boxes.
const visualSampleEntryHeaderSize = 78

// setInbandSampleEntry returns a copy of an init segment with its avc1 or hvc1 sample entry renamed
// avc3 or hev1, or, for an encrypted encv sample entry, its original format in frma. The boxes are
// otherwise unchanged, so they are not decoded and encoded again. Other sample entries are kept.
func setInbandSampleEntry(init []byte) ([]byte, error) {
	out := slices.Clone(init)
	stsd, err := boxPayload(out, "moov", "trak", "mdia", "minf", "stbl", "stsd")
	if err != nil {
		return nil, err
	}
	// The stsd payload has version and flags, the entry count, and then the sample entries.
	if len(stsd) < 16 {
		return nil, fmt.Errorf("stsd box too short")
	}
	entry := stsd[8:]
	entrySize := int(binary.BigEndian.Uint32(entry))
	if entrySize > len(entry) || entrySize < 8+visualSampleEntryHeaderSize {
		return nil, fmt.Errorf("bad sample entry size %d", entrySize)
	}
	format := entry[4:8]
	if string(format) == "encv" {
		children := entry[8+visualSampleEntryHeaderSize : entrySize]
		format, err = boxPayload(children, "sinf", "frma")
		if err != nil {
			return nil, err
		}
		if len(format) < 4 {
			return nil, fmt.Errorf("frma box too short")
		}
		format = format[:4]
	}
	if to, ok := inbandSampleEntries[string(format)]; ok {
		copy(format, to)
	}
	return out, nil
}

// boxPayload returns the payload of the box at path in data, a sequence of boxes,
// as a subslice of data.
func boxPayload(data []byte, path ...string) ([]byte, error) {
	for _, name := range path {
		found := false
		for pos := 0; pos+8 <= len(data); {
			size := int(binary.BigEndian.Uint32(data[pos:]))
			if size < 8 || pos+size > len(data) {
				return nil, fmt.Errorf("bad box size %d looking for %s", size, name)
			}
			if string(data[pos+4:pos+8]) == name {
				data = data[pos+8 : pos+size]
				found = true
				break
			}
			pos += size
		}
		if !found {
			return nil, fmt.Errorf("no %s box", name)
		}
	}
	return data, nil
}

// parameterSet is a parameter set NAL unit, with its 4-byte length, and its NAL unit type.
type parameterSet struct {
	naluType int
	data     []byte
}

// inbandParameterSets returns the parameter sets of the avc1 or hvc1 sample entry of init in the
// order that they come in an access unit, and whether they are HEVC. It returns no parameter sets
// for other sample entries.
func inbandParameterSets(init *mp4.InitSegment) (sets []parameterSet, isHEVC bool, err error) {
	stsd := init.Moov.Trak.Mdia.Minf.Stbl.Stsd
	add := func(naluType int, nalus [][]byte) {
		for _, nalu := range nalus {
			data := binary.BigEndian.AppendUint32(nil, uint32(len(nalu)))
			sets = append(sets, parameterSet{naluType: naluType, data: append(data, nalu...)})
		}
	}
	switch {
	case stsd.AvcX != nil && stsd.AvcX.Type() == "avc1" && stsd.AvcX.AvcC != nil:
		// mp4ff only decodes avcC with a 4-byte NAL unit length.
		add(int(avc.NALU_SPS), stsd.AvcX.AvcC.SPSnalus)
		add(int(avc.NALU_PPS), stsd.AvcX.AvcC.PPSnalus)
	case stsd.HvcX != nil && stsd.HvcX.Type() == "hvc1" && stsd.HvcX.HvcC != nil:
		hvcC := stsd.HvcX.HvcC
		if hvcC.LengthSizeMinusOne != 3 {
			return nil, false, fmt.Errorf("NAL unit length size %d, not 4", hvcC.LengthSizeMinusOne+1)
		}
		isHEVC = true
		for _, naluType := range []hevc.NaluType{hevc.NALU_VPS, hevc.NALU_SPS, hevc.NALU_PPS} {
			add(int(naluType), hvcC.GetNalusForType(naluType))
		}
	}
	return sets, isHEVC, nil
}

// addInbandParameterSets adds the parameter sets of the sample entry of rep in front of the slices
// of each AVC IDR or HEVC IRAP picture of seg, except those of a type that the sample already has.
// An access unit delimiter stays first. The pictures are found by their NAL units, since the sample
// flags of some content do not mark them. Since encryption leaves non-VCL NAL units in the clear, it
// must come after this, as for CTA-608 SEI.
func addInbandParameterSets(seg *mp4.MediaSegment, rep *RepData) error {
	if rep.initSeg == nil || rep.initSeg.Moov == nil || rep.initSeg.Moov.Mvex == nil {
		return fmt.Errorf("inbandps: missing init/trex for representation %q", rep.ID)
	}
	sets, isHEVC, err := inbandParameterSets(rep.initSeg)
	if err != nil {
		return fmt.Errorf("inbandps: %w", err)
	}
	if len(sets) == 0 {
		return nil
	}
	trex := rep.initSeg.Moov.Mvex.Trex
	var samples []mp4.FullSample // reused for all fragments
	for _, frag := range seg.Fragments {
		samples, err = frag.AppendFullSamples(samples[:0], trex)
		if err != nil {
			return fmt.Errorf("inbandps: appendFullSamples: %w", err)
		}
		prefixes := make([][]byte, len(samples))
		size, added := 0, 0
		for i, s := range samples {
			size += len(s.Data)
			prefixes[i] = missingParameterSets(s.Data, sets, isHEVC)
			added += len(prefixes[i])
		}
		if added == 0 {
			continue
		}
		payload := make([]byte, 0, size+added)
		for i, s := range samples {
			start := len(payload)
			audSize := 0
			if len(prefixes[i]) > 0 {
				audSize = firstAUDSize(s.Data, isHEVC)
			}
			payload = append(payload, s.Data[:audSize]...)
			payload = append(payload, prefixes[i]...)
			payload = append(payload, s.Data[audSize:]...)
			samples[i].Size = uint32(len(payload) - start)
		}
		if err := writeBackSamples(frag, samples, payload); err != nil {
			return fmt.Errorf("inbandps: %w", err)
		}
	}
	// The fragments grow; writeLiveSegment updates the sidx sizes (updateSidx).
	return nil
}

// missingParameterSets returns the parameter sets to add in front of the slices of an AVC or HEVC
// sample with 4-byte NAL unit lengths: none if it is not an IDR or IRAP picture, and otherwise
// those of a type that it does not have.
func missingParameterSets(sample []byte, sets []parameterSet, isHEVC bool) []byte {
	present := make(map[int]bool)
	if isHEVC {
		if !hevc.IsRAPSample(sample) {
			return nil
		}
		for _, nt := range hevc.FindNaluTypes(sample) {
			present[int(nt)] = true
		}
	} else {
		if !avc.IsIDRSample(sample) {
			return nil
		}
		for _, nt := range avc.FindNaluTypes(sample) {
			present[int(nt)] = true
		}
	}
	var prefix []byte
	for _, ps := range sets {
		if !present[ps.naluType] {
			prefix = append(prefix, ps.data...)
		}
	}
	return prefix
}

// firstAUDSize returns the size, with its length field, of an access unit delimiter that starts
// a sample with 4-byte NAL unit lengths, or 0 if it does not start with one.
func firstAUDSize(sample []byte, isHEVC bool) int {
	if len(sample) < 5 {
		return 0
	}
	size := 4 + int(binary.BigEndian.Uint32(sample))
	if size > len(sample) {
		return 0
	}
	if isHEVC && hevc.GetNaluType(sample[4]) == hevc.NALU_AUD || !isHEVC && avc.GetNaluType(sample[4]) == avc.NALU_AUD {
		return size
	}
	return 0
}
