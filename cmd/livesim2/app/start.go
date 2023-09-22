// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// SetupServer sets up router, middleware, and server, given koanf configuration.
func SetupServer(ctx context.Context, cfg *ServerConfig) (*Server, error) {
	var err error

	logger := logging.GetGlobalLogger()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(logging.ZerologMiddleware(logger))
	r.Use(middleware.Recoverer)
	r.Use(addVersionAndCORSHeaders)
	prometheusMiddleWare := NewPrometheusMiddleware()

	l := chi.NewRouter()
	r.Use(prometheusMiddleWare)

	v := chi.NewRouter()
	r.Use(prometheusMiddleWare)

	// Set a timeout value on the request context (ctx), that will signal
	// through ctx.Done() that the request has timed out and further
	// processing should be stopped.
	if cfg.TimeoutS > 0 {
		r.Use(middleware.Timeout(time.Duration(cfg.TimeoutS) * time.Second))
	}

	// Add prometheus counters
	r.Mount("/metrics", promhttp.Handler())

	var reqLimiter *IPRequestLimiter
	if cfg.MaxRequests > 0 {
		reqLimiter = NewIPRequestLimiter(cfg.MaxRequests, time.Duration(cfg.ReqLimitInt)*time.Second, time.Now(), cfg.ReqLimitLog)
		ltrMw := NewLimiterMiddleware("Livesim2-Requests", reqLimiter)
		l.Use(ltrMw)
		v.Use(ltrMw)
	}

	// Mount livesim and vod routers
	r.Mount("/livesim2", l)
	r.Mount("/vod", v)

	vodFS := os.DirFS(cfg.VodRoot)
	server := Server{
		Router:     r,
		LiveRouter: l,
		VodRouter:  v,
		logger:     logger,
		Cfg:        cfg,
		assetMgr:   newAssetMgr(vodFS, cfg.RepDataRoot, cfg.WriteRepData),
		reqLimiter: reqLimiter,
	}

	err = server.compileTemplates()
	if err != nil {
		return nil, err
	}

	err = server.Routes(ctx)
	if err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}

	start := time.Now()
	err = server.assetMgr.discoverAssets()
	if err != nil {
		return nil, fmt.Errorf("findAssets: %w", err)
	}
	elapsedSeconds := fmt.Sprintf("%.3fs", time.Since(start).Seconds())

	logger.Info().Int("count", len(server.assetMgr.assets)).Str("elapsed", elapsedSeconds).Msg("VoD assets found")
	for name := range server.assetMgr.assets {
		a := server.assetMgr.assets[name]
		for mpdName := range a.MPDs {
			logger.Info().Str("assetPath", name).Str("mpdName", mpdName).Msg("Available MPD")
		}
	}

	logger.Info().Str("version", internal.GetVersion()).Int("port", cfg.Port).Msg("livesim2 starting")

	return &server, nil
}
