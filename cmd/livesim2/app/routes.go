// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

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
	s.Router.MethodFunc("GET", "/static/*", s.embeddedStaticHandlerFunc)
	// LiveRouter is mounted at /livesim2
	s.LiveRouter.MethodFunc("GET", "/*", s.livesimHandlerFunc)
	s.LiveRouter.MethodFunc("HEAD", "/*", s.livesimHandlerFunc)
	// VodRouter is mounted at /vod
	s.VodRouter.MethodFunc("GET", "/*", s.vodHandlerFunc)
	s.VodRouter.MethodFunc("HEAD", "/*", s.vodHandlerFunc)
	s.Router.MethodFunc("OPTIONS", "/*", s.optionsHandlerFunc)
	s.Router.MethodFunc("GET", "/", s.indexHandlerFunc)
	return nil
}
