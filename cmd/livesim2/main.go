package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/cmd/livesim2/app"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

const (
	gracefulShutdownWait = 2 * time.Second
)

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cfg, err := app.LoadConfig(os.Args, cwd)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err.Error())
		os.Exit(1)
	}

	logger, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		logger.Fatal().Err(err).Send()
	}

	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	startIssue := make(chan struct{}, 1)
	stopServer := make(chan struct{}, 1)

	ctx, cancelBkg := context.WithCancel(context.Background())

	go func() {
		select {
		case <-startIssue:
		case <-stopSignal:
		}
		cancelBkg()
		stopServer <- struct{}{}
	}()

	server, err := app.SetupServer(ctx, cfg)
	if err != nil {
		_, prErr := fmt.Fprintf(os.Stderr, "Error setting up server: %s\n", err.Error())
		// If we are unable to log to stderr; try just printing the error to
		// provide some insight.
		if prErr != nil {
			fmt.Print(prErr)
		}
		return 1
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", server.Cfg.Port),
		Handler: server.Router,
	}

	go func() {
		var err error
		if cfg.CertPath != "" && cfg.KeyPath != "" { // HTTPS
			err = srv.ListenAndServeTLS(cfg.CertPath, cfg.KeyPath)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("")
			exitCode = 1
			startIssue <- struct{}{}
		}
	}()

	<-stopServer // Wait here for stop signal
	logger.Info().Msg("Server to be stopped")

	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		logger.Info().Msg("Server stopped")
		cancelTimeout()
		time.Sleep(gracefulShutdownWait)
	}()

	if err := srv.Shutdown(timeoutCtx); err != nil {
		logger.Error().Err(err).Msg("Server shutdown failed")
	}
	return exitCode
}
