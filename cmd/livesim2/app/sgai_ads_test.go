// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAdCatalog(t *testing.T) {
	fsys := adsTestFS()
	fsys["ads/ads.json"] = &fstest.MapFile{Data: []byte(
		`{"ad0":{"title":"Tech","interests":["tech"]},` +
			`"ad1":{"interests":["sports","boats"]},` +
			`"ad2":{"interests":["boats","sailing"]}}`)}
	durFn := func(mpdPath string) int {
		return map[string]int{
			"ads/ad0/Manifest.mpd": 8000,
			"ads/ad1/Manifest.mpd": 4800,
			"ads/ad2/Manifest.mpd": 5000,
			"ads/ad3/manifest.mpd": 6000,
		}[mpdPath]
	}

	cat := buildAdCatalog(fsys, durFn)
	require.Len(t, cat.ads, 4)
	assert.Equal(t, "ad0", cat.ads[0].ID)
	assert.Equal(t, "ads/ad0", cat.ads[0].Path)
	assert.Equal(t, "ads/ad0/Manifest.mpd", cat.ads[0].MPDPath)
	assert.Equal(t, "Tech", cat.ads[0].Title)
	assert.Equal(t, []string{"tech"}, cat.ads[0].Interests)
	assert.Equal(t, 8000, cat.ads[0].DurationMS)
	assert.Equal(t, []string{"boats", "sailing"}, cat.ads[2].Interests)
	assert.Equal(t, 5000, cat.ads[2].DurationMS)
	assert.Equal(t, "ads/ad3/manifest.mpd", cat.ads[3].MPDPath, "lowercase MPD name preserved")
	assert.Equal(t, 6000, cat.ads[3].DurationMS)
	assert.Nil(t, cat.ads[3].Interests, "no ads.json entry -> no tags")
}

func adIDs(pod []adEntry) []string {
	out := make([]string, len(pod))
	for i, e := range pod {
		out[i] = e.ID
	}
	return out
}

func testCatalog() *adCatalog {
	return &adCatalog{ads: []adEntry{
		{ID: "ad0", Path: "ads/ad0", Interests: []string{"tech"}, DurationMS: 8000},
		{ID: "ad1", Path: "ads/ad1", Interests: []string{"sports", "boats"}, DurationMS: 4800},
		{ID: "ad2", Path: "ads/ad2", Interests: []string{"boats", "sailing"}, DurationMS: 5000},
	}}
}

func TestAdCatalogSelectPodSteering(t *testing.T) {
	cat := testCatalog()

	// No interests -> empty pod: no ad is generated, the viewer keeps the base AD BREAK
	// slate. (An ad pod is only produced for an explicit interest match.)
	assert.Empty(t, cat.selectPod("alice", nil, 0), "no interests -> base ad break, no pod")

	// A single interest steers the matching ads to the lead.
	pod := cat.selectPod("alice", []string{"boats"}, 0)
	assert.ElementsMatch(t, []string{"ad1", "ad2"}, adIDs(pod)[:2], "boats-tagged ads lead")
	assert.Equal(t, "ad0", adIDs(pod)[2], "the rest fills")

	// A comma-separated list matches any of the interests.
	pod = cat.selectPod("alice", []string{"sailing", "tech"}, 0)
	assert.ElementsMatch(t, []string{"ad2", "ad0"}, adIDs(pod)[:2], "sailing + tech ads lead")

	// Interests that match nothing -> empty pod: the break stays unfilled and the
	// viewer keeps the underlying break content (the AD BREAK slate).
	assert.Empty(t, cat.selectPod("alice", []string{"weather"}, 0))
}

func TestAdCatalogSelectPodDurationFit(t *testing.T) {
	cat := testCatalog() // durations 8000 + 4800 + 5000 = 17800
	// Interests matching every ad so the pod holds all three (a per-session rotation),
	// letting us exercise the duration-fit trimming.
	all := []string{"tech", "boats"}

	assert.Len(t, cat.selectPod("alice", all, 0), 3, "no limit keeps all")
	assert.Len(t, cat.selectPod("alice", all, 1_000_000), 3, "ample budget keeps all")
	assert.Len(t, cat.selectPod("alice", all, 17800), 3, "exact total fits all")
	assert.Len(t, cat.selectPod("alice", all, 1), 1, "tiny budget keeps only the lead")

	// Just under the total: the last ad overflows and is dropped (the first two always sum to
	// 17800 - last <= 17799, regardless of rotation), so two ads remain within budget.
	pod := cat.selectPod("alice", all, 17799)
	require.Len(t, pod, 2)
	total := 0
	for _, e := range pod {
		total += e.DurationMS
	}
	assert.LessOrEqual(t, total, 17799)
}
