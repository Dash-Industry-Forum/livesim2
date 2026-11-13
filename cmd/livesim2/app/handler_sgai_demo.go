// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"net/http"
)

// sgaiDemoPath is where the SGAI demo page (the --sgaidemodir directory) is served.
const sgaiDemoPath = "/sgai/demo"

// sgaiDemoHandlerFunc serves the files of the SGAI demo directory. They are served from disk,
// not embedded, so the page can be changed without a new build. Cache-Control no-cache makes
// browsers revalidate on every load, so a changed page shows up on the next reload instead of
// a heuristically cached old copy.
func (s *Server) sgaiDemoHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	http.StripPrefix(sgaiDemoPath, http.FileServer(http.Dir(s.Cfg.SgaiDemoDir))).ServeHTTP(w, r)
}
