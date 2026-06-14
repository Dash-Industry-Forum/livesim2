// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adsTestFS() fstest.MapFS {
	return fstest.MapFS{
		"ads/ad0/Manifest.mpd":     {Data: []byte("<MPD/>")},
		"ads/ad0/V600/init.mp4":    {Data: []byte("x")},
		"ads/ad1/Manifest.mpd":     {Data: []byte("<MPD/>")},
		"ads/ad2/Manifest.mpd":     {Data: []byte("<MPD/>")},
		"ads/ad3/manifest.mpd":     {Data: []byte("<MPD/>")}, // GPAC-style lowercase name
		"ads/notanad/readme.txt":   {Data: []byte("no manifest here")},
		"other/asset/Manifest.mpd": {Data: []byte("<MPD/>")},
	}
}

func TestDiscoverAdCreatives(t *testing.T) {
	ads, err := discoverAdCreatives(adsTestFS())
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"ads/ad0/Manifest.mpd", "ads/ad1/Manifest.mpd", "ads/ad2/Manifest.mpd", "ads/ad3/manifest.mpd"},
		ads, "only ad dirs with an MPD, sorted, with the actual MPD file name")
}

func TestFindAdMPD(t *testing.T) {
	fsys := fstest.MapFS{
		"ads/a/Manifest.mpd":  {Data: []byte("<MPD/>")},
		"ads/a/alt.mpd":       {Data: []byte("<MPD/>")},
		"ads/b/manifest.mpd":  {Data: []byte("<MPD/>")},
		"ads/c/readme.txt":    {Data: []byte("x")},
		"ads/d/sub/other.mpd": {Data: []byte("<MPD/>")}, // only direct children count
	}
	assert.Equal(t, "Manifest.mpd", findAdMPD(fsys, "ads/a"), "Manifest.mpd preferred")
	assert.Equal(t, "manifest.mpd", findAdMPD(fsys, "ads/b"))
	assert.Equal(t, "", findAdMPD(fsys, "ads/c"))
	assert.Equal(t, "", findAdMPD(fsys, "ads/d"))
	assert.Equal(t, "", findAdMPD(fsys, "ads/missing"))
}

func TestLoadAdMeta(t *testing.T) {
	// No ads.json -> nil, not an error.
	assert.Nil(t, loadAdMeta(adsTestFS()))

	fsWithMeta := adsTestFS()
	fsWithMeta["ads/ads.json"] = &fstest.MapFile{Data: []byte(
		`{"ad0":{"title":"A","interests":["tech"]},"ad1":{"interests":["sports","tech"]}}`)}
	meta := loadAdMeta(fsWithMeta)
	require.NotNil(t, meta)
	assert.Equal(t, "A", meta["ad0"].Title)
	assert.Equal(t, []string{"sports", "tech"}, meta["ad1"].Interests)

	// Invalid JSON -> nil (no crash).
	bad := adsTestFS()
	bad["ads/ads.json"] = &fstest.MapFile{Data: []byte("{not json")}
	assert.Nil(t, loadAdMeta(bad))
}

func TestParseInterests(t *testing.T) {
	assert.Nil(t, parseInterests(""))
	assert.Equal(t, []string{"boats"}, parseInterests("boats"))
	assert.Equal(t, []string{"boats", "sailing"}, parseInterests("boats,sailing"))
	assert.Equal(t, []string{"boats", "sailing"}, parseInterests(" boats , sailing , "))
}

func TestAdMetaMatchesAny(t *testing.T) {
	m := sgaiAdMeta{Interests: []string{"Sports", "tech"}}
	assert.True(t, adMetaMatchesAny(m, []string{"sports"}), "case-insensitive match")
	assert.True(t, adMetaMatchesAny(m, []string{"news", "TECH"}), "matches any of the list")
	assert.False(t, adMetaMatchesAny(m, []string{"news", "weather"}))
	assert.False(t, adMetaMatchesAny(sgaiAdMeta{}, []string{"sports"}))
	assert.False(t, adMetaMatchesAny(m, nil))
}

