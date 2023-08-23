// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"net/http"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// redirect returns an HTTP redirect with "from" replaced by "to" in URL.
func redirect(from, to string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.Replace(r.URL.Path, from, to, 1)
		http.Redirect(w, r, r.URL.String(), http.StatusFound)
	}
}

// Routes defines dispatches for all routes.
func (s *Server) Routes(ctx context.Context) error {
	for _, route := range logging.LogRoutes {
		s.Router.MethodFunc(route.Method, route.Path, route.Handler)
	}
	s.Router.MethodFunc("GET", "/healthz", s.healthzHandlerFunc)
	s.Router.MethodFunc("GET", "/favicon.ico", s.favIconFunc)
	s.Router.MethodFunc("GET", "/config", s.configHandlerFunc)
	s.Router.MethodFunc("GET", "/assets", s.assetsHandlerFunc)
	s.Router.MethodFunc("GET", "/vod", s.assetsHandlerFunc)
	s.Router.MethodFunc("GET", "/static/*", s.embeddedStaticHandlerFunc)
	s.Router.MethodFunc("HEAD", "/static/*", s.embeddedStaticHandlerFunc)
	// LiveRouter is mounted at /livesim2
	s.LiveRouter.MethodFunc("GET", "/*", s.livesimHandlerFunc)
	s.LiveRouter.MethodFunc("HEAD", "/*", s.livesimHandlerFunc)
	// VodRouter is mounted at /vod
	s.VodRouter.MethodFunc("GET", "/*", s.vodHandlerFunc)
	s.VodRouter.MethodFunc("HEAD", "/*", s.vodHandlerFunc)
	s.Router.MethodFunc("OPTIONS", "/*", s.optionsHandlerFunc)
	s.Router.MethodFunc("GET", "/", s.indexHandlerFunc)
	// Redirect /livesim to /livesim2 and /livesim-chunked for backwards compatibility
	s.Router.MethodFunc("GET", "/livesim/*", redirect("/livesim", "/livesim2"))
	s.Router.MethodFunc("GET", "/livesim-chunked/*", redirect("/livesim-chunked", "/livesim2"))
	// Redirect /dash/vod to /vod for backwards compatibility
	s.Router.MethodFunc("GET", "/dash/vod/*", redirect("/dash/vod", "/vod"))
	s.Router.MethodFunc("HEAD", "/dash/vod/*", redirect("/dash/vod", "/vod"))

	return nil
}
