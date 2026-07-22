// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"errors"
	"fmt"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/go-608/carriage"
	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// CEA708AccessibilitySchemeIdUri is the DASH Accessibility descriptor scheme for
// in-band CEA-708 closed captions (the CEA-608 sibling is
// CEA608AccessibilitySchemeIdUri). Both signal that a video track already carries
// captions.
const CEA708AccessibilitySchemeIdUri = "urn:scte:dash:cc:cea-708:2015"

// errCC608AlreadyCaptioned is returned when a timecc608 request targets media that
// already carries CEA-608/708 captions. livesim2 refuses rather than inject a second
// caption stream on top of the existing one. It maps to HTTP 400.
var errCC608AlreadyCaptioned = errors.New(
	"timecc608 rejected: the target already carries CEA-608/708 captions")

// detectCC608InSegment reports whether a video segment's samples already carry
// CEA-608 cc_data (user_data_registered_itu_t_t35 / GA94) SEI. It is used at
// asset-scan time on the stored VoD segment (before any injection), so a positive
// result means the source content genuinely has captions. Non-AVC/HEVC codecs cannot
// carry this SEI and return false.
func detectCC608InSegment(initSeg *mp4.InitSegment, segData []byte, codecs string) (bool, error) {
	codec, ok := cc608CodecFor(codecs)
	if !ok {
		return false, nil
	}
	if initSeg == nil || initSeg.Moov == nil || initSeg.Moov.Mvex == nil {
		return false, nil
	}
	file, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(segData))
	if err != nil {
		return false, fmt.Errorf("cc608 detect decode: %w", err)
	}
	trex := initSeg.Moov.Mvex.Trex
	for _, seg := range file.Segments {
		for _, frag := range seg.Fragments {
			fss, err := frag.GetFullSamples(trex)
			if err != nil {
				return false, fmt.Errorf("cc608 detect getFullSamples: %w", err)
			}
			for i := range fss {
				nalus, err := avc.GetNalusFromSample(fss[i].Data)
				if err != nil {
					return false, fmt.Errorf("cc608 detect nalus: %w", err)
				}
				// FieldPairs yields the CEA-608 field bytes carried by a sample's
				// SEI; a non-empty result means this sample carries captions.
				f1, f2, err := carriage.FieldPairs(nalus, codec)
				if err != nil {
					return false, fmt.Errorf("cc608 detect fieldPairs: %w", err)
				}
				if len(f1) > 0 || len(f2) > 0 {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// cc608AlreadyCaptioned reports whether the period the request targets already carries
// CEA-608/708 captions, via either signal #321 lists: (1) one of its video
// AdaptationSets declares a CEA-608/708 Accessibility descriptor, or (2) one of its
// video representations was detected at scan time as carrying cc_data SEI
// (RepData.HasCEA608). The check is scoped to the representations in this period, so
// timecc608 still works on a clean manifest of an asset that also exposes a captioned
// one (e.g. testpic_2s Manifest.mpd vs cea608.mpd).
func cc608AlreadyCaptioned(a *asset, period *m.Period) bool {
	for _, as := range period.AdaptationSets {
		if as.ContentType != "video" {
			continue
		}
		for _, desc := range as.Accessibilities {
			switch desc.SchemeIdUri {
			case CEA608AccessibilitySchemeIdUri, CEA708AccessibilitySchemeIdUri:
				return true
			}
		}
		for _, rep := range as.Representations {
			if rd, ok := a.Reps[rep.Id]; ok && rd.HasCEA608 {
				return true
			}
		}
	}
	return false
}
