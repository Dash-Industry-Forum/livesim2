// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

// Package c2pavsi signs and verifies live media segments with C2PA Verifiable Segment Info
// (VSI), as defined in the Live Media chapter of the C2PA 2.5 specification.
//
// A VSI stream's init segment carries a C2PA manifest whose c2pa.session-keys assertion
// announces one or more Ed25519 session keys. Producing that manifest needs a full C2PA
// implementation and is done elsewhere; this package handles the per-segment part:
//
//   - [SegmentHash] computes a segment's c2pa.hash.bmff.v3 hash: SHA-256 over each top-level
//     box, in order, as its 8-byte big-endian file offset followed by its bytes, leaving out
//     the VSI emsg box.
//   - [Signer.SignSegment] inserts an emsg box (scheme_id_uri urn:c2pa:verifiable-segment-info,
//     value fseg) whose message_data is a COSE_Sign1, signed with a session key, over the
//     segment's sequence number, its hash and the init manifest's identifier.
//   - [Verify] checks a signed segment against a session public key.
//
// Signing is deterministic: the optional claimed-time-of-signing (iat) header is not written,
// and Ed25519 signatures depend only on the key and message, so the same segment always
// signs to the same bytes.
package c2pavsi

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

const (
	// SchemeIDURI is the emsg scheme_id_uri of a VSI emsg box.
	SchemeIDURI = "urn:c2pa:verifiable-segment-info"
	// Value is the emsg value of a VSI emsg box.
	Value = "fseg"

	// uriOffsetInEmsg is where scheme_id_uri starts in a version 0 emsg box: the 8-byte box
	// header and the 4-byte version and flags. The bmffHash exclusion matches the URI there.
	uriOffsetInEmsg = 12
	// coseAlgEdDSA is the COSE algorithm identifier for EdDSA (RFC 9053).
	coseAlgEdDSA = -8
	// coseHeaderAlg and coseHeaderKid are COSE header labels (RFC 9052).
	coseHeaderAlg = 1
	coseHeaderKid = 4
	// coseSign1Tag is the CBOR tag of a COSE_Sign1_Tagged structure.
	coseSign1Tag = 18
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
		Data:  []DataMap{{Offset: uriOffsetInEmsg, Value: []byte(SchemeIDURI)}},
	}
}

// Signer signs segments of one track with one session key.
type Signer struct {
	key        ed25519.PrivateKey
	kid        []byte
	manifestID string
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
	return &Signer{key: ed25519.NewKeyFromSeed(seed), kid: kid, manifestID: manifestID}, nil
}

// PublicKey returns the session public key.
func (s *Signer) PublicKey() ed25519.PublicKey {
	return s.key.Public().(ed25519.PublicKey)
}

// SignSegment returns seg with a VSI emsg box inserted after any leading styp, sidx and prft
// boxes, before the first moof (ISO/IEC 23009-1 and CMAF box order). seqNr is the segment's
// sequence number, normally its moof/mfhd sequence_number. timescale and duration fill the
// emsg box's timescale and event_duration: the track timescale and the segment's duration in
// it. A single-level sidx has its first referenced_size grown to cover the emsg box. Segments
// with an ssix, more than one sidx, or a sidx referencing another sidx are rejected.
func (s *Signer) SignSegment(seg []byte, seqNr uint64, timescale, duration uint32) ([]byte, error) {
	if timescale == 0 {
		return nil, errors.New("timescale must not be 0")
	}
	insertAt, err := emsgInsertionPoint(seg)
	if err != nil {
		return nil, err
	}
	// Two passes: the hash prefixes each box with its offset in the final segment, which
	// depends on the emsg box's size. A placeholder hash of the same length gives that size.
	draftEmsg, err := s.emsgBox(make([]byte, sha256.Size), seqNr, timescale, duration)
	if err != nil {
		return nil, err
	}
	draft, err := insertBox(seg, insertAt, draftEmsg)
	if err != nil {
		return nil, err
	}
	hash, err := SegmentHash(draft)
	if err != nil {
		return nil, err
	}
	emsg, err := s.emsgBox(hash, seqNr, timescale, duration)
	if err != nil {
		return nil, err
	}
	if len(emsg) != len(draftEmsg) {
		return nil, errors.New("final VSI emsg box differs in size from its draft")
	}
	return insertBox(seg, insertAt, emsg)
}

