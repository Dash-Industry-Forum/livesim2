//go:build windows
// +build windows

package logging

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// InitZerolog initializes the global zerolog logger.
//
// level and logLevel determine where the logs go and what format is used.
func InitZerolog(level string, logFormat string) (*Logger, error) {

	if !isValidLogFormat(logFormat) {
		msg := fmt.Sprintf("Unknown log format: %q", logFormat)
		err := errors.New(msg)
		return nil, err
	}

	switch logFormat {
	case LogJSON:
		log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	case LogConsolePretty:
		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
			With().Timestamp().Logger()
	case LogJournald:
		return nil, fmt.Errorf("journald logging not supported on Windows")
	case LogDiscard:
		log.Logger = zerolog.New(io.Discard)
	default:
		return nil, fmt.Errorf("logFormat %q not known", logFormat)
	}

	err := SetLogLevel(level)
	if err != nil {
		return nil, err
	}

	return &log.Logger, nil
}
