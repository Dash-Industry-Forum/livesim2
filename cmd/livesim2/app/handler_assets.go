// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type assetsInfo struct {
	Host    string
	PlayURL string
	Assets  []*assetInfo
}

type assetInfo struct {
	Path         string
	LoopDurMS    int
	MPDs         []mpdInfo
	PreEncrypted bool
}

type mpdInfo struct {
	Path string
	Desc string
	Dur  string
}

// assetHandlerFunc returns information about assets
func (s *Server) assetsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	forVod := strings.HasPrefix(r.URL.String(), "/vod")
	assets := make([]*asset, 0, len(s.assetMgr.assets))
	for _, a := range s.assetMgr.assets {
		assets = append(assets, a)
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].AssetPath < assets[j].AssetPath
	})
	fh := fullHost(s.Cfg.Host, r)
	playURL, err := createPlayURL(fh, s.Cfg.PlayURL)
	if err != nil {
		slog.Error("cannot create playurl")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	aInfo := assetsInfo{
		Host:    fh,
		PlayURL: playURL,
		Assets:  make([]*assetInfo, 0, len(assets)),
	}
	for _, asset := range assets {
		mpds := make([]mpdInfo, 0, len(asset.MPDs))
		for _, mpd := range asset.MPDs {
			mpds = append(mpds, mpdInfo{
				Path: mpd.Name,
				Desc: mpd.Title,
				Dur:  mpd.Dur,
			})
		}
		sort.Slice(mpds, func(i, j int) bool {
			return mpds[i].Path < mpds[j].Path
		})
		assetInfo := assetInfo{
			Path:         asset.AssetPath,
			LoopDurMS:    asset.LoopDurMS,
			MPDs:         mpds,
			PreEncrypted: asset.refRep.PreEncrypted,
		}
		aInfo.Assets = append(aInfo.Assets, &assetInfo)
	}
	w.Header().Set("Content-Type", "text/html")
	templateName := "assets.html"
	if forVod {
		templateName = "assets_vod.html"
	}
	err = s.htmlTemplates.ExecuteTemplate(w, templateName, aInfo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func schemePrefix(host string) string {
	schemePrefix := "http://"
	if strings.HasPrefix(host, "https://") {
		schemePrefix = "https://"
	}
	return schemePrefix
}

// createPlayURL returns a proxied URL for http and direct URL for https.
func createPlayURL(host, playURL string) (string, error) {
	schemePrefix := schemePrefix(host)
	if schemePrefix == "https://" {
		return playURL, nil
	}
	// Replace scheme + host with /player for proxying
	u, err := url.Parse(playURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse playurl %s: %w", playURL, err)
	}
	u.Scheme = ""
	u.Host = ""
	return "/player" + u.String(), nil
}
