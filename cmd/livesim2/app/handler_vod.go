// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// vodHandlerFunc handles static files in tred starting at vodRoot.
func (s *Server) vodHandlerFunc(w http.ResponseWriter, r *http.Request) {
	rctx := chi.RouteContext(r.Context())
	rp := rctx.RoutePattern()
	pathPrefix := strings.TrimSuffix(rp, "/*")
	vodRoot := s.Cfg.VodRoot
	// SGAI ad creatives (MPD + segments) must not be cached by browsers/proxies:
	// the assets may be replaced on disk between sessions, and http.FileServer's
	// Last-Modified would otherwise let a stale cached copy mask the new ad.
	if strings.HasPrefix(r.URL.Path, pathPrefix+"/"+sgaiAdsBaseDir+"/") {
		w.Header().Set("Cache-Control", "no-store")
	}
	fs := http.StripPrefix(pathPrefix, http.FileServer(http.Dir(vodRoot)))
	fs.ServeHTTP(w, r)
}
