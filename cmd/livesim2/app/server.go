// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	htmpl "html/template"
	ttmpl "text/template"

	_ "net/http/pprof"
)

type Server struct {
	Router        *chi.Mux
	LiveRouter    *chi.Mux
	VodRouter     *chi.Mux
	Cfg           *ServerConfig
	assetMgr      *assetMgr
	cmafMgr       *cmafIngesterMgr
	textTemplates *ttmpl.Template
	htmlTemplates *htmpl.Template
	reqLimiter    *IPRequestLimiter
}

func (s *Server) healthzHandlerFunc(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, true, http.StatusOK)
}

// jsonResponse marshals message and give response with code
//
// Don't add any more content after this since Content-Length is set
func (s *Server) jsonResponse(w http.ResponseWriter, message any, code int) {
	raw, err := json.Marshal(message)
	if err != nil {
		http.Error(w, fmt.Sprintf("{message: \"%s\"}", err), http.StatusInternalServerError)
		slog.Error(err.Error())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	_, err = w.Write(raw)
	if err != nil {
		slog.Error("could not write HTTP response", "err", err)
	}
}

func (s *Server) compileTemplates() error {
	var err error
	s.textTemplates, err = compileTextTemplates(content, "templates")
	if err != nil {
		return fmt.Errorf("compileTextTemplates: %w", err)
	}
	slog.Debug("text templates", "defined", s.textTemplates.DefinedTemplates())
	s.htmlTemplates, err = compileHTMLTemplates(content, "templates")
	if err != nil {
		return fmt.Errorf("compileHTMLTemplates: %w", err)
	}
	slog.Debug("html templates", "defined", s.htmlTemplates.DefinedTemplates())

	return nil
}
