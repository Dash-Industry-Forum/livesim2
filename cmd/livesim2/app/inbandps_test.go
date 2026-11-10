// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Eyevinn/mp4ff/avc"
	"github.com/Eyevinn/mp4ff/hevc"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

func TestInbandCodecs(t *testing.T) {
	cases := map[string]string{
		"avc1.64001e":            "avc3.64001e",
		"avc1":                   "avc3",
		"hvc1.1.6.L123.90":       "hev1.1.6.L123.90",
		"avc1.64001e,mp4a.40.2":  "avc3.64001e,mp4a.40.2",
		"avc3.64001e":            "avc3.64001e",
		"hev1.1.6.L93.B0":        "hev1.1.6.L93.B0",
		"av01.0.08M.08":          "av01.0.08M.08",
		"avc10.64001e (no such)": "avc10.64001e (no such)",
	}
	for in, want := range cases {
		require.Equal(t, want, inbandCodecs(in), in)
	}
}

// naluTypes returns the NAL unit types of an AVC or HEVC sample.
func naluTypes(sample []byte, isHEVC bool) []int {
	var types []int
	if isHEVC {
		for _, nt := range hevc.FindNaluTypes(sample) {
			types = append(types, int(nt))
		}
		return types
	}
	for _, nt := range avc.FindNaluTypes(sample) {
		types = append(types, int(nt))
	}
	return types
}

func decodeSegmentSamples(t *testing.T, body []byte, init *mp4.InitSegment) []mp4.FullSample {
	t.Helper()
	f, err := mp4.DecodeFile(bytes.NewReader(body))
	require.NoError(t, err)
	require.Len(t, f.Segments, 1)
	var samples []mp4.FullSample
	for _, frag := range f.Segments[0].Fragments {
		samples, err = frag.AppendFullSamples(samples, init.Moov.Mvex.Trex)
		require.NoError(t, err)
	}
	return samples
}

// TestInbandPS checks that inbandps_1 signals avc3 and hev1 in the MPD and the init segment,
// and adds the parameter sets of the sample entry in front of the slices of each sync sample,
// after an access unit delimiter and before any SEI, also when the segment is encrypted.
func TestInbandPS(t *testing.T) {
	server, get := testLiveServer(t)
	cases := []struct {
		asset, mpdName, repID, initPath, mediaFmt string
		nr                                        int
		isHEVC                                    bool
		wantEntry                                 string
		psTypes                                   []int
	}{
		{"testpic_2s_low_delay", "Manifest.mpd", "360", "360/init.mp4", "360/%d.m4s", 50, false, "avc3",
			[]int{int(avc.NALU_SPS), int(avc.NALU_PPS)}},
		// The IDR pictures of V300 already carry their SPS and PPS, so only the sample entry changes.
		{"testpic_2s", "Manifest.mpd", "V300", "V300/init.mp4", "V300/%d.m4s", 50, false, "avc3",
			[]int{int(avc.NALU_SPS), int(avc.NALU_PPS)}},
		{"mpegh_LC_1_6", "LC_1_6.mpd", "hvc1_10000kbps", "hvc1_10000kbps_init.mp4", "hvc1_10000kbps_%d.m4s", 50, true, "hev1",
			[]int{int(hevc.NALU_VPS), int(hevc.NALU_SPS), int(hevc.NALU_PPS)}},
	}
	for _, c := range cases {
		t.Run(c.asset, func(t *testing.T) {
			a, ok := server.assetMgr.findAsset(c.asset)
			require.True(t, ok)
			rep := a.Reps[c.repID]
			nowMS := (c.nr + 5) * a.SegmentDurMS
			base := "/livesim2/inbandps_1/" + c.asset + "/"

			status, body := get(base+c.mpdName, nowMS)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, string(body), `codecs="`+inbandCodecs(rep.Codecs)+`"`)
			require.NotContains(t, string(body), `codecs="`+rep.Codecs+`"`)

			status, body = get(base+c.initPath, nowMS)
			require.Equal(t, http.StatusOK, status)
			initFile, err := mp4.DecodeFile(bytes.NewReader(body))
			require.NoError(t, err)
			stsd := initFile.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd
			require.Equal(t, c.wantEntry, stsd.Children[0].Type())

			media := fmt.Sprintf(c.mediaFmt, c.nr)
			status, plainBody := get("/livesim2/"+c.asset+"/"+media, nowMS)
			require.Equal(t, http.StatusOK, status)
			status, inbandBody := get(base+media, nowMS)
			require.Equal(t, http.StatusOK, status)
			plain := decodeSegmentSamples(t, plainBody, rep.initSeg)
			inband := decodeSegmentSamples(t, inbandBody, rep.initSeg)
			require.Len(t, inband, len(plain))
			nrRAP := 0
			for i := range plain {
				isRAP := avc.IsIDRSample(plain[i].Data)
				if c.isHEVC {
					isRAP = hevc.IsRAPSample(plain[i].Data)
				}
				if !isRAP {
					require.Equal(t, plain[i].Data, inband[i].Data, "sample %d", i)
					continue
				}
				nrRAP++
				plainTypes, inbandTypes := naluTypes(plain[i].Data, c.isHEVC), naluTypes(inband[i].Data, c.isHEVC)
				audSize := firstAUDSize(plain[i].Data, c.isHEVC)
				nrAUD := 0
				if audSize > 0 {
					nrAUD = 1
				}
				var missing []int
				for _, pt := range c.psTypes {
					if !slices.Contains(plainTypes, pt) {
						missing = append(missing, pt)
					}
				}
				want := append(append(append([]int{}, plainTypes[:nrAUD]...), missing...), plainTypes[nrAUD:]...)
				require.Equal(t, want, inbandTypes, "NAL unit types of random access sample %d", i)
				psSize := len(inband[i].Data) - len(plain[i].Data)
				require.Equal(t, plain[i].Data[audSize:], inband[i].Data[audSize+psSize:], "rest of sample %d", i)
			}
			require.Positive(t, nrRAP)
		})
	}
}

