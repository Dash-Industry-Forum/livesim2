package app

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/Dash-Industry-Forum/livesim2/internal"
)

// indexHandlerFunc handles access to /.
func (s *Server) indexHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = fmt.Fprintf(w, "Welcome to livesim2!")
}

// favIconFunc returns the DASH-IF favicon.
func (s *Server) favIconFunc(w http.ResponseWriter, r *http.Request) {
	//w.Header().Set("Content-Type", "image/x-icon")
	b := internal.GetFavIcon()
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(internal.GetFavIcon())
}

// optionsHandlerFunc provides the allowed methods.
func (s *Server) optionsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, GET, POST, PUT")
}