func TestBuildAdListMPD(t *testing.T) {
	host := "http://localhost:8899"
	pod := []string{"ads/ad2/Manifest.mpd", "ads/ad0/manifest.mpd"}
	durMS := map[string]int{"ad2": 5000, "ad0": 8000}
	mpd := buildAdListMPD(host, pod, durMS, "29689640")

	require.NotNil(t, mpd.Type)
	assert.Equal(t, "list", *mpd.Type)
	assert.Equal(t, "urn:mpeg:dash:profile:list:2024", string(mpd.Profiles))
	require.Len(t, mpd.Periods, 2)

	p0 := mpd.Periods[0]
	require.Len(t, p0.ImportedMPDs, 1)
	assert.Equal(t, "http://localhost:8899/vod/ads/ad2/Manifest.mpd", string(p0.ImportedMPDs[0].Value))
	p1 := mpd.Periods[1]
	require.Len(t, p1.ImportedMPDs, 1)
	assert.Equal(t, "http://localhost:8899/vod/ads/ad0/manifest.mpd", string(p1.ImportedMPDs[0].Value),
		"the actual (lowercase) MPD file name is used in the import URL")
	require.Len(t, p0.EventStreams, 1)
	es := p0.EventStreams[0]
	assert.Equal(t, sgaiCallbackScheme, string(es.SchemeIdUri))
	require.NotNil(t, es.Timescale)
	assert.Equal(t, uint32(1000), *es.Timescale)
	// impression + 4 quartiles, at correct presentation times (ms) for a 5000 ms ad. The
	// beacon path is common per ad (no session id) — the session rides via the callback
	// RequestParam; with sgaiBeaconStampEventID off there is no ?evId= either.
	require.Len(t, es.Events, 5)
	assert.Equal(t, "http://localhost:8899/sgai/beacon/ad2/impression", es.Events[0].Value)
	assert.Equal(t, uint64(0), es.Events[0].PresentationTime)
	assert.Equal(t, "http://localhost:8899/sgai/beacon/ad2/firstQuartile", es.Events[1].Value)
	assert.Equal(t, uint64(1250), es.Events[1].PresentationTime)
	assert.Equal(t, "http://localhost:8899/sgai/beacon/ad2/midpoint", es.Events[2].Value)
	assert.Equal(t, uint64(2500), es.Events[2].PresentationTime)
	assert.Equal(t, "http://localhost:8899/sgai/beacon/ad2/thirdQuartile", es.Events[3].Value)
	assert.Equal(t, "http://localhost:8899/sgai/beacon/ad2/complete", es.Events[4].Value)
	assert.Equal(t, uint64(5000), es.Events[4].PresentationTime)

	// Annex I: the callback EventStream carries a RequestParam that copies the List-MPD
	// request query (session id, interests, break) onto each beacon (DASH Ed.6 §8.13.2.4).
	require.Len(t, es.RequestParam, 1)
	assert.Equal(t, "callback", es.RequestParam[0].IncludeInRequests)
	assert.True(t, es.RequestParam[0].UseMPDUrlQuery)
	assert.Equal(t, "$querypart$", es.RequestParam[0].QueryTemplate)

	// Serializes as a valid List MPD.
	buf := bytes.NewBuffer(nil)
	_, err := mpd.Write(buf, "  ", true)
	require.NoError(t, err)
	xmlStr := buf.String()
	for _, want := range []string{
		`type="list"`,
		`profiles="urn:mpeg:dash:profile:list:2024"`,
		`<ImportedMPD earliestResolutionTimeOffset="0">http://localhost:8899/vod/ads/ad2/Manifest.mpd</ImportedMPD>`,
		`schemeIdUri="urn:mpeg:dash:event:callback:2015"`,
		`http://localhost:8899/sgai/beacon/ad2/impression`,
		`http://localhost:8899/sgai/beacon/ad2/thirdQuartile`,
		`includeInRequests="callback"`,
		`useMPDUrlQuery="true"`,
		// MPD-level marker that Annex I (2025) URL parameters are in use.
		`schemeIdUri="urn:mpeg:dash:urlparam:2025"`,
	} {
		assert.Contains(t, xmlStr, want)
	}
}

func TestParseBeaconPath(t *testing.T) {
	// The common (session-less) beacon path: exactly /sgai/beacon/<adId>/<event>.
	adID, event := parseBeaconPath("/sgai/beacon/ad0/impression")
	assert.Equal(t, "ad0", adID)
	assert.Equal(t, "impression", event)

	// Too few or too many segments yield empty (no session id is embedded in the path).
	a, e := parseBeaconPath("/sgai/beacon/onlyone")
	assert.Empty(t, a)
	assert.Empty(t, e)
	a, e = parseBeaconPath("/sgai/beacon/alice/ad0/impression")
	assert.Empty(t, a)
	assert.Empty(t, e)
}

func TestSGAIBeaconURL(t *testing.T) {
	// Common path, no session id and (with stamping off) no event id.
	assert.Equal(t, "http://h/sgai/beacon/ad0/impression", sgaiBeaconURL("http://h", "ad0", "impression", "42"))

	// With stamping on, the break/avail event id is appended as ?evId=.
	sgaiBeaconStampEventID = true
	defer func() { sgaiBeaconStampEventID = false }()
	assert.Equal(t, "http://h/sgai/beacon/ad0/impression?evId=42", sgaiBeaconURL("http://h", "ad0", "impression", "42"))
	assert.Equal(t, "http://h/sgai/beacon/ad0/impression", sgaiBeaconURL("http://h", "ad0", "impression", ""))
}

// The ad-decisioning endpoint requires a positive break duration: a missing/non-numeric/
// non-positive dur is a malformed request (the @uri always carries a valid dur), and must be
// rejected rather than silently returning the whole catalog as an over-long pod. The guard
// runs before the catalog is touched, so a bare Server suffices.
func TestSgaiAdsHandlerRequiresPositiveDur(t *testing.T) {
	s := &Server{}
	for _, q := range []string{"", "?dur=", "?dur=abc", "?dur=0", "?dur=-5", "?interests=travel"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/sgai/ads"+q, nil)
		s.sgaiAdsHandlerFunc(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code, "dur query %q must be rejected", q)
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"), "error must not be cached for %q", q)
	}
}