// TestInbandPSEncrypted checks that inbandps_1 renames the original format of an encrypted
// sample entry, and that the encrypted samples decrypt to the samples of the clear segment.
func TestInbandPSEncrypted(t *testing.T) {
	server, get := testLiveServer(t)
	a, ok := server.assetMgr.findAsset("testpic_2s_low_delay")
	require.True(t, ok)
	rep := a.Reps["360"]
	const nr = 50
	nowMS := (nr + 5) * a.SegmentDurMS
	for _, scheme := range []string{"cenc", "cbcs"} {
		t.Run(scheme, func(t *testing.T) {
			base := "/livesim2/inbandps_1/eccp_" + scheme + "/testpic_2s_low_delay/"
			status, body := get(base+"360/init.mp4", nowMS)
			require.Equal(t, http.StatusOK, status)
			initFile, err := mp4.DecodeFile(bytes.NewReader(body))
			require.NoError(t, err)
			stsd := initFile.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd
			require.Equal(t, "encv", stsd.Children[0].Type())
			require.Equal(t, "avc3", stsd.Encv.Sinf.Frma.DataFormat)
			di, err := mp4.DecryptInit(initFile.Init)
			require.NoError(t, err)

			status, body = get(base+fmt.Sprintf("360/%d.m4s", nr), nowMS)
			require.Equal(t, http.StatusOK, status)
			encFile, err := mp4.DecodeFile(bytes.NewReader(body))
			require.NoError(t, err)
			require.NoError(t, mp4.DecryptSegment(encFile.Segments[0], di, rep.encData.key[:]))
			var decrypted bytes.Buffer
			require.NoError(t, encFile.Segments[0].Encode(&decrypted))

			status, clearBody := get(fmt.Sprintf("/livesim2/inbandps_1/testpic_2s_low_delay/360/%d.m4s", nr), nowMS)
			require.Equal(t, http.StatusOK, status)
			clear := decodeSegmentSamples(t, clearBody, rep.initSeg)
			dec := decodeSegmentSamples(t, decrypted.Bytes(), rep.initSeg)
			require.Len(t, dec, len(clear))
			for i := range clear {
				require.Equal(t, clear[i].Data, dec[i].Data, "sample %d", i)
			}
		})
	}
}

