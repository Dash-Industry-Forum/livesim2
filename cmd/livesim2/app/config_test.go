// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"os"
	"path"
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
	osArgs := []string{"/path/livesim2", "--loglevel", "debug", "--domains", "livesim2.dashif.org",
		"--drmcfgfile", "/testdata/drm_config.json"}
	cfg, err := LoadConfig(osArgs, "/root")
	assert.NoError(t, err)
	c := DefaultConfig
	c.VodRoot = "/root/vod"
	c.RepDataRoot = c.VodRoot
	c.LogLevel = "debug"
	c.Port = 443
	c.Domains = "livesim2.dashif.org"
	c.DrmCfgFile = "/testdata/drm_config.json"
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

func TestSgaiDemoDir(t *testing.T) {
	cwd := t.TempDir()
	assert.NoError(t, os.Mkdir(path.Join(cwd, "demo"), 0o755))
	assert.NoError(t, os.WriteFile(path.Join(cwd, "demo", "index.html"), []byte("<html></html>"), 0o644))
	// A relative directory is resolved against the working directory.
	cfg, err := LoadConfig([]string{"/path/livesim2", "--sgaidemodir", "demo"}, cwd)
	assert.NoError(t, err)
	assert.Equal(t, path.Join(cwd, "demo"), cfg.SgaiDemoDir)
	// A directory without index.html is an error, so that a wrong path is noticed at startup.
	_, err = LoadConfig([]string{"/path/livesim2", "--sgaidemodir", "missing"}, cwd)
	assert.Error(t, err)
}
