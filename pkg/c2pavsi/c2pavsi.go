// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

// Package c2pavsi signs and verifies live media segments with C2PA Verifiable Segment Info
// (VSI), as defined in the Live Media chapter of the C2PA 2.5 specification.
//
// A VSI stream's init segment carries a C2PA manifest whose c2pa.session-keys assertion
// announces one or more Ed25519 session keys ([SignInit]). Each media segment then carries
// an emsg box (scheme_id_uri urn:c2pa:verifiable-segment-info, value fseg) whose
// message_data is a COSE_Sign1, signed with a session key, over the segment's sequence
// number, its c2pa.hash.bmff.v3 hash and the init manifest's identifier:
//
//   - [Signer.SignSegment] adds that emsg box to a segment and encodes it.
//   - [SegmentHash] computes a segment's c2pa.hash.bmff.v3 hash: SHA-256 over each top-level
//     box, in order, as its 8-byte big-endian file offset followed by its bytes, leaving out
//     the VSI emsg box.
//   - [Verify] checks a signed segment against a session public key.
//
// Segment signing is deterministic: the optional claimed-time-of-signing (iat) header is not
// written, and Ed25519 signatures depend only on the key and message, so the same segment
// always signs to the same bytes.
package c2pavsi

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/fxamacker/cbor/v2"
	"github.com/veraison/go-cose"
)

const (
	// SchemeIDURI is the emsg scheme_id_uri of a VSI emsg box.
	SchemeIDURI = "urn:c2pa:verifiable-segment-info"
	// Value is the emsg value of a VSI emsg box.
	Value = "fseg"

	// uriOffsetInEmsg is where scheme_id_uri starts in a version 0 emsg box: the 8-byte box
	// header and the 4-byte version and flags. The bmffHash exclusion matches the URI there.
	uriOffsetInEmsg = 12
	// exclusionValue is the bmffHash exclusion's data match: scheme_id_uri with its NUL
	// terminator (c2pa-org/specs-core#2542), so only emsg boxes with exactly the VSI scheme
	// match.
	exclusionValue = SchemeIDURI + "\x00"
	// legacyExclusionValue is the earlier value without the terminator, still accepted by
	// Verify during the transition. As a prefix it also matches longer schemes.
	legacyExclusionValue = SchemeIDURI
)

// DataMap is a bmff-hash-map exclusion's data match: value at offset from the box start.
type DataMap struct {
	Offset uint64 `cbor:"offset"`
	Value  []byte `cbor:"value"`
}

// ExclusionsMap is a bmff-hash-map exclusion.
type ExclusionsMap struct {
	XPath string    `cbor:"xpath"`
	Data  []DataMap `cbor:"data,omitempty"`
}

// BMFFHash is a C2PA bmff-hash-map (c2pa.hash.bmff.v3) for one segment.
type BMFFHash struct {
	Exclusions []ExclusionsMap `cbor:"exclusions"`
	Alg        string          `cbor:"alg,omitempty"`
	Hash       []byte          `cbor:"hash,omitempty"`
	Name       string          `cbor:"name,omitempty"`
}

// SegmentInfo is the CBOR segment-info-map signed in a VSI emsg box.
type SegmentInfo struct {
	SequenceNumber uint64   `cbor:"sequenceNumber"`
	BMFFHash       BMFFHash `cbor:"bmffHash"`
	ManifestID     string   `cbor:"manifestId"`
}

// vsiExclusion is the bmffHash exclusion of the VSI emsg box itself.
func vsiExclusion() ExclusionsMap {
	return ExclusionsMap{
		XPath: "/emsg",
		Data:  []DataMap{{Offset: uriOffsetInEmsg, Value: []byte(exclusionValue)}},
	}
}

// Signer signs segments of one track with one session key.
type Signer struct {
	key        ed25519.PrivateKey
	coseSigner cose.Signer
	kid        []byte
	manifestID string
	// The emsg id of a segment is seqNr*idCount + idIndex, modulo 2^32; see SetEventIDs.
	idIndex, idCount uint32
}

