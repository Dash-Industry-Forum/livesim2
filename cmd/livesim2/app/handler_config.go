package app

import (
	"encoding/json"
	"net/http"
)

// configHandler returns the global config parameters.
func (s *Server) configHandlerFunc(w http.ResponseWriter, r *http.Request) {
	body, err := json.MarshalIndent(s.Cfg, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
