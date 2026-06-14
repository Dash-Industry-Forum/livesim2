// Copyright 2025, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"io/fs"
	"path"
)

// adEntry is one ad creative in the catalog: its id, asset path, MPD path (the MPD file
// name varies with the packager), optional title/interest tags (from ads.json) and media
// duration.
type adEntry struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	MPDPath    string   `json:"mpdPath"`
	Title      string   `json:"title,omitempty"`
	Interests  []string `json:"interests,omitempty"`
	DurationMS int      `json:"durationMs"`
}

// adCatalog is the in-memory "database" of available SPS ad creatives with their tags and
// durations. It is built once from the vodroot (the discovered ad dirs + optional ads.json +
// each ad's media duration) and used to make the ad-pod selection: steer by interest and fit
// the pod to the break duration.
type adCatalog struct {
	ads []adEntry
}

// adCatalog returns the server's ad catalog, building and caching it on first use. The
// catalog is built once (ad creatives, like other VoD assets, are loaded at startup); restart
// to pick up newly added ads.
func (s *Server) adCatalog() *adCatalog {
	s.sgaiAdsMu.Lock()
	defer s.sgaiAdsMu.Unlock()
	if s.sgaiAds == nil {
		s.sgaiAds = buildAdCatalog(s.assetMgr.vodFS, s.adDurationMS)
	}
	return s.sgaiAds
}

// buildAdCatalog assembles the catalog from the vodroot: discover the SPS ad MPDs, merge the
// optional ads.json tags, and resolve each ad's duration via durFn (keyed on the MPD path).
func buildAdCatalog(vodFS fs.FS, durFn func(mpdPath string) int) *adCatalog {
	cat := &adCatalog{}
	mpdPaths, err := discoverAdCreatives(vodFS)
	if err != nil {
		return cat
	}
	meta := loadAdMeta(vodFS)
	for _, mp := range mpdPaths {
		adPath := path.Dir(mp)
		id := path.Base(adPath)
		md := meta[id]
		cat.ads = append(cat.ads, adEntry{
			ID:         id,
			Path:       adPath,
			MPDPath:    mp,
			Title:      md.Title,
			Interests:  md.Interests,
			DurationMS: durFn(mp),
		})
	}
	return cat
}

// selectPod chooses the ad pod for a request: ads tagged with any requested interest lead
// (each group rotated per session for variety), the rest fill, and the pod is then trimmed to
// fit maxDurMS (the break duration) — keeping at least the lead ad. maxDurMS <= 0 means no
// duration limit. An ad pod is only ever produced for an explicit interest match: with no
// interests — or interests that match no ad — it returns an empty pod, so the break is left
// unfilled and the viewer keeps the underlying break content (the AD BREAK slate, i.e. the
// base ad).
func (c *adCatalog) selectPod(sid string, interests []string, maxDurMS int) []adEntry {
	ordered := c.steerOrder(sid, interests)
	if maxDurMS <= 0 {
		return ordered
	}
	pod := make([]adEntry, 0, len(ordered))
	total := 0
	for i, a := range ordered {
		if i > 0 && a.DurationMS > 0 && total+a.DurationMS > maxDurMS {
			break
		}
		pod = append(pod, a)
		total += a.DurationMS
	}
	return pod
}

// steerOrder returns the catalog ads ordered for an interest-steered request: interest
// matches first, then the rest, each group rotated by session id. With no interests it
// returns an empty list — no ad pod is generated, so the viewer keeps the base AD BREAK
// slate. Interests that match nothing likewise return an empty list (no ad decision).
func (c *adCatalog) steerOrder(sid string, interests []string) []adEntry {
	if len(interests) == 0 {
		return nil
	}
	var matching, rest []adEntry
	for _, a := range c.ads {
		if adMetaMatchesAny(sgaiAdMeta{Interests: a.Interests}, interests) {
			matching = append(matching, a)
		} else {
			rest = append(rest, a)
		}
	}
	if len(matching) == 0 {
		return nil
	}
	return append(rotateBySid(matching, sid), rotateBySid(rest, sid)...)
}
