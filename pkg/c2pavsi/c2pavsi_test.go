// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package c2pavsi

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures in testdata/<rep> are livesim2 testpic_2s segments: raw/ as served, and
// c2pars/ signed by c2pa-rs's LiveVideoVsiSigner (see testdata/README.md) with the session
// key whose seed, kid, public key and init manifest ID are in c2pars/vsi.json.
var reps = []string{"V300", "A48"}

type vsiInfo struct {
	ManifestID string `json:"manifestId"`
	Kid        string `json:"kid"`
	Seed       string `json:"seed"`
	PublicKey  string `json:"publicKey"`
}

func loadInfo(t *testing.T, rep string) vsiInfo {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", rep, "c2pars", "vsi.json"))
	require.NoError(t, err)
	var info vsiInfo
	require.NoError(t, json.Unmarshal(data, &info))
	return info
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

func segmentFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.m4s"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	sort.Strings(files)
	return files
}

func decode(t *testing.T, data []byte) *mp4.File {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(data))
	require.NoError(t, err)
	return f
}

// trackTiming returns the track timescale from the init segment and the segment's
// sequence number and duration in that timescale.
func trackTiming(t *testing.T, init, seg []byte) (timescale uint32, seqNr uint64, duration uint32) {
	t.Helper()
	timescale = decode(t, init).Init.Moov.Trak.Mdia.Mdhd.Timescale
	var total uint64
	for _, frag := range decode(t, seg).Segments[0].Fragments {
		traf := frag.Moof.Traf
		seqNr = uint64(frag.Moof.Mfhd.SequenceNumber)
		total += traf.Trun.Duration(traf.Tfhd.DefaultSampleDuration)
	}
	return timescale, seqNr, uint32(total)
}

func topLevelTypes(t *testing.T, seg []byte) []string {
	t.Helper()
	boxes, err := topLevelBoxes(seg)
	require.NoError(t, err)
	types := make([]string, len(boxes))
	for i, b := range boxes {
		types[i] = b.boxType
	}
	return types
}

// TestVerifyC2PARSSignedSegments checks that this package reads segments signed by c2pa-rs:
// the same COSE_Sign1 and segment-info-map encoding, and the same bmffHash over the boxes.
func TestVerifyC2PARSSignedSegments(t *testing.T) {
	for _, rep := range reps {
		info := loadInfo(t, rep)
		pub := ed25519.PublicKey(mustHex(t, info.PublicKey))
		for _, file := range segmentFiles(t, filepath.Join("testdata", rep, "c2pars")) {
			seg, err := os.ReadFile(file)
			require.NoError(t, err)
			got, err := Verify(seg, pub)
			require.NoError(t, err, file)
			assert.Equal(t, info.ManifestID, got.ManifestID, file)
			_, seqNr, _ := trackTiming(t, mustRead(t, filepath.Join("testdata", rep, "raw", "init.mp4")), seg)
			assert.Equal(t, seqNr, got.SequenceNumber, file)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func newTestSigner(t *testing.T, rep string) *Signer {
	t.Helper()
	info := loadInfo(t, rep)
	signer, err := NewSigner(mustHex(t, info.Seed), []byte(info.Kid), info.ManifestID)
	require.NoError(t, err)
	assert.Equal(t, mustHex(t, info.PublicKey), []byte(signer.PublicKey()))
	return signer
}

// TestSignRawSegments signs the raw livesim2 segments and checks the result.
func TestSignRawSegments(t *testing.T) {
	for _, rep := range reps {
		signer := newTestSigner(t, rep)
		init := mustRead(t, filepath.Join("testdata", rep, "raw", "init.mp4"))
		for _, file := range segmentFiles(t, filepath.Join("testdata", rep, "raw")) {
			raw := mustRead(t, file)
			timescale, seqNr, duration := trackTiming(t, init, raw)

			signed, err := sign(t, signer, raw, seqNr, timescale, duration)
			require.NoError(t, err, file)
			info, err := Verify(signed, signer.PublicKey())
			require.NoError(t, err, file)
			assert.Equal(t, seqNr, info.SequenceNumber)
			assert.Equal(t, loadInfo(t, rep).ManifestID, info.ManifestID)

			// styp stays first; the emsg box goes before the first moof, and nothing else changes.
			assert.Equal(t, append([]string{"styp", "emsg"}, topLevelTypes(t, raw)[1:]...), topLevelTypes(t, signed))
			assert.Equal(t, raw, withoutVSIEmsg(t, signed))
			emsg, err := FindEmsg(signed)
			require.NoError(t, err)
			assert.Equal(t, Emsg{timescale, 0, duration, uint32(seqNr), emsg.MessageData}, *emsg)

			// Deterministic: the same segment always signs to the same bytes.
			again, err := sign(t, signer, raw, seqNr, timescale, duration)
			require.NoError(t, err)
			assert.Equal(t, signed, again)
		}
	}
}

func TestTamperedSegmentFails(t *testing.T) {
	signer := newTestSigner(t, "V300")
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	raw := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "raw"))[0])
	timescale, seqNr, duration := trackTiming(t, init, raw)
	signed, err := sign(t, signer, raw, seqNr, timescale, duration)
	require.NoError(t, err)

	tampered := bytes.Clone(signed)
	tampered[len(tampered)-1] ^= 0xff // media data in the mdat box
	_, err = Verify(tampered, signer.PublicKey())
	assert.ErrorContains(t, err, "does not match bmffHash")

	other, err := NewSigner(bytes.Repeat([]byte{7}, 32), []byte("k"), "urn:c2pa:x")
	require.NoError(t, err)
	_, err = Verify(signed, other.PublicKey())
	assert.ErrorContains(t, err, "signature does not verify")
}

