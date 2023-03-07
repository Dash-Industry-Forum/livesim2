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
	fs := http.StripPrefix(pathPrefix, http.FileServer(http.Dir(vodRoot)))
	fs.ServeHTTP(w, r)
}