func (s *Signer) emsgBox(hash []byte, seqNr uint64, timescale, duration uint32) ([]byte, error) {
	info := SegmentInfo{
		SequenceNumber: seqNr,
		BMFFHash: BMFFHash{
			Exclusions: []ExclusionsMap{vsiExclusion()},
			Alg:        "sha256",
			Hash:       hash,
			Name:       "jumbf manifest",
		},
		ManifestID: s.manifestID,
	}
	payload, err := cbor.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("encode segment-info-map: %w", err)
	}
	sign1, err := signCOSE(s.key, s.kid, payload)
	if err != nil {
		return nil, err
	}
	// The emsg id only needs to be unique within the session.
	return buildEmsg(timescale, 0, duration, uint32(seqNr), sign1), nil
}

func signCOSE(key ed25519.PrivateKey, kid, payload []byte) ([]byte, error) {
	protected, err := cbor.Marshal(map[int]int{coseHeaderAlg: coseAlgEdDSA})
	if err != nil {
		return nil, err
	}
	toBeSigned, err := sigStructure(protected, payload)
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(key, toBeSigned)
	return cbor.Marshal(cbor.Tag{
		Number:  coseSign1Tag,
		Content: []any{protected, map[int][]byte{coseHeaderKid: kid}, payload, signature},
	})
}

// sigStructure is the COSE Sig_structure for a COSE_Sign1 with no external AAD (RFC 9052 §4.4).
func sigStructure(protected, payload []byte) ([]byte, error) {
	return cbor.Marshal([]any{"Signature1", protected, []byte{}, payload})
}

func buildEmsg(timescale, presentationTimeDelta, duration, id uint32, messageData []byte) []byte {
	var body bytes.Buffer
	body.Write([]byte{0, 0, 0, 0}) // version 0, flags
	body.WriteString(SchemeIDURI)
	body.WriteByte(0)
	body.WriteString(Value)
	body.WriteByte(0)
	for _, v := range []uint32{timescale, presentationTimeDelta, duration, id} {
		body.Write(binary.BigEndian.AppendUint32(nil, v))
	}
	body.Write(messageData)
	box := binary.BigEndian.AppendUint32(nil, uint32(8+body.Len()))
	box = append(box, "emsg"...)
	return append(box, body.Bytes()...)
}

// SegmentHash returns seg's c2pa.hash.bmff.v3 SHA-256: over each top-level box, in order, its
// 8-byte big-endian file offset followed by the box bytes, leaving out VSI emsg boxes (those
// whose bytes at offset 12 start with the VSI scheme_id_uri, as the bmffHash exclusion
// matches them).
func SegmentHash(seg []byte) ([]byte, error) {
	boxes, err := topLevelBoxes(seg)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, b := range boxes {
		if isExcludedVSIEmsg(b) {
			continue
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(b.offset)))
		h.Write(b.data)
	}
	return h.Sum(nil), nil
}

func isExcludedVSIEmsg(b box) bool {
	end := uriOffsetInEmsg + len(SchemeIDURI)
	return b.boxType == "emsg" && len(b.data) >= end &&
		string(b.data[uriOffsetInEmsg:end]) == SchemeIDURI
}

type box struct {
	boxType string
	offset  int
	data    []byte // the whole box, header included
}

