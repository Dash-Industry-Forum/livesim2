package logging

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestInitZerolog(t *testing.T) {
	logger, err := InitZerolog("debug", LogConsolePretty)
	require.NoError(t, err)
	require.Equal(t, zerolog.DebugLevel, zerolog.GlobalLevel())
	logger.Info().Msg("Should show 1")

	// For some reason output is not JSON-formatted when logging from within a test
	logger, err = InitZerolog("info", LogJSON)
	require.NoError(t, err)
	require.Equal(t, zerolog.InfoLevel, zerolog.GlobalLevel())
	logger.Info().Msg("Should show 2")

	logger, err = InitZerolog("warn", LogConsolePretty)
	require.NoError(t, err)
	require.Equal(t, zerolog.WarnLevel, zerolog.GlobalLevel())
	logger.Info().Msg("Should not show")

	_, err = InitZerolog("fish", LogJSON)
	require.Error(t, err)

	_, err = InitZerolog("debug", "party")
	require.Error(t, err)
}