// NewSigner returns a Signer for the Ed25519 session key with the given 32-byte seed.
// kid is the key identifier announced in the init manifest's c2pa.session-keys assertion,
// and manifestID is that manifest's identifier (its urn:c2pa: label).
func NewSigner(seed, kid []byte, manifestID string) (*Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("session key seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	if len(kid) == 0 {
		return nil, errors.New("session key kid must not be empty")
	}
	if manifestID == "" {
		return nil, errors.New("manifest ID must not be empty")
	}
	key := ed25519.NewKeyFromSeed(seed)
	coseSigner, err := cose.NewSigner(cose.AlgorithmEdDSA, key)
	if err != nil {
		return nil, err
	}
	return &Signer{key: key, coseSigner: coseSigner, kid: kid, manifestID: manifestID, idCount: 1}, nil
}

// PublicKey returns the session public key.
func (s *Signer) PublicKey() ed25519.PublicKey {
	return s.key.Public().(ed25519.PublicKey)
}

// ManifestID returns the identifier of the init segment manifest that announces the key.
func (s *Signer) ManifestID() string {
	return s.manifestID
}

// SetEventIDs makes the emsg ids of this track differ from those of the other tracks signed
// alongside it: of count tracks, the one with index i (0 ≤ i < count) gives the segment with
// sequence number n the id n*count + i, modulo 2^32. Without it, the id is the sequence
// number, the same in every track.
//
// Players such as dash.js treat inband events with the same scheme_id_uri, value and id in
// one Period as one event, so with equal ids only one track's VSI events would reach an
// application.
func (s *Signer) SetEventIDs(index, count uint32) error {
	if index >= count {
		return fmt.Errorf("event id index %d must be below the track count %d", index, count)
	}
	s.idIndex, s.idCount = index, count
	return nil
}

// eventID is the emsg id of the segment with sequence number seqNr.
func (s *Signer) eventID(seqNr uint64) uint32 {
	return uint32(seqNr)*s.idCount + s.idIndex
}

// SignSegment adds a VSI emsg box to seg and returns the encoded, signed segment. seqNr is
// the segment's sequence number, normally its moof/mfhd sequence_number. timescale and
// duration fill the emsg box's timescale and event_duration: the track timescale and the
// segment's duration in it.
//
// The emsg box goes into the first fragment, after any emsg boxes already there. A sidx with
// one reference per fragment has its referenced sizes updated to include the new box; a
// segment with another sidx layout is rejected. seg is changed.
func (s *Signer) SignSegment(seg *mp4.MediaSegment, seqNr uint64, timescale, duration uint32) ([]byte, error) {
	if timescale == 0 {
		return nil, errors.New("timescale must not be 0")
	}
	if len(seg.Fragments) == 0 {
		return nil, errors.New("segment has no fragments")
	}
	if len(seg.Sidxs) > 1 {
		return nil, errors.New("VSI signing does not support media segments with more than one sidx box")
	}
	if seg.Sidx != nil {
		if len(seg.Sidx.SidxRefs) != len(seg.Fragments) {
			return nil, errors.New("VSI signing needs a sidx with one reference per fragment")
		}
		for _, ref := range seg.Sidx.SidxRefs {
			if ref.ReferenceType != 0 {
				return nil, errors.New("VSI signing does not support a sidx that references another sidx")
			}
		}
	}

	// The hash prefixes each box with its offset, which depends on the emsg box's size. A
	// placeholder signed over a zero hash of the same length gives the emsg its final size;
	// the real signature then overwrites the placeholder message_data in place.
	placeholder, err := s.messageData(make([]byte, sha256.Size), seqNr)
	if err != nil {
		return nil, err
	}
	seg.Fragments[0].AddEmsg(&mp4.EmsgBox{
		Version:       0,
		TimeScale:     timescale,
		EventDuration: duration,
		ID:            s.eventID(seqNr),
		SchemeIDURI:   SchemeIDURI,
		Value:         Value,
		MessageData:   placeholder,
	})
	if seg.Sidx != nil {
		// A subsegment starts at its fragment's first box, so it includes the emsg boxes.
		for i, frag := range seg.Fragments {
			seg.Sidx.SidxRefs[i].ReferencedSize = uint32(frag.Size())
		}
	}

	buf := bytes.NewBuffer(make([]byte, 0, seg.Size()))
	if err := seg.Encode(buf); err != nil {
		return nil, fmt.Errorf("encode segment: %w", err)
	}
	out := buf.Bytes()
	hash, err := SegmentHash(out)
	if err != nil {
		return nil, err
	}
	messageData, err := s.messageData(hash, seqNr)
	if err != nil {
		return nil, err
	}
	if len(messageData) != len(placeholder) {
		return nil, errors.New("signed VSI differs in size from its placeholder")
	}
	emsg, err := FindEmsg(out)
	if err != nil {
		return nil, err
	}
	if emsg == nil {
		return nil, errors.New("encoded segment has no VSI emsg box")
	}
	copy(emsg.MessageData, messageData) // emsg.MessageData is a subslice of out
	return out, nil
}

