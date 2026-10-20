// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package c2pavsi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/richardwooding/c2pa"
	"github.com/veraison/go-cose"
)

// SessionKeysLabel is the label of the C2PA assertion that announces session keys.
const SessionKeysLabel = "c2pa.session-keys"

// coseKeyTypeOKP and coseCurveEd25519 are COSE_Key values for an Ed25519 key (RFC 9053).
const (
	coseKeyTypeOKP   = 1
	coseCurveEd25519 = 6
)

// c2paUUID identifies a uuid box holding a C2PA Manifest Store (C2PA §A.5.1).
var c2paUUID = []byte{0xd8, 0xfe, 0xc3, 0xd6, 0x1b, 0x0e, 0x48, 0x3c, 0x92, 0x97, 0x58, 0x28, 0x87, 0x7e, 0xc4, 0x81}

// InitConfig configures SignInit.
type InitConfig struct {
	// Signer is the claim signer for the init segment's C2PA manifest.
	Signer *c2pa.Signer
	// Leaf is Signer's end-entity certificate, which the session key's signerBinding binds to.
	Leaf *x509.Certificate
	// Seed is the 32-byte Ed25519 seed of the session key.
	Seed []byte
	// Kid identifies the session key.
	Kid []byte
	// CreatedAt and Validity are the session key's createdAt and validityPeriod.
	CreatedAt time.Time
	Validity  time.Duration
	// Title is the manifest's dc:title.
	Title string
}

