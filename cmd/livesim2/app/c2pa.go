// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/c2pavsi"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/richardwooding/c2pa"
)

// C2PA Verifiable Segment Info (c2pa_vsi option). Experimental.
//
// The feature is off unless the server runs with the c2pa option. Then, when livesim2 loads
// an asset, it signs the init segment of each clear audio and video representation with a
// C2PA manifest (github.com/richardwooding/c2pa) whose c2pa.session-keys assertion announces
// a new Ed25519 session key. With c2pa_vsi, livesim2 serves that init segment and signs each
// media segment with the session key.
//
// The claim-signing certificate and key come from the c2pacert and c2pakey options. Without
// them, livesim2 creates a test CA and end-entity certificate at startup, which validators
// report as untrusted.
//
// Session keys and manifest IDs are created at startup, so they change when the server
// restarts and differ between server instances. Serve a stream from a single instance, and
// have players fetch the init segment again after a restart: segments signed after a restart
// don't validate against an init segment from before it.

// errC2PADisabled is returned when c2pa_vsi is requested from a server without the c2pa option.
var errC2PADisabled = errors.New("c2pa_vsi: C2PA is not enabled on this server (c2pa option)")

// errC2PAUnavailable is returned when c2pa_vsi is requested for a representation without a
// signed init segment and session key.
var errC2PAUnavailable = errors.New(
	"c2pa_vsi: no C2PA-signed init segment and session key for this representation")

// c2paValidityAfterStart is how long after server start the session keys and the generated
// test certificate stay valid. Session keys are not rotated, so it covers a long-running
// server.
const c2paValidityAfterStart = 365 * 24 * time.Hour

// c2paKeyCreatedAt is the createdAt of the session keys: the Unix epoch, the earliest media
// time livesim2 can serve (availabilityStartTime defaults to 0, and nowMS, nowDate and
// publishTime can ask for any time since). A validator that checks the key against a
// segment's presentation time (c2pa-org/specs-core#2620) then accepts every segment,
// including those from before a restart in a time-shift buffer of up to 48 hours.
var c2paKeyCreatedAt = time.Unix(0, 0).UTC()

// c2paCredentials are the claim signer for init segments and its end-entity certificate.
type c2paCredentials struct {
	signer *c2pa.Signer
	leaf   *x509.Certificate
}

// c2paRep holds a representation's signed init segment and its session key signer.
type c2paRep struct {
	init   []byte
	signer *c2pavsi.Signer
}

// newC2PACredentials loads the claim-signing certificate chain and private key from PEM
// files, or creates test ones if both paths are empty.
func newC2PACredentials(logger *slog.Logger, certPath, keyPath string) (*c2paCredentials, error) {
	var (
		key   crypto.Signer
		chain []*x509.Certificate
		err   error
	)
	switch {
	case certPath == "" && keyPath == "":
		key, chain, err = c2paTestCredentials()
		if err != nil {
			return nil, fmt.Errorf("c2pa test credentials: %w", err)
		}
		logger.Info("C2PA: signing init segments with a generated test certificate")
	case certPath == "" || keyPath == "":
		return nil, errors.New("c2pacert and c2pakey must be set together")
	default:
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("c2pa credentials: %w", err)
		}
		var ok bool
		if key, ok = pair.PrivateKey.(crypto.Signer); !ok {
			return nil, errors.New("c2pa credentials: private key cannot sign")
		}
		for _, der := range pair.Certificate {
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, fmt.Errorf("c2pa credentials: %w", err)
			}
			chain = append(chain, cert)
		}
	}
	signer, err := c2pa.NewSigner(key, chain, c2pa.WithClaimGenerator("livesim2", internal.Version()))
	if err != nil {
		return nil, fmt.Errorf("c2pa signer: %w", err)
	}
	return &c2paCredentials{signer: signer, leaf: chain[0]}, nil
}

