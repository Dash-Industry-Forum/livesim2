// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"

	"github.com/Dash-Industry-Forum/livesim2/pkg/c2pavsi"
	"github.com/Eyevinn/mp4ff/mp4"
)

// C2PA Verifiable Segment Info (c2pa_vsi option).
//
// A representation can be served with C2PA VSI when its asset has a pre-signed init segment
// and session key for it under c2pa/<representation id>/:
//
//   - init.mp4: the init segment livesim2 serves, signed with a C2PA manifest whose
//     c2pa.session-keys assertion announces the session key.
//   - vsi.json: {"manifestId": "...", "kid": "...", "seed": "<hex>"}. That is the manifest's
//     identifier, and the session key's identifier and 32-byte Ed25519 seed.
//
// livesim2 serves that init segment and signs each media segment with the session key.

// errC2PAUnavailable is returned when c2pa_vsi is requested for media without a pre-signed
// init segment and session key.
var errC2PAUnavailable = errors.New(
	"c2pa_vsi: no pre-signed C2PA init segment and session key for this representation")

const (
	c2paDir      = "c2pa"
	c2paInitName = "init.mp4"
	c2paInfoName = "vsi.json"
)

// c2paRep holds a representation's pre-signed init segment and its session key signer.
type c2paRep struct {
	init   []byte
	signer *c2pavsi.Signer
}

type c2paVSIInfo struct {
	ManifestID string `json:"manifestId"`
	Kid        string `json:"kid"`
	Seed       string `json:"seed"`
}

// loadC2PA loads the representation's pre-signed C2PA init segment and session key, if the
// asset has them. It checks that the signed init segment is the one livesim2 serves, plus
// the C2PA manifest box.
func (r *RepData) loadC2PA(logger *slog.Logger, vodFS fs.FS, assetPath string) error {
	dir := path.Join(assetPath, c2paDir, r.ID)
	signedInit, err := fs.ReadFile(vodFS, path.Join(dir, c2paInitName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read c2pa init: %w", err)
	}
	infoData, err := fs.ReadFile(vodFS, path.Join(dir, c2paInfoName))
	if err != nil {
		return fmt.Errorf("read %s: %w", c2paInfoName, err)
	}
	var info c2paVSIInfo
	if err := json.Unmarshal(infoData, &info); err != nil {
		return fmt.Errorf("decode %s: %w", c2paInfoName, err)
	}
	seed, err := hex.DecodeString(info.Seed)
	if err != nil {
		return fmt.Errorf("%s seed: %w", c2paInfoName, err)
	}
	signer, err := c2pavsi.NewSigner(seed, []byte(info.Kid), info.ManifestID)
	if err != nil {
		return fmt.Errorf("%s: %w", c2paInfoName, err)
	}
	unsigned, err := c2pavsi.WithoutManifestBox(signedInit)
	if err != nil {
		return fmt.Errorf("c2pa init: %w", err)
	}
	if !bytes.Equal(unsigned, r.initBytes) {
		return errors.New("c2pa init does not match the init segment livesim2 serves; re-sign it")
	}
	r.c2pa = &c2paRep{init: signedInit, signer: signer}
	logger.Info("C2PA VSI enabled", "rep", r.ID, "kid", info.Kid, "manifestId", info.ManifestID)
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
