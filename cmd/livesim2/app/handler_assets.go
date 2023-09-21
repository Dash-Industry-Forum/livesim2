// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"net/http"
	"sort"
	"strings"
)

type assetsInfo struct {
	Host    string
	PlayURL string
	Assets  []*assetInfo
}

type assetInfo struct {
	Path      string
	LoopDurMS int
	MPDs      []mpdInfo
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
	playURL := schemePrefix(fh) + s.Cfg.PlayURL
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
			Path:      asset.AssetPath,
			LoopDurMS: asset.LoopDurMS,
			MPDs:      mpds,
		}
		aInfo.Assets = append(aInfo.Assets, &assetInfo)
	}
	w.Header().Set("Content-Type", "text/html")
	templateName := "assets.html"
	if forVod {
		templateName = "assets_vod.html"
	}
	err := s.htmlTemplates.ExecuteTemplate(w, templateName, aInfo)
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
