// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/drm"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

// SetupServer sets up router, middleware, and server, given koanf configuration.
func SetupServer(ctx context.Context, cfg *ServerConfig) (*Server, error) {
	var err error

	logger := slog.Default()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(logging.SlogMiddleWare(logger))
	r.Use(middleware.Recoverer)
	prometheusMiddleWare := NewPrometheusMiddleware()
	r.Use(prometheusMiddleWare)
	r.Use(addVersionAndCORSHeaders)

	// Set a timeout value on the request context (ctx), that will signal
	// through ctx.Done() that the request has timed out and further
	// processing should be stopped.
	if cfg.TimeoutS > 0 {
		r.Use(middleware.Timeout(time.Duration(cfg.TimeoutS) * time.Second))
	}

	// Add prometheus counters
	r.Mount("/metrics", promhttp.Handler())

	var reqLimiter *IPRequestLimiter
	l := chi.NewRouter()
	v := chi.NewRouter()
	if cfg.MaxRequests > 0 {
		reqLimiter, err = NewIPRequestLimiter(cfg.MaxRequests, time.Duration(cfg.ReqLimitInt)*time.Second,
			time.Now(), cfg.WhiteListBlocks, cfg.ReqLimitLog)
		if err != nil {
			return nil, fmt.Errorf("newIPLimiter: %w", err)
		}
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
		Cfg:        cfg,
		assetMgr:   newAssetMgr(vodFS, cfg.RepDataRoot, cfg.WriteRepData),
		reqLimiter: reqLimiter,
	}

	r.Route("/api", createRouteAPI(&server))

	server.cmafMgr = NewCmafIngesterMgr(&server)

	err = server.compileTemplates()
	if err != nil {
		return nil, err
	}

	err = server.Routes(ctx)
	if err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}

	start := time.Now()
	logger.Debug("Loading VOD assets", "vodRoot", cfg.VodRoot)
	err = server.assetMgr.discoverAssets(logger)
	if err != nil {
		return nil, fmt.Errorf("findAssets: %w", err)
	}
	elapsedSeconds := fmt.Sprintf("%.3fs", time.Since(start).Seconds())

	logger.Info("Vod assets loaded",
		"vodRoot", cfg.VodRoot,
		"count", len(server.assetMgr.assets),
		"elapsed seconds", elapsedSeconds)
	for name := range server.assetMgr.assets {
		a := server.assetMgr.assets[name]
		for mpdName := range a.MPDs {
			logger.Info("Available MPD", "assetPath", name, "mpdName", mpdName)
		}
	}

	if cfg.DrmCfgFile != "" {
		drmCfg, err := drm.ReadDrmConfig(cfg.DrmCfgFile)
		if err != nil {
			return nil, fmt.Errorf("readDrmConfigs: %w", err)
		}
		logger.Info("DRM configurations loaded", "path", cfg.DrmCfgFile, "count", len(drmCfg.Packages))
		cfg.DrmCfg = drmCfg
	}

	logger.Info("livesim2 starting", "version", internal.GetVersion(), "port", cfg.Port)
	server.cmafMgr.Start()
	return &server, nil
}
