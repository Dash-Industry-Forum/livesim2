// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package c2pavsi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/richardwooding/c2pa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/veraison/go-cose"
)

// testClaimSigner returns a claim signer with an ES256 test CA and end-entity certificate.
func testClaimSigner(t *testing.T) (*c2pa.Signer, *x509.Certificate) {
	t.Helper()
	now := time.Now()
	issue := func(tmpl, parent *x509.Certificate, pub, priv any) *x509.Certificate {
		tmpl.SerialNumber = big.NewInt(now.UnixNano())
		tmpl.NotBefore, tmpl.NotAfter = now.Add(-time.Hour), now.Add(time.Hour)
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, priv)
		require.NoError(t, err)
		cert, err := x509.ParseCertificate(der)
		require.NoError(t, err)
		return cert
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{Subject: pkix.Name{CommonName: "test CA"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	ca := issue(caTmpl, caTmpl, &caKey.PublicKey, caKey)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := issue(&x509.Certificate{Subject: pkix.Name{CommonName: "test signer"},
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}},
		ca, &key.PublicKey, caKey)
	signer, err := c2pa.NewSigner(key, []*x509.Certificate{leaf, ca})
	require.NoError(t, err)
	return signer, leaf
}

// TestSignInit signs a livesim2 init segment and checks the manifest, the session-keys
// assertion and that segments signed with the returned Signer verify.
func TestSignInit(t *testing.T) {
	claimSigner, leaf := testClaimSigner(t)
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	seed := bytes.Repeat([]byte{3}, 32)
	created := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	signed, signer, err := SignInit(context.Background(), init, InitConfig{
		Signer: claimSigner, Leaf: leaf, Seed: seed, Kid: []byte("V300"),
		CreatedAt: created, Validity: time.Hour, Title: "V300",
	})
	require.NoError(t, err)

	assert.Equal(t, init, withoutManifestBox(t, signed), "only the manifest box is added")

	// The manifest ID read from the C2PA box is the active manifest's label.
	res := c2pa.Validate(context.Background(), c2pa.BMFF, bytes.NewReader(signed))
	assert.Equal(t, signer.ManifestID(), res.ActiveManifestLabel)
	assert.Contains(t, signer.ManifestID(), "urn:c2pa:")
	codes := map[c2pa.StatusCode]bool{}
	for _, st := range res.Statuses {
		codes[st.Code] = true
	}
	assert.True(t, codes[c2pa.StatusClaimSignatureValidated])
	assert.True(t, codes[c2pa.StatusAssertionBMFFHashMatch])

	// The session-keys assertion round-trips with the expected fields.
	data, err := SessionKeysAssertion(ed25519.NewKeyFromSeed(seed), []byte("V300"), leaf.Raw, created, time.Hour)
	require.NoError(t, err)
	var sk struct {
		Keys []struct {
			Key               map[int]any     `cbor:"key"`
			MinSequenceNumber uint64          `cbor:"minSequenceNumber"`
			CreatedAt         cbor.Tag        `cbor:"createdAt"`
			ValidityPeriod    uint64          `cbor:"validityPeriod"`
			SignerBinding     cbor.RawMessage `cbor:"signerBinding"`
		} `cbor:"keys"`
	}
	require.NoError(t, cbor.Unmarshal(data, &sk))
	require.Len(t, sk.Keys, 1)
	k := sk.Keys[0]
	assert.Equal(t, []byte(signer.PublicKey()), k.Key[-2])
	assert.Equal(t, cbor.Tag{Number: 0, Content: "2026-10-16T12:00:00Z"}, k.CreatedAt)
	assert.Equal(t, uint64(3600), k.ValidityPeriod)
	assert.True(t, bytes.Contains(signed, data), "the init manifest carries this assertion")

	// signerBinding: a COSE_Sign1 by the session key over the detached CBOR byte string of
	// the claim signer's certificate.
	var binding cose.Sign1Message
	require.NoError(t, binding.UnmarshalCBOR(k.SignerBinding))
	assert.Nil(t, binding.Payload, "detached")
	binding.Payload, err = cbor.Marshal(leaf.Raw)
	require.NoError(t, err)
	verifier, err := cose.NewVerifier(cose.AlgorithmEdDSA, signer.PublicKey())
	require.NoError(t, err)
	require.NoError(t, binding.Verify(nil, verifier))

	raw := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "raw"))[0])
	timescale, seqNr, duration := trackTiming(t, init, raw)
	seg, err := sign(t, signer, raw, seqNr, timescale, duration)
	require.NoError(t, err)
	info, err := Verify(seg, signer.PublicKey())
	require.NoError(t, err)
	assert.Equal(t, signer.ManifestID(), info.ManifestID)
}

// TestActiveManifestLabel checks reading the manifest label for assets without a usable C2PA
// Manifest Store box, and the createdAt encoding of the Unix epoch.
func TestActiveManifestLabel(t *testing.T) {
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	_, err := activeManifestLabel(init)
	assert.ErrorContains(t, err, "no C2PA Manifest Store box")

	c2paBox := makeBox("uuid", append(append(bytes.Clone(c2paUUID), 0, 0, 0, 0), "manifest\x00"...))
	_, err = activeManifestLabel(append(bytes.Clone(c2paBox), make([]byte, 8)...))
	assert.Error(t, err, "truncated store")

	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	data, err := SessionKeysAssertion(key, []byte("k"), []byte{1}, time.Unix(0, 0), time.Second)
	require.NoError(t, err)
	assert.True(t, bytes.Contains(data, []byte("1970-01-01T00:00:00Z")))
}

// withoutManifestBox returns data with its top-level C2PA Manifest Store uuid boxes removed.
// For a signed init segment, that is the init segment before signing.
func withoutManifestBox(t *testing.T, data []byte) []byte {
	t.Helper()
	boxes, err := topLevelBoxes(data)
	require.NoError(t, err)
	var out []byte
	for _, b := range boxes {
		if b.boxType == "uuid" && bytes.HasPrefix(b.payload(), c2paUUID) {
			continue
		}
		out = append(out, b.data...)
	}
	return out
}
