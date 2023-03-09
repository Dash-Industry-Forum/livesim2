// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"net/http"
	"path"
	"sort"
)

type assetsInfo struct {
	MPDs   []string
	Assets []*asset
}

// assetHandlerFunc returns information about assets
func (s *Server) assetsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	assets := make([]*asset, 0, len(s.assetMgr.assets))
	for _, a := range s.assetMgr.assets {
		assets = append(assets, a)
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].AssetPath < assets[j].AssetPath
	})
	aInfo := assetsInfo{}
	mpds := make([]string, 0, len(assets))
	for _, asset := range assets {
		for m := range asset.MPDs {
			fullURL := path.Join(asset.AssetPath, m)
			mpds = append(mpds, fullURL)
		}
		aInfo.Assets = append(aInfo.Assets, asset)
	}
	sort.Strings(mpds)
	aInfo.MPDs = append(aInfo.MPDs, mpds...)
	body, err := json.MarshalIndent(aInfo, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
