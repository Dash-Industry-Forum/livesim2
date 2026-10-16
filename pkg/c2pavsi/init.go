// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package c2pavsi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/richardwooding/c2pa"
)

// SessionKeysLabel is the label of the C2PA assertion that announces session keys.
const SessionKeysLabel = "c2pa.session-keys"

// coseKeyTypeOKP and coseCurveEd25519 are COSE_Key values for an Ed25519 key (RFC 9053).
const (
	coseKeyTypeOKP   = 1
	coseCurveEd25519 = 6
)

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
	var signed bytes.Buffer
	err = cfg.Signer.Sign(ctx, c2pa.BMFF, bytes.NewReader(init), &signed, c2pa.Manifest{
		Title: cfg.Title,
		Actions: []c2pa.Action{{
			Action:            c2pa.ActionCreated,
			DigitalSourceType: c2pa.DigitalSourceTypeDigitalCapture,
		}},
		Assertions: []c2pa.Assertion{{Label: SessionKeysLabel, Value: cbor.RawMessage(sessionKeys)}},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("sign init segment: %w", err)
	}
	// The manifest ID that media segments refer to is the active manifest's label.
	res := c2pa.Validate(ctx, c2pa.BMFF, bytes.NewReader(signed.Bytes()))
	if res.ActiveManifestLabel == "" {
		return nil, nil, errors.New("signed init segment has no active manifest")
	}
	signer, err := NewSigner(cfg.Seed, cfg.Kid, res.ActiveManifestLabel)
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
	protected, err := enc.Marshal(map[int]int{coseHeaderAlg: coseAlgEdDSA})
	if err != nil {
		return nil, err
	}
	certBstr, err := enc.Marshal(leafDER)
	if err != nil {
		return nil, err
	}
	toBeSigned, err := sigStructure(protected, certBstr)
	if err != nil {
		return nil, err
	}
	sig, err := key.Sign(nil, toBeSigned, crypto.Hash(0))
	if err != nil {
		return nil, err
	}
	return enc.Marshal(map[string]any{"keys": []any{map[string]any{
		"key": map[int]any{
			1:  coseKeyTypeOKP,
			2:  kid,
			3:  coseAlgEdDSA,
			-1: coseCurveEd25519,
			-2: []byte(key.Public().(ed25519.PublicKey)),
		},
		"minSequenceNumber": 0,
		"createdAt":         cbor.Tag{Number: 0, Content: createdAt.UTC().Format(time.RFC3339)},
		"validityPeriod":    uint64(validity / time.Second),
		// Detached payload: nil in the COSE_Sign1 array.
		"signerBinding": cbor.Tag{Number: coseSign1Tag, Content: []any{protected, map[int]any{}, nil, sig}},
	}}})
}
