// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
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
	"time"

	"github.com/Dash-Industry-Forum/livesim2/pkg/c2pavsi"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/richardwooding/c2pa"
)

// C2PA Verifiable Segment Info (c2pa_vsi option).
//
// When livesim2 loads an asset, it signs the init segment of each audio and video
// representation with a C2PA manifest (github.com/richardwooding/c2pa) whose
// c2pa.session-keys assertion announces a new Ed25519 session key. With c2pa_vsi, livesim2
// serves that init segment and signs each media segment with the session key.
//
// The claim-signing certificate and key come from the c2pacert and c2pakey options. Without
// them, livesim2 creates a test CA and end-entity certificate at startup, which validators
// report as untrusted.

// errC2PAUnavailable is returned when c2pa_vsi is requested for a representation without a
// signed init segment and session key.
var errC2PAUnavailable = errors.New(
	"c2pa_vsi: no C2PA-signed init segment and session key for this representation")

// c2paSessionKeyValidity is the validityPeriod of the session keys livesim2 creates. They
// are not rotated, so it covers a long-running server.
const c2paSessionKeyValidity = 365 * 24 * time.Hour

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
	signer, err := c2pa.NewSigner(key, chain, c2pa.WithClaimGenerator("livesim2", "c2pa_vsi"))
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
		tmpl.NotAfter = now.Add(c2paSessionKeyValidity + 24*time.Hour)
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
// c2pa_vsi can serve it. Only clear audio and video representations are signed.
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
		CreatedAt: time.Now(),
		Validity:  c2paSessionKeyValidity,
		Title:     string(kid),
	})
	if err != nil {
		return err
	}
	r.c2pa = &c2paRep{init: signedInit, signer: signer}
	logger.Debug("C2PA VSI init signed", "rep", r.ID, "kid", string(kid), "manifestId", signer.ManifestID())
	return nil
}

// signC2PASegment encodes seg and adds a VSI emsg box for the representation's session key.
func signC2PASegment(seg *mp4.MediaSegment, meta segMeta) ([]byte, error) {
	rep := meta.rep
	if rep.c2pa == nil {
		return nil, errC2PAUnavailable
	}
	var buf bytes.Buffer
	if err := seg.Encode(&buf); err != nil {
		return nil, fmt.Errorf("encode segment: %w", err)
	}
	var duration uint64
	for _, frag := range seg.Fragments {
		traf := frag.Moof.Traf
		defaultDur := rep.DefaultSampleDuration
		if traf.Tfhd.HasDefaultSampleDuration() {
			defaultDur = traf.Tfhd.DefaultSampleDuration
		}
		duration += traf.Trun.Duration(defaultDur)
	}
	return rep.c2pa.signer.SignSegment(buf.Bytes(), uint64(meta.newNr), uint32(rep.MediaTimescale), uint32(duration))
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
		// Encrypted init segments are generated per request and can't be pre-signed.
		return errors.New("c2pa_vsi cannot be combined with drm or eccp")
	case len(cfg.TimeSubsStpp) > 0 || len(cfg.TimeSubsWvtt) > 0 || cfg.TimeSubsStpc != nil || cfg.TimeSubsWvtc != nil:
		// Generated subtitle tracks have no pre-signed init segment.
		return errors.New("c2pa_vsi cannot be combined with generated time subtitles")
	}
	return nil
}