// TestInbandPSWithCC608 checks that the parameter sets come before the CTA-608 SEI.
func TestInbandPSWithCC608(t *testing.T) {
	server, get := testLiveServer(t)
	a, ok := server.assetMgr.findAsset("testpic_2s_low_delay")
	require.True(t, ok)
	const nr = 50
	status, body := get(fmt.Sprintf("/livesim2/inbandps_1/timecc608_CC1-eng/testpic_2s_low_delay/360/%d.m4s", nr), (nr+5)*a.SegmentDurMS)
	require.Equal(t, http.StatusOK, status)
	samples := decodeSegmentSamples(t, body, a.Reps["360"].initSeg)
	for i, s := range samples {
		if !avc.IsIDRSample(s.Data) {
			continue
		}
		types := naluTypes(s.Data, false)
		spsIdx, seiIdx := -1, -1
		for j, nt := range types {
			if nt == int(avc.NALU_SPS) && spsIdx < 0 {
				spsIdx = j
			}
			if nt == int(avc.NALU_SEI) && seiIdx < 0 {
				seiIdx = j
			}
		}
		require.True(t, spsIdx >= 0 && seiIdx > spsIdx, "sample %d NAL unit types %v", i, types)
	}
}

// TestMissingParameterSets checks that only an IDR or IRAP picture gets parameter sets, and only
// those of a type that it does not have.
func TestMissingParameterSets(t *testing.T) {
	nalu := func(header ...byte) []byte {
		return append(binary.BigEndian.AppendUint32(nil, uint32(len(header))), header...)
	}
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	sps, pps, sei, idr, nonIDR := nalu(0x67, 1), nalu(0x68, 2), nalu(0x06, 3), nalu(0x65, 4), nalu(0x41, 5)
	avcSets := []parameterSet{{int(avc.NALU_SPS), sps}, {int(avc.NALU_PPS), pps}}
	require.Equal(t, cat(sps, pps), missingParameterSets(cat(sei, idr), avcSets, false))
	require.Equal(t, sps, missingParameterSets(cat(pps, sei, idr), avcSets, false), "only the missing SPS")
	require.Empty(t, missingParameterSets(cat(sps, pps, idr), avcSets, false))
	require.Empty(t, missingParameterSets(cat(sei, nonIDR), avcSets, false), "not an IDR picture")

	vps, hsps, hpps := nalu(0x40, 1, 1), nalu(0x42, 1, 2), nalu(0x44, 1, 3)
	aud, cra, trail := nalu(0x46, 1, 0x50), nalu(0x2a, 1, 4), nalu(0x02, 1, 5)
	hevcSets := []parameterSet{{int(hevc.NALU_VPS), vps}, {int(hevc.NALU_SPS), hsps}, {int(hevc.NALU_PPS), hpps}}
	require.Equal(t, cat(vps, hsps, hpps), missingParameterSets(cat(aud, cra), hevcSets, true), "CRA is an IRAP picture")
	require.Empty(t, missingParameterSets(cat(aud, trail), hevcSets, true))
	require.Equal(t, len(aud), firstAUDSize(cat(aud, cra), true))
	require.Equal(t, 0, firstAUDSize(cat(cra, aud), true))
}

func TestInbandPSConfig(t *testing.T) {
	_, err := processURLCfg("/livesim2/inbandps_2/testpic_2s/Manifest.mpd", 100_000)
	require.ErrorContains(t, err, "inbandps")
	_, err = processURLCfg("/livesim2/inbandps_1/c2pa_vsi/testpic_2s/Manifest.mpd", 100_000)
	require.ErrorContains(t, err, "c2pa_vsi cannot be combined with inbandps")
	cfg, err := processURLCfg("/livesim2/inbandps_1/testpic_2s/Manifest.mpd", 100_000)
	require.NoError(t, err)
	require.True(t, cfg.InbandPS)
}

// TestInbandPSChunked checks that the first chunk of a chunked segment gets the parameter sets.
func TestInbandPSChunked(t *testing.T) {
	server, get := testLiveServer(t)
	a, ok := server.assetMgr.findAsset("testpic_2s_low_delay")
	require.True(t, ok)
	const nr = 50
	status, body := get(fmt.Sprintf("/livesim2/inbandps_1/chunkdur_0.5/testpic_2s_low_delay/360/%d.m4s", nr), (nr+5)*a.SegmentDurMS)
	require.Equal(t, http.StatusOK, status)
	f, err := mp4.DecodeFile(bytes.NewReader(body))
	require.NoError(t, err)
	require.Len(t, f.Segments[0].Fragments, 4)
	samples, err := f.Segments[0].Fragments[0].GetFullSamples(a.Reps["360"].initSeg.Moov.Mvex.Trex)
	require.NoError(t, err)
	require.Equal(t, []avc.NaluType{avc.NALU_SPS, avc.NALU_PPS}, avc.FindNaluTypes(samples[0].Data)[:2])
}
