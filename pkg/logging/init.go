// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/dusted-go/logging/prettylog"
)

// InitSlog initializes the global slog logger.
//
// level and logLevel determine where the logs go and what format is used.
func InitSlog(level string, logFormat string) error {

	var logger *slog.Logger
	logLevel = new(slog.LevelVar)

	switch logFormat {
	case LogText:
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	case LogJSON:
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	case LogPretty:
		f := func(groups []string, a slog.Attr) slog.Attr { return a }
		prettyHandler := prettylog.NewHandler(&slog.HandlerOptions{
			Level:       logLevel,
			AddSource:   false,
			ReplaceAttr: f})
		logger = slog.New(prettyHandler)
	case LogDiscard:
		logger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: logLevel}))
	default:
		return fmt.Errorf("logFormat %q not known", logFormat)
	}
	slog.SetDefault(logger)
	return SetLogLevel(level)
}
