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
		if b.boxType != "emsg" || len(b.data) < 12 || b.data[8] != 0 {
			continue
		}
		rest := b.data[12:]
		scheme, rest, ok := cutCString(rest)
		if !ok || scheme != SchemeIDURI {
			continue
		}
		value, rest, ok := cutCString(rest)
		if !ok || value != Value || len(rest) < 16 {
			continue
		}
		return &Emsg{
			Timescale:             binary.BigEndian.Uint32(rest[0:]),
			PresentationTimeDelta: binary.BigEndian.Uint32(rest[4:]),
			EventDuration:         binary.BigEndian.Uint32(rest[8:]),
			ID:                    binary.BigEndian.Uint32(rest[12:]),
			MessageData:           rest[16:],
		}, nil
	}
	return nil, nil
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
// EdDSA, that the bmffHash excludes the VSI emsg box, and that the hash matches seg. Checks
// that need stream state (sequence order, key validity, the manifest ID) are the caller's.
func Verify(seg []byte, pub ed25519.PublicKey) (*SegmentInfo, error) {
	emsg, err := FindEmsg(seg)
	if err != nil {
		return nil, err
	}
	if emsg == nil {
		return nil, errors.New("segment has no VSI emsg box")
	}
	var tag cbor.RawTag
	if err := cbor.Unmarshal(emsg.MessageData, &tag); err != nil {
		return nil, fmt.Errorf("decode COSE_Sign1: %w", err)
	}
	if tag.Number != coseSign1Tag {
		return nil, fmt.Errorf("message_data has CBOR tag %d, want %d (COSE_Sign1)", tag.Number, coseSign1Tag)
	}
	var sign1 struct {
		_           struct{} `cbor:",toarray"`
		Protected   []byte
		Unprotected cbor.RawMessage
		Payload     []byte
		Signature   []byte
	}
	if err := cbor.Unmarshal(tag.Content, &sign1); err != nil {
		return nil, fmt.Errorf("decode COSE_Sign1: %w", err)
	}
	// Header labels can be integers or text (c2pa-rs writes a text "iat" label).
	var protected map[any]any
	if err := cbor.Unmarshal(sign1.Protected, &protected); err != nil {
		return nil, fmt.Errorf("decode protected header: %w", err)
	}
	if alg, ok := intLabel(protected, coseHeaderAlg).(int64); !ok || alg != coseAlgEdDSA {
		return nil, fmt.Errorf("protected header alg must be EdDSA (%d)", coseAlgEdDSA)
	}
	toBeSigned, err := sigStructure(sign1.Protected, sign1.Payload)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, toBeSigned, sign1.Signature) {
		return nil, errors.New("VSI signature does not verify")
	}

	var info SegmentInfo
	if err := cbor.Unmarshal(sign1.Payload, &info); err != nil {
		return nil, fmt.Errorf("decode segment-info-map: %w", err)
	}
	if !excludesVSIEmsg(info.BMFFHash) {
		return nil, errors.New("bmffHash has no exclusion for the VSI emsg box")
	}
	if info.BMFFHash.Alg != "sha256" {
		return nil, fmt.Errorf("unsupported bmffHash alg %q", info.BMFFHash.Alg)
	}
	hash, err := SegmentHash(seg)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(hash, info.BMFFHash.Hash) {
		return nil, errors.New("segment hash does not match bmffHash")
	}
	return &info, nil
}

// intLabel returns the value of integer label in a decoded COSE header map. CBOR integers
// decode as uint64 (non-negative) or int64 (negative); negative values are returned as int64.
func intLabel(header map[any]any, label int64) any {
	for k, v := range header {
		var key int64
		switch k := k.(type) {
		case uint64:
			key = int64(k)
		case int64:
			key = k
		default:
			continue
		}
		if key == label {
			if u, ok := v.(uint64); ok {
				return int64(u)
			}
			return v
		}
	}
	return nil
}

func excludesVSIEmsg(h BMFFHash) bool {
	for _, e := range h.Exclusions {
		if e.XPath != "/emsg" {
			continue
		}
		for _, d := range e.Data {
			if d.Offset == uriOffsetInEmsg && string(d.Value) == SchemeIDURI {
				return true
			}
		}
	}
	return false
}
