// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"embed"
	"net/http"
)

//go:embed static/* templates/*
var content embed.FS

// embeddedStaticHandlerFunc handles static files in tree starting at static
func (s *Server) embeddedStaticHandlerFunc(w http.ResponseWriter, r *http.Request) {
	fs := http.FileServer(http.FS(content))
	fs.ServeHTTP(w, r)
}
