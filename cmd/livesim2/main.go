// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/caddyserver/certmagic"

	"github.com/Dash-Industry-Forum/livesim2/cmd/livesim2/app"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
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
		if strings.Contains(err.Error(), "help requested") {
			return 0
		}
		_, _ = fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err.Error())
		os.Exit(1)
	}

	err = logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error initializing logging: %s\n", err.Error())
		os.Exit(1)
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

	go func() {
		var err error

		switch {
		case cfg.Domains != "":
			domains := strings.Split(cfg.Domains, ",")
			err = certmagic.HTTPS(domains, server.Router)
		case cfg.CertPath != "" && cfg.KeyPath != "":
			err = http.ListenAndServeTLS(fmt.Sprintf(":%d", server.Cfg.Port), cfg.CertPath, cfg.KeyPath, server.Router)
		default:
			err = http.ListenAndServe(fmt.Sprintf(":%d", server.Cfg.Port), server.Router)
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Default().Error(err.Error())
			exitCode = 1
			startIssue <- struct{}{}
		}
	}()

	<-stopServer // Wait here for stop signal
	slog.Default().Info("Server  stopped")

	return exitCode
}
