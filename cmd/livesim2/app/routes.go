package app

import (
	"context"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// Routes defines dispatches for all routes.
func (s *Server) Routes(ctx context.Context) error {
	for _, route := range logging.LogRoutes {
		s.Router.MethodFunc(route.Method, route.Path, route.Handler)
	}
	s.Router.MethodFunc("GET", "/healthz", s.healthzHandlerFunc)
	s.Router.MethodFunc("GET", "/favicon.ico", s.favIconFunc)
	s.Router.MethodFunc("GET", "/config", s.configHandlerFunc)
	s.Router.MethodFunc("GET", "/assets", s.assetsHandlerFunc)
	s.LiveRouter.MethodFunc("GET", "/*", s.livesimHandlerFunc)
	s.LiveRouter.MethodFunc("HEAD", "/*", s.livesimHandlerFunc)
	s.VodRouter.MethodFunc("GET", "/*", s.vodHandlerFunc)
	s.VodRouter.MethodFunc("HEAD", "/*", s.vodHandlerFunc)
	s.Router.MethodFunc("OPTIONS", "/", s.optionsHandlerFunc)
	s.Router.MethodFunc("GET", "/", s.indexHandlerFunc)
	return nil
}
