// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"io/fs"
	"net/http"
	"strconv"
)

// indexHandlerFunc handles access to /.
func (s *Server) indexHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	err := s.htmlTemplates.ExecuteTemplate(w, "welcome.html", s.Cfg.URLPrefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// favIconFunc returns the DASH-IF favicon.
func (s *Server) favIconFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	b, err := fs.ReadFile(content, "static/favicon.ico")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// optionsHandlerFunc provides the allowed methods.
func (s *Server) optionsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, GET, POST, PUT")
}
