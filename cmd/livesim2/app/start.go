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

	if cfg.MaxRequests > 0 {
		ltr := NewIPRequestLimiter("Livesim2-Requests", cfg.MaxRequests, 24*time.Hour)
		l.Use(ltr)
		v.Use(ltr)
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
		assetMgr:   newAssetMgr(vodFS),
	}

	err = server.compileTemplates()
	if err != nil {
		return nil, err
	}

	err = server.Routes(ctx)
	if err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}

	err = server.assetMgr.discoverAssets()
	if err != nil {
		return nil, fmt.Errorf("findAssets: %w", err)
	}
	logger.Info().Int("count", len(server.assetMgr.assets)).Msg("VoD assets found")
	for name := range server.assetMgr.assets {
		a := server.assetMgr.assets[name]
		for mpdName := range a.MPDs {
			logger.Info().Str("assetPath", name).Str("mpdName", mpdName).Msg("VoD asset")
		}
	}

	logger.Info().Str("version", internal.GetVersion()).Int("port", cfg.Port).Msg("livesim2 starting")

	return &server, nil
}