// messageData returns the COSE_Sign1 over the segment-info-map with hash and seqNr.
func (s *Signer) messageData(hash []byte, seqNr uint64) ([]byte, error) {
	payload, err := cbor.Marshal(SegmentInfo{
		SequenceNumber: seqNr,
		BMFFHash: BMFFHash{
			Exclusions: []ExclusionsMap{vsiExclusion()},
			Alg:        "sha256",
			Hash:       hash,
			Name:       "jumbf manifest",
		},
		ManifestID: s.manifestID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode segment-info-map: %w", err)
	}
	msg := cose.Sign1Message{
		Headers: cose.Headers{
			Protected:   cose.ProtectedHeader{cose.HeaderLabelAlgorithm: cose.AlgorithmEdDSA},
			Unprotected: cose.UnprotectedHeader{cose.HeaderLabelKeyID: s.kid},
		},
		Payload: payload,
	}
	// Ed25519 doesn't use the random source.
	if err := msg.Sign(rand.Reader, nil, s.coseSigner); err != nil {
		return nil, fmt.Errorf("sign VSI: %w", err)
	}
	return msg.MarshalCBOR()
}

// SegmentHash returns seg's c2pa.hash.bmff.v3 SHA-256: over each top-level box, in order, its
// 8-byte big-endian file offset followed by the box bytes, leaving out VSI emsg boxes (those
// whose bytes at offset 12 are the NUL-terminated VSI scheme_id_uri, as the bmffHash
// exclusion matches them).
func SegmentHash(seg []byte) ([]byte, error) {
	return segmentHash(seg, exclusionValue)
}

// segmentHash is SegmentHash for the exclusion data match value.
func segmentHash(seg []byte, value string) ([]byte, error) {
	boxes, err := topLevelBoxes(seg)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, b := range boxes {
		if isExcludedEmsg(b, value) {
			continue
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(b.offset)))
		h.Write(b.data)
	}
	return h.Sum(nil), nil
}

// isExcludedEmsg reports whether the bmffHash exclusion with data match value leaves b out.
func isExcludedEmsg(b box, value string) bool {
	end := uriOffsetInEmsg + len(value)
	return b.boxType == "emsg" && len(b.data) >= end &&
		string(b.data[uriOffsetInEmsg:end]) == value
}

type box struct {
	boxType string
	offset  int
	data    []byte // the whole box, header included
}

// payload returns the box's content after its header.
func (b box) payload() []byte {
	if binary.BigEndian.Uint32(b.data) == 1 {
		return b.data[16:]
	}
	return b.data[8:]
}

// topLevelBoxes splits data into its top-level boxes. Trailing bytes too short for a box
// header are an error, as is a box that runs past the end of data.
func topLevelBoxes(data []byte) ([]box, error) {
	var boxes []box
	for pos := 0; pos < len(data); {
		if len(data)-pos < 8 {
			return nil, fmt.Errorf("truncated box header at offset %d", pos)
		}
		size := uint64(binary.BigEndian.Uint32(data[pos:]))
		boxType := string(data[pos+4 : pos+8])
		minSize := uint64(8)
		switch size {
		case 0:
			size = uint64(len(data) - pos)
		case 1:
			if len(data)-pos < 16 {
				return nil, fmt.Errorf("truncated largesize at offset %d", pos)
			}
			size = binary.BigEndian.Uint64(data[pos+8:])
			minSize = 16
		}
		if size < minSize || size > uint64(len(data)-pos) {
			return nil, fmt.Errorf("box %q at offset %d has bad size %d", boxType, pos, size)
		}
		boxes = append(boxes, box{boxType: boxType, offset: pos, data: data[pos : pos+int(size)]})
		pos += int(size)
	}
	return boxes, nil
}
