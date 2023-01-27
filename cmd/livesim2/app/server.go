package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

type Server struct {
	Router     *chi.Mux
	LiveRouter *chi.Mux
	VodRouter  *chi.Mux
	logger     *logging.Logger
	Cfg        *ServerConfig
	assetMgr   *assetMgr
}

func (s *Server) healthzHandlerFunc(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, true, http.StatusOK)
}

func (s *Server) GetLogger() *logging.Logger {
	return s.logger
}

// jsonResponse marshals message and give response with code
//
// Don't add any more content after this since Content-Length is set
func (s *Server) jsonResponse(w http.ResponseWriter, message interface{}, code int) {
	raw, err := json.Marshal(message)
	if err != nil {
		http.Error(w, fmt.Sprintf("{message: \"%s\"}", err), http.StatusInternalServerError)
		s.logger.Error().Msg(err.Error())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	_, err = w.Write(raw)
	if err != nil {
		s.logger.Error().
			Str("error", err.Error()).
			Msg("Could not write HTTP response")
	}
}
