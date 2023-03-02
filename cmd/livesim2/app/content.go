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