// SignInit signs an init segment with a C2PA manifest whose c2pa.session-keys assertion
// announces the session key in cfg, and returns the signed init segment and a Signer for
// media segments with that session key and the manifest's ID. The manifest is bound to the
// init segment with c2pa.hash.bmff.v3, and its box is inserted after ftyp.
func SignInit(ctx context.Context, init []byte, cfg InitConfig) ([]byte, *Signer, error) {
	if cfg.Signer == nil || cfg.Leaf == nil {
		return nil, nil, errors.New("claim signer and its certificate are required")
	}
	if cfg.Validity < time.Second {
		return nil, nil, errors.New("session key validity must be at least one second")
	}
	if len(cfg.Seed) != ed25519.SeedSize {
		return nil, nil, fmt.Errorf("session key seed must be %d bytes, got %d", ed25519.SeedSize, len(cfg.Seed))
	}
	sessionKeys, err := SessionKeysAssertion(ed25519.NewKeyFromSeed(cfg.Seed), cfg.Kid, cfg.Leaf.Raw,
		cfg.CreatedAt, cfg.Validity)
	if err != nil {
		return nil, nil, err
	}
	// Sign validates its own output, so the result needs no further check. The created
	// action needs a digitalSourceType; the signed asset is the init segment, which holds
	// codec configuration but no media samples, so it is "empty" (C2PA "Actions": an asset
	// created with no digital content). That says nothing about how the media was made,
	// which the signer can't know.
	var signed bytes.Buffer
	err = cfg.Signer.Sign(ctx, c2pa.BMFF, bytes.NewReader(init), &signed, c2pa.Manifest{
		Title: cfg.Title,
		Actions: []c2pa.Action{{
			Action:            c2pa.ActionCreated,
			DigitalSourceType: c2pa.DigitalSourceTypeEmpty,
		}},
		Assertions: []c2pa.Assertion{{Label: SessionKeysLabel, Value: cbor.RawMessage(sessionKeys)}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("sign init segment: %w", err)
	}
	manifestID, err := activeManifestLabel(signed.Bytes())
	if err != nil {
		return nil, nil, err
	}
	signer, err := NewSigner(cfg.Seed, cfg.Kid, manifestID)
	if err != nil {
		return nil, nil, err
	}
	return signed.Bytes(), signer, nil
}

// SessionKeysAssertion returns the CBOR c2pa.session-keys assertion for one Ed25519 session
// key valid from sequence number 0. Its signerBinding is a COSE_Sign1 by the session key with
// the detached payload of the claim signer's end-entity certificate as a CBOR byte string.
func SessionKeysAssertion(key ed25519.PrivateKey, kid, leafDER []byte, createdAt time.Time,
	validity time.Duration) ([]byte, error) {
	if len(kid) == 0 {
		return nil, errors.New("session key kid must not be empty")
	}
	enc, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		return nil, err
	}
	certBstr, err := enc.Marshal(leafDER)
	if err != nil {
		return nil, err
	}
	coseSigner, err := cose.NewSigner(cose.AlgorithmEdDSA, key)
	if err != nil {
		return nil, err
	}
	binding := cose.Sign1Message{
		Headers: cose.Headers{
			Protected: cose.ProtectedHeader{cose.HeaderLabelAlgorithm: cose.AlgorithmEdDSA},
		},
		Payload: certBstr,
	}
	if err := binding.Sign(rand.Reader, nil, coseSigner); err != nil {
		return nil, fmt.Errorf("sign signerBinding: %w", err)
	}
	binding.Payload = nil // detached
	bindingCBOR, err := binding.MarshalCBOR()
	if err != nil {
		return nil, err
	}
	return enc.Marshal(map[string]any{"keys": []any{map[string]any{
		"key": map[int]any{
			1:  coseKeyTypeOKP,
			2:  kid,
			3:  int64(cose.AlgorithmEdDSA),
			-1: coseCurveEd25519,
			-2: []byte(key.Public().(ed25519.PublicKey)),
		},
		"minSequenceNumber": 0,
		"createdAt":         cbor.Tag{Number: 0, Content: createdAt.UTC().Format(time.RFC3339)},
		"validityPeriod":    uint64(validity / time.Second),
		"signerBinding":     cbor.RawMessage(bindingCBOR),
	}}})
}

// activeManifestLabel returns the label of the active manifest of a signed BMFF asset: the
// last manifest superbox in its C2PA Manifest Store (C2PA §11.1.4). The store is in a uuid
// box (C2PA §A.5.1) holding, after the C2PA uuid and the FullBox version and flags, the
// NUL-terminated box purpose, for "manifest" an 8-byte Merkle offset, and the store's
// JUMBF superbox.
func activeManifestLabel(asset []byte) (string, error) {
	boxes, err := topLevelBoxes(asset)
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		p := b.payload()
		if b.boxType != "uuid" || len(p) < 20 || !bytes.Equal(p[:16], c2paUUID) {
			continue
		}
		purpose, rest, ok := cutCString(p[20:])
		if !ok {
			return "", errors.New("C2PA box has no purpose")
		}
		if purpose == "manifest" {
			if len(rest) < 8 {
				return "", errors.New("C2PA box is truncated")
			}
			rest = rest[8:]
		}
		store, err := topLevelBoxes(rest)
		if err != nil {
			return "", fmt.Errorf("C2PA Manifest Store: %w", err)
		}
		if len(store) == 0 || store[0].boxType != "jumb" {
			return "", errors.New("C2PA box holds no JUMBF superbox")
		}
		if label, err := jumbLabel(store[0]); err != nil || label != "c2pa" {
			return "", errors.New("C2PA box holds no C2PA Manifest Store")
		}
		children, err := topLevelBoxes(store[0].payload())
		if err != nil {
			return "", fmt.Errorf("C2PA Manifest Store: %w", err)
		}
		var active string
		for _, c := range children[1:] { // after the store's description box
			if c.boxType == "jumb" {
				if active, err = jumbLabel(c); err != nil {
					return "", err
				}
			}
		}
		if active == "" {
			return "", errors.New("C2PA Manifest Store has no manifest")
		}
		return active, nil
	}
	return "", errors.New("no C2PA Manifest Store box")
}

// jumbLabel returns the label of a JUMBF superbox from its description box (ISO/IEC 19566-5):
// a 16-byte content type, a toggles byte, then, when toggle 0x02 is set, the NUL-terminated
// label.
func jumbLabel(jumb box) (string, error) {
	children, err := topLevelBoxes(jumb.payload())
	if err != nil {
		return "", err
	}
	if len(children) == 0 || children[0].boxType != "jumd" {
		return "", errors.New("JUMBF superbox has no description box")
	}
	desc := children[0].payload()
	if len(desc) < 17 || desc[16]&0x02 == 0 {
		return "", errors.New("JUMBF superbox has no label")
	}
	label, _, ok := cutCString(desc[17:])
	if !ok {
		return "", errors.New("JUMBF label is not terminated")
	}
	return label, nil
}
