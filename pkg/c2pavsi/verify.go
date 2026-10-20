// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package c2pavsi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/veraison/go-cose"
)

// Emsg holds the fields of a VSI emsg box.
type Emsg struct {
	Timescale             uint32
	PresentationTimeDelta uint32
	EventDuration         uint32
	ID                    uint32
	MessageData           []byte
}

// FindEmsg returns the first version 0 VSI emsg box in seg, or nil if there is none.
func FindEmsg(seg []byte) (*Emsg, error) {
	boxes, err := topLevelBoxes(seg)
	if err != nil {
		return nil, err
	}
	for _, b := range boxes {
		if e := parseVSIEmsg(b); e != nil {
			return e, nil
		}
	}
	return nil, nil
}

// parseVSIEmsg returns b as a version 0 VSI emsg box, or nil if it isn't one.
func parseVSIEmsg(b box) *Emsg {
	if b.boxType != "emsg" || len(b.data) < 12 || b.data[8] != 0 {
		return nil
	}
	rest := b.data[12:]
	scheme, rest, ok := cutCString(rest)
	if !ok || scheme != SchemeIDURI {
		return nil
	}
	value, rest, ok := cutCString(rest)
	if !ok || value != Value || len(rest) < 16 {
		return nil
	}
	return &Emsg{
		Timescale:             binary.BigEndian.Uint32(rest[0:]),
		PresentationTimeDelta: binary.BigEndian.Uint32(rest[4:]),
		EventDuration:         binary.BigEndian.Uint32(rest[8:]),
		ID:                    binary.BigEndian.Uint32(rest[12:]),
		MessageData:           rest[16:],
	}
}

func cutCString(b []byte) (string, []byte, bool) {
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil, false
	}
	return string(b[:i]), b[i+1:], true
}

// Verify checks seg's VSI emsg box against the session public key pub and returns the
// verified segment-info-map. It checks the COSE_Sign1 signature, that the algorithm is
// EdDSA, that the bmffHash excludes the VSI emsg box (with the NUL-terminated or, during the
// transition, the legacy unterminated value), that it excludes no other box, and that the
// hash matches seg. Checks that need stream state (sequence order, key validity, the
// manifest ID) are the caller's.
func Verify(seg []byte, pub ed25519.PublicKey) (*SegmentInfo, error) {
	emsg, err := FindEmsg(seg)
	if err != nil {
		return nil, err
	}
	if emsg == nil {
		return nil, errors.New("segment has no VSI emsg box")
	}
	var msg cose.Sign1Message
	if err := msg.UnmarshalCBOR(emsg.MessageData); err != nil {
		return nil, fmt.Errorf("decode COSE_Sign1: %w", err)
	}
	if alg, err := msg.Headers.Protected.Algorithm(); err != nil || alg != cose.AlgorithmEdDSA {
		return nil, errors.New("protected header alg must be EdDSA")
	}
	verifier, err := cose.NewVerifier(cose.AlgorithmEdDSA, pub)
	if err != nil {
		return nil, err
	}
	if err := msg.Verify(nil, verifier); err != nil {
		return nil, fmt.Errorf("VSI signature does not verify: %w", err)
	}

	var info SegmentInfo
	if err := cbor.Unmarshal(msg.Payload, &info); err != nil {
		return nil, fmt.Errorf("decode segment-info-map: %w", err)
	}
	value, ok := vsiExclusionValue(info.BMFFHash)
	if !ok {
		return nil, errors.New("bmffHash has no exclusion for the VSI emsg box")
	}
	if info.BMFFHash.Alg != "sha256" {
		return nil, fmt.Errorf("unsupported bmffHash alg %q", info.BMFFHash.Alg)
	}
	boxes, err := topLevelBoxes(seg)
	if err != nil {
		return nil, err
	}
	for _, b := range boxes {
		// A box the exclusion leaves out of the hash must be a VSI emsg box
		// (c2pa-org/specs-core#2619).
		if isExcludedEmsg(b, value) && parseVSIEmsg(b) == nil {
			return nil, errors.New("bmffHash exclusion matches an emsg box that is not a VSI emsg box")
		}
	}
	hash, err := segmentHash(seg, value)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(hash, info.BMFFHash.Hash) {
		return nil, errors.New("segment hash does not match bmffHash")
	}
	return &info, nil
}

// vsiExclusionValue returns the data match value of h's VSI emsg exclusion: the
// NUL-terminated scheme_id_uri, or the legacy value without the terminator.
func vsiExclusionValue(h BMFFHash) (string, bool) {
	for _, e := range h.Exclusions {
		if e.XPath != "/emsg" {
			continue
		}
		for _, d := range e.Data {
			v := string(d.Value)
			if d.Offset == uriOffsetInEmsg && (v == exclusionValue || v == legacyExclusionValue) {
				return v, true
			}
		}
	}
	return "", false
}
