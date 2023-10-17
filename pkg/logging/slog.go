// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.
package logging

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// Different types of logging
const (
	LogText    string = "text"
	LogJSON    string = "json"
	LogPretty  string = "pretty"
	LogDiscard string = "discard"
)

var logLevel *slog.LevelVar

// LogFormats returns the allowed log formats.
var LogFormats = []string{LogText, LogJSON, LogPretty, LogDiscard}

// LogLevels returns the allowed log levels.
var LogLevels = []string{"DEBUG", "INFO", "WARN", "ERROR"}

// LogLevel returns the current log level.
func LogLevel() string {
	l := logLevel.Level()
	return l.String()
}

// parseLevel parses a log level string. If the string is empty, INFO is assumed.
func parseLevel(level string) (slog.Level, error) {
	level = strings.ToUpper(level)
	switch level {
	case "DEBUG":
		return slog.LevelDebug, nil
	case "INFO", "":
		return slog.LevelInfo, nil
	case "WARN":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return slog.LevelDebug, fmt.Errorf("log level %q not known", level)
	}
}

// SetLogLevel sets the global log level
func SetLogLevel(level string) error {
	l, err := parseLevel(level)
	if err != nil {
		return err
	}
	logLevel.Set(l)
	return nil
}

// SlogMiddleWare logs access and converts panic to stack traces.
func SlogMiddleWare(l *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			startTime := time.Now()
			inPath := r.URL.Path

			defer func() {
				endTime := time.Now()

				// Recover and record stack traces in case of a panic
				if rec := recover(); rec != nil {
					l.Error("Runtime error (panic)",
						"request_id", GetRequestID(r),
						"recover_info", rec,
						"debug_stack", debug.Stack())
					http.Error(ww, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}

				latency_ms := fmt.Sprintf("%.3f", float64(endTime.Sub(startTime).Nanoseconds())/1000000.0)
				l2 := l.With(
					"request_id", GetRequestID(r),
					"remote_ip", r.RemoteAddr,
					"proto", r.Proto,
					"method", r.Method,
					"user_agent", r.Header.Get("User-Agent"),
					"status", ww.Status(),
					"latency_ms", latency_ms,
					"bytes_out", ww.BytesWritten())
				if inPath != r.URL.Path {
					l2 = l2.With("url", inPath, "location", r.URL.Path)
				} else {
					l2 = l2.With("url", r.URL.Path)
				}

				bytesIn := r.Header.Get("Content-Length")
				if bytesIn != "" {
					l2 = l2.With("bytes_in", bytesIn)
				}
				l2.Info("request")
				next.ServeHTTP(ww, r)
			}()
		}
		return http.HandlerFunc(fn)
	}
}

// GetRequestID returns the request ID.
func GetRequestID(r *http.Request) string {
	key := middleware.RequestIDKey
	requestID, ok := r.Context().Value(key).(string)
	if !ok {
		requestID = "-"
	}
	return requestID
}

// SubLoggerWithRequestID creates a new sub-logger with request_id field.
func SubLoggerWithRequestID(l *slog.Logger, r *http.Request) *slog.Logger {
	return l.With(slog.String("request_id", GetRequestID(r)))
}