// topLevelBoxes splits seg into its top-level boxes. Trailing bytes too short for a box
// header are an error, as is a box that runs past the end of seg.
func topLevelBoxes(seg []byte) ([]box, error) {
	var boxes []box
	for pos := 0; pos < len(seg); {
		if len(seg)-pos < 8 {
			return nil, fmt.Errorf("truncated box header at offset %d", pos)
		}
		size := uint64(binary.BigEndian.Uint32(seg[pos:]))
		boxType := string(seg[pos+4 : pos+8])
		switch size {
		case 0:
			size = uint64(len(seg) - pos)
		case 1:
			if len(seg)-pos < 16 {
				return nil, fmt.Errorf("truncated largesize at offset %d", pos)
			}
			size = binary.BigEndian.Uint64(seg[pos+8:])
		}
		if size < 8 || size > uint64(len(seg)-pos) {
			return nil, fmt.Errorf("box %q at offset %d has bad size %d", boxType, pos, size)
		}
		boxes = append(boxes, box{boxType: boxType, offset: pos, data: seg[pos : pos+int(size)]})
		pos += int(size)
	}
	return boxes, nil
}

// emsgInsertionPoint returns the offset after the leading styp, sidx and prft boxes.
func emsgInsertionPoint(seg []byte) (int, error) {
	boxes, err := topLevelBoxes(seg)
	if err != nil {
		return 0, err
	}
	insertAt, sidxCount := 0, 0
	for _, b := range boxes {
		switch b.boxType {
		case "styp", "prft":
		case "sidx":
			sidxCount++
		case "ssix":
			return 0, errors.New("VSI signing does not support media segments with an ssix box")
		default:
			if sidxCount > 1 {
				return 0, errors.New("VSI signing does not support media segments with more than one sidx box")
			}
			return insertAt, nil
		}
		insertAt += len(b.data)
	}
	if sidxCount > 1 {
		return 0, errors.New("VSI signing does not support media segments with more than one sidx box")
	}
	return insertAt, nil
}

// insertBox returns seg with ins inserted at offset insertAt, growing a leading sidx's first
// reference to cover it: the inserted box sits between the sidx and the first moof, so it
// becomes part of the first subsegment (ISO/IEC 14496-12 §8.16.3).
func insertBox(seg []byte, insertAt int, ins []byte) ([]byte, error) {
	out := make([]byte, 0, len(seg)+len(ins))
	out = append(out, seg[:insertAt]...)
	out = append(out, ins...)
	out = append(out, seg[insertAt:]...)
	if err := growFirstSidxReference(out[:insertAt], uint32(len(ins))); err != nil {
		return nil, err
	}
	return out, nil
}

func growFirstSidxReference(leading []byte, by uint32) error {
	boxes, err := topLevelBoxes(leading)
	if err != nil {
		return err
	}
	for _, b := range boxes {
		if b.boxType != "sidx" {
			continue
		}
		sidx := b.data
		headerLen := 8
		if binary.BigEndian.Uint32(sidx) == 1 {
			headerLen = 16
		}
		if len(sidx) < headerLen+1 {
			return errors.New("malformed sidx box")
		}
		// FullBox header(4) + reference_ID(4) + timescale(4) + earliest_presentation_time and
		// first_offset (4+4 for version 0, 8+8 otherwise) + reserved(2).
		countAt := headerLen + 12 + 8 + 2
		if sidx[headerLen] != 0 {
			countAt = headerLen + 12 + 16 + 2
		}
		refAt := countAt + 2
		if len(sidx) < refAt+4 {
			return errors.New("malformed sidx box")
		}
		if binary.BigEndian.Uint16(sidx[countAt:]) == 0 {
			return nil
		}
		ref := binary.BigEndian.Uint32(sidx[refAt:])
		if ref&0x8000_0000 != 0 {
			return errors.New("VSI signing does not support a sidx that references another sidx")
		}
		size := uint64(ref&0x7fff_ffff) + uint64(by)
		if size > 0x7fff_ffff {
			return errors.New("sidx referenced_size overflows")
		}
		binary.BigEndian.PutUint32(sidx[refAt:], uint32(size))
		return nil
	}
	return nil
}
