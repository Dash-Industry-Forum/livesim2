// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

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
	c.RepDataRoot = c.VodRoot
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
	extCfg.RepDataRoot = extCfg.VodRoot
	extCfg.PlayURL = defaultPlayURL
	extCfg.ReqLimitInt = defaultReqIntervalS
	assert.NoError(t, err)
	assert.Equal(t, extCfg, *cfg)

}

func TestCommandLine(t *testing.T) {
	osArgs := []string{"/path/livesim2", "--loglevel", "debug", "--domains", "livesim2.dashif.org"}
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	c := DefaultConfig
	c.VodRoot = "/root/vod"
	c.RepDataRoot = c.VodRoot
	c.LogLevel = "debug"
	c.Port = 443
	c.Domains = "livesim2.dashif.org"
	assert.Equal(t, c, *cfg)
}

func TestEnv(t *testing.T) {
	osArgs := []string{"/path/livesim2", "--loglevel", "debug"}
	t.Setenv("LIVESIM_LOGLEVEL", "warn")
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	c := DefaultConfig
	c.VodRoot = "/root/vod"
	c.RepDataRoot = c.VodRoot
	c.LogLevel = "warn"
	assert.Equal(t, c, *cfg)
}