// TestWriteSignedForInterop writes Go-signed segments for checking with c2pa-rs's validator,
// next to the c2pa-rs-signed init segment whose session key they use:
//
//	C2PAVSI_OUT=/tmp/vsi go test ./pkg/c2pavsi -run Interop
//	vsi-golden check --init /tmp/vsi/V300/init.mp4 /tmp/vsi/V300/*.m4s
func TestWriteSignedForInterop(t *testing.T) {
	out := os.Getenv("C2PAVSI_OUT")
	if out == "" {
		t.Skip("set C2PAVSI_OUT to write Go-signed segments")
	}
	for _, rep := range reps {
		dir := filepath.Join(out, rep)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "init.mp4"),
			mustRead(t, filepath.Join("testdata", rep, "c2pars", "init.mp4")), 0o644))
		signer := newTestSigner(t, rep)
		init := mustRead(t, filepath.Join("testdata", rep, "raw", "init.mp4"))
		for _, file := range segmentFiles(t, filepath.Join("testdata", rep, "raw")) {
			raw := mustRead(t, file)
			timescale, seqNr, duration := trackTiming(t, init, raw)
			signed, err := sign(t, signer, raw, seqNr, timescale, duration)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.Base(file)), signed, 0o644))
		}
	}
}

func makeBox(boxType string, payload []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	return append(append(b, boxType...), payload...)
}

// makeSidx returns a version 0 sidx with one reference per (isSidx, referencedSize).
func makeSidx(refs ...[2]uint32) []byte {
	p := []byte{0, 0, 0, 0}                    // version 0, flags
	p = binary.BigEndian.AppendUint32(p, 1)    // reference_ID
	p = binary.BigEndian.AppendUint32(p, 1000) // timescale
	p = binary.BigEndian.AppendUint32(p, 0)    // earliest_presentation_time
	p = binary.BigEndian.AppendUint32(p, 0)    // first_offset
	p = binary.BigEndian.AppendUint16(p, 0)    // reserved
	p = binary.BigEndian.AppendUint16(p, uint16(len(refs)))
	for _, r := range refs {
		p = binary.BigEndian.AppendUint32(p, r[0]<<31|r[1])
		p = binary.BigEndian.AppendUint32(p, 1000)        // subsegment_duration
		p = binary.BigEndian.AppendUint32(p, 0x9000_0000) // SAP
	}
	return makeBox("sidx", p)
}

