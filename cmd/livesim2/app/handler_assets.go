package app

import (
	"encoding/json"
	"net/http"
	"sort"
)

// assetHandlerFunc returns information about assets
func (s *Server) assetsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	assets := make([]*asset, 0, len(s.assetMgr.assets))
	for _, a := range s.assetMgr.assets {
		assets = append(assets, a)
	}
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].AssetPath < assets[j].AssetPath
	})
	body, err := json.MarshalIndent(assets, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