// c2paTestCredentials creates an ES256 test CA and an end-entity certificate it issued that
// meet the C2PA certificate profile, and returns the end-entity key and the chain. The
// end-entity certificate has the id-kp-emailProtection EKU, as c2pa-rs's sample certificates
// do, which validators accept for claim signing.
func c2paTestCredentials() (crypto.Signer, []*x509.Certificate, error) {
	now := time.Now()
	newCert := func(tmpl, parent *x509.Certificate, pub, signer any) (*x509.Certificate, error) {
		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
		if err != nil {
			return nil, err
		}
		tmpl.SerialNumber = serial
		tmpl.NotBefore = now.Add(-time.Hour)
		tmpl.NotAfter = now.Add(c2paValidityAfterStart + 24*time.Hour)
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
		if err != nil {
			return nil, err
		}
		return x509.ParseCertificate(der)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	caTmpl := &x509.Certificate{
		Subject:               pkix.Name{Organization: []string{"livesim2"}, CommonName: "livesim2 C2PA test CA"},
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	ca, err := newCert(caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	leaf, err := newCert(&x509.Certificate{
		Subject:               pkix.Name{Organization: []string{"livesim2"}, CommonName: "livesim2 C2PA test signer"},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		BasicConstraintsValid: true,
	}, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	return key, []*x509.Certificate{leaf, ca}, nil
}

// signC2PAInit signs the representation's init segment with a new session key, so that
// c2pa_vsi can serve it. Only clear audio and video representations are signed, and only
// when C2PA is enabled (creds is not nil).
func (r *RepData) signC2PAInit(ctx context.Context, logger *slog.Logger, creds *c2paCredentials, assetPath string) error {
	if creds == nil || r.PreEncrypted || (r.ContentType != "audio" && r.ContentType != "video") {
		return nil
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	kid := []byte(assetPath + "/" + r.ID)
	signedInit, signer, err := c2pavsi.SignInit(ctx, r.initBytes, c2pavsi.InitConfig{
		Signer:    creds.signer,
		Leaf:      creds.leaf,
		Seed:      seed,
		Kid:       kid,
		CreatedAt: c2paKeyCreatedAt,
		Validity:  time.Since(c2paKeyCreatedAt) + c2paValidityAfterStart,
		Title:     string(kid),
	})
	if err != nil {
		return err
	}
	r.c2pa = &c2paRep{init: signedInit, signer: signer}
	logger.Debug("C2PA VSI init signed", "rep", r.ID, "kid", string(kid), "manifestId", signer.ManifestID())
	return nil
}

// setC2PAEventIDs gives the VSI emsg boxes of each signed representation of the asset its
// own ids (c2pavsi.Signer.SetEventIDs), so that players don't merge the VSI events of
// different tracks for the same segment. Representations are numbered in ID order.
func (a *asset) setC2PAEventIDs() error {
	var ids []string
	for id, rep := range a.Reps {
		if rep.c2pa != nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for i, id := range ids {
		if err := a.Reps[id].c2pa.signer.SetEventIDs(uint32(i), uint32(len(ids))); err != nil {
			return err
		}
	}
	return nil
}

// signC2PASegment adds a VSI emsg box for the representation's session key to seg and
// returns the encoded segment.
func signC2PASegment(seg *mp4.MediaSegment, meta segMeta) ([]byte, error) {
	if meta.rep.c2pa == nil {
		return nil, errC2PAUnavailable
	}
	return meta.rep.c2pa.signer.SignSegment(seg, uint64(meta.newNr), meta.timescale, meta.newDur)
}

// verifyC2PAConfig rejects options c2pa_vsi can't be combined with yet.
func verifyC2PAConfig(cfg *ResponseConfig) error {
	if !cfg.C2PAVSI {
		return nil
	}
	switch {
	case cfg.ChunkDurS != nil || cfg.SSRFlag:
		// The segment hash is only known once the whole segment exists.
		return errors.New("c2pa_vsi cannot be combined with chunked delivery (chunkdur, ssras)")
	case cfg.DRM != "":
		// Encrypted init segments are generated per request, not signed at load.
		return errors.New("c2pa_vsi cannot be combined with drm or eccp")
	case len(cfg.TimeSubsStpp) > 0 || len(cfg.TimeSubsWvtt) > 0 || cfg.TimeSubsStpc != nil || cfg.TimeSubsWvtc != nil:
		// Generated subtitle tracks have no signed init segment.
		return errors.New("c2pa_vsi cannot be combined with generated time subtitles")
	case cfg.InbandPS:
		// The signed init segments cannot get another sample entry.
		return errors.New("c2pa_vsi cannot be combined with inbandps")
	}
	return nil
}
