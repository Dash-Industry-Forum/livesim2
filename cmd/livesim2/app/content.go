// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"embed"
	"net/http"
)

// HTML pages are rendered by compiled templ components (*.templ); only the
// text/template XML subtitle templates still need to be embedded here.
//
//go:embed static/* templates/*.xml
var content embed.FS

// embeddedStaticHandlerFunc handles static files in tree starting at static
func (s *Server) embeddedStaticHandlerFunc(w http.ResponseWriter, r *http.Request) {
	fs := http.FileServer(http.FS(content))
	fs.ServeHTTP(w, r)
}
