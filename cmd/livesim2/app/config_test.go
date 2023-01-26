package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaults(t *testing.T) {
	osArgs := []string{"/path/livesim2"}
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	c := DefaultConfig
	c.VodRoot = "/root/vod"
	assert.Equal(t, c, *cfg)
}

func TestConfigFile(t *testing.T) {
	cfgFile := "./testdata/configs/testvalues.json"
	osArgs := []string{"/path/livesim2", "--cfg", cfgFile}
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	var extCfg ServerConfig
	data, err := os.ReadFile(cfgFile)
	assert.NoError(t, err)
	err = json.Unmarshal(data, &extCfg)
	extCfg.VodRoot = "/vod2"
	assert.NoError(t, err)
	assert.Equal(t, extCfg, *cfg)

}

func TestCommandLine(t *testing.T) {
	osArgs := []string{"/path/livesim2", "--loglevel", "debug"}
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	c := DefaultConfig
	c.VodRoot = "/root/vod"
	c.LogLevel = "debug"
	assert.Equal(t, c, *cfg)
}

func TestEnv(t *testing.T) {
	osArgs := []string{"/path/livesim2", "--loglevel", "debug"}
	t.Setenv("LIVESIM_LOGLEVEL", "warn")
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	c := DefaultConfig
	c.VodRoot = "/root/vod"
	c.LogLevel = "warn"
	assert.Equal(t, c, *cfg)
}