// sign decodes raw with mp4ff and signs it.
func sign(t *testing.T, s *Signer, raw []byte, seqNr uint64, timescale, duration uint32) ([]byte, error) {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	require.NoError(t, err)
	return s.SignSegment(f.Segments[0], seqNr, timescale, duration)
}

// withoutVSIEmsg returns seg without its VSI emsg boxes.
func withoutVSIEmsg(t *testing.T, seg []byte) []byte {
	t.Helper()
	boxes, err := topLevelBoxes(seg)
	require.NoError(t, err)
	var out []byte
	for _, b := range boxes {
		if parseVSIEmsg(b) == nil {
			out = append(out, b.data...)
		}
	}
	return out
}

// TestSidxLayouts checks that a sidx with one reference per fragment grows to include the
// VSI emsg box, and that other sidx layouts are rejected.
func TestSidxLayouts(t *testing.T) {
	signer := newTestSigner(t, "V300")
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	raw := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "raw"))[0])
	timescale, seqNr, duration := trackTiming(t, init, raw)
	boxes, err := topLevelBoxes(raw)
	require.NoError(t, err)
	styp, media := boxes[0].data, raw[len(boxes[0].data):]
	withSidx := func(sidxs ...[]byte) []byte {
		return bytes.Join(append(append([][]byte{styp}, sidxs...), media), nil)
	}

	seg := withSidx(makeSidx([2]uint32{0, uint32(len(media))}))
	signed, err := sign(t, signer, seg, seqNr, timescale, duration)
	require.NoError(t, err)
	assert.Equal(t, []string{"styp", "sidx", "emsg", "moof", "mdat"}, topLevelTypes(t, signed))
	emsgLen := len(signed) - len(seg)
	sidx := signed[len(styp):]
	assert.Equal(t, uint32(len(media)+emsgLen), binary.BigEndian.Uint32(sidx[32:])&0x7fff_ffff)
	_, err = Verify(signed, signer.PublicKey())
	require.NoError(t, err)

	for name, bad := range map[string][]byte{
		"two sidx":         withSidx(makeSidx([2]uint32{0, uint32(len(media))}), makeSidx([2]uint32{0, uint32(len(media))})),
		"hierarchical":     withSidx(makeSidx([2]uint32{1, uint32(len(media))})),
		"refs ≠ fragments": withSidx(makeSidx([2]uint32{0, 10}, [2]uint32{0, uint32(len(media)) - 10})),
	} {
		_, err := sign(t, signer, bad, seqNr, timescale, duration)
		assert.Error(t, err, name)
	}
}

// TestEventIDs checks that SetEventIDs gives each track of a segment its own emsg id.
func TestEventIDs(t *testing.T) {
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	raw := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "raw"))[0])
	timescale, seqNr, duration := trackTiming(t, init, raw)
	ids := map[uint32]bool{}
	for index := range uint32(3) {
		signer := newTestSigner(t, "V300")
		require.NoError(t, signer.SetEventIDs(index, 3))
		signed, err := sign(t, signer, raw, seqNr, timescale, duration)
		require.NoError(t, err)
		emsg, err := FindEmsg(signed)
		require.NoError(t, err)
		assert.Equal(t, uint32(seqNr)*3+index, emsg.ID)
		ids[emsg.ID] = true
	}
	assert.Len(t, ids, 3)
	assert.Error(t, newTestSigner(t, "V300").SetEventIDs(2, 2))
}

func TestNewSignerValidation(t *testing.T) {
	_, err := NewSigner(make([]byte, 31), []byte("k"), "urn:c2pa:x")
	assert.Error(t, err)
	_, err = NewSigner(make([]byte, 32), nil, "urn:c2pa:x")
	assert.Error(t, err)
	_, err = NewSigner(make([]byte, 32), []byte("k"), "")
	assert.Error(t, err)
}

// emsgV0 is a version 0 emsg box with the given scheme_id_uri, value and message_data.
func emsgV0(scheme, value string, data []byte) []byte {
	body := []byte{0, 0, 0, 0} // version 0, flags
	body = append(body, scheme...)
	body = append(body, 0)
	body = append(body, value...)
	body = append(body, 0)
	body = append(body, make([]byte, 16)...) // timescale, presentation_time_delta, event_duration, id
	body = append(body, data...)
	box := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	box = append(box, "emsg"...)
	return append(box, body...)
}

func exclusionValues(h BMFFHash) []string {
	var values []string
	for _, e := range h.Exclusions {
		for _, d := range e.Data {
			values = append(values, string(d.Value))
		}
	}
	return values
}

// TestExclusionValue checks that SignSegment emits the NUL-terminated exclusion value
// (c2pa-org/specs-core#2542) and that segments with the legacy unterminated value, such as
// the c2pa-rs fixtures, still verify.
func TestExclusionValue(t *testing.T) {
	signer := newTestSigner(t, "V300")
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	raw := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "raw"))[0])
	timescale, seqNr, duration := trackTiming(t, init, raw)
	signed, err := sign(t, signer, raw, seqNr, timescale, duration)
	require.NoError(t, err)
	info, err := Verify(signed, signer.PublicKey())
	require.NoError(t, err)
	assert.Equal(t, []string{SchemeIDURI + "\x00"}, exclusionValues(info.BMFFHash))

	legacy := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "c2pars"))[0])
	info, err = Verify(legacy, signer.PublicKey())
	require.NoError(t, err)
	assert.Equal(t, []string{SchemeIDURI}, exclusionValues(info.BMFFHash))
}

// TestExcludedNonVSIEmsg checks that an emsg box the exclusion matches but that isn't a VSI
// emsg box fails verification instead of riding along unhashed (c2pa-org/specs-core#2619).
func TestExcludedNonVSIEmsg(t *testing.T) {
	signer := newTestSigner(t, "V300")
	init := mustRead(t, filepath.Join("testdata", "V300", "raw", "init.mp4"))
	raw := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "raw"))[0])
	timescale, seqNr, duration := trackTiming(t, init, raw)
	longer := emsgV0(SchemeIDURI+"-extra", Value, []byte("data"))

	// With the NUL-terminated value, a longer scheme isn't excluded: it's hashed like any
	// other box, so the segment verifies and changing it is detected.
	boxes, err := topLevelBoxes(raw)
	require.NoError(t, err)
	styp := boxes[0].data
	withLonger := append(append(bytes.Clone(styp), longer...), raw[len(styp):]...)
	signed, err := sign(t, signer, withLonger, seqNr, timescale, duration)
	require.NoError(t, err)
	_, err = Verify(signed, signer.PublicKey())
	require.NoError(t, err)
	at := bytes.Index(signed, longer)
	require.Positive(t, at)
	tampered := bytes.Clone(signed)
	tampered[at+len(longer)-1] ^= 0xff
	_, err = Verify(tampered, signer.PublicKey())
	assert.ErrorContains(t, err, "does not match bmffHash")

	// The exact scheme with another value is excluded by either form.
	signed, err = sign(t, signer, raw, seqNr, timescale, duration)
	require.NoError(t, err)
	_, err = Verify(append(signed, emsgV0(SchemeIDURI, "other", nil)...), signer.PublicKey())
	assert.ErrorContains(t, err, "not a VSI emsg box")

	// The legacy prefix match also excludes the longer scheme.
	legacy := mustRead(t, segmentFiles(t, filepath.Join("testdata", "V300", "c2pars"))[0])
	_, err = Verify(append(bytes.Clone(legacy), longer...), signer.PublicKey())
	assert.ErrorContains(t, err, "not a VSI emsg box")
}
