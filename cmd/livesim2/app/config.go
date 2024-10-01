// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/knadh/koanf"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/providers/structs"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/spf13/pflag"
)

const (
	defaultReqIntervalS             = 24 * 3600
	defaultAvailabilityStartTimeS   = 0
	defaultAvailabilityTimeComplete = true
	defaultTimeShiftBufferDepthS    = 60
	defaultStartNr                  = 0
	timeShiftBufferDepthMarginS     = 10
	defaultTimeSubsDurMS            = 900
	defaultLatencyTargetMS          = 3500
	defaultPlayURL                  = "https://reference.dashif.org/dash.js/latest/samples/dash-if-reference-player/index.html?mpd=%s&autoLoad=true&muted=true"
)

type ServerConfig struct {
	LogFormat   string `json:"logformat"`
	LogLevel    string `json:"loglevel"`
	ReqLimitLog string `json:"reqlimitlog"`
	ReqLimitInt int    `json:"reqlimitint"` // in seconds
	Port        int    `json:"port"`
	LiveWindowS int    `json:"livewindowS"`
	TimeoutS    int    `json:"timeoutS"`
	MaxRequests int    `json:"maxrequests"`
	// WhiteListBlocks is a comma-separated list of CIDR blocks that are not rate limited
	WhiteListBlocks string `json:"whitelistblocks"`
	VodRoot         string `json:"vodroot"`
	// RepDataRoot is the root directory for representation metadata
	RepDataRoot string `json:"repdataroot"`
	// WriteRepData is true if representation metadata should be written (will override existing metadata)
	WriteRepData bool `json:"writerepdata"`
	// Domains is a comma-separated list of domains for Let's Encrypt
	Domains string `json:"domains"`
	// CertPath is a path to a valid TLS certificate
	CertPath string `json:"-"`
	// KeyPath is a path to a valid private TLS key
	KeyPath string `json:"-"`
	// If Host is set, it will be used instead of autodetected value scheme://host.
	Host string `json:"host"`
	// PlayURL is a URL template to play asset including player and pattern %s to be replaced by MPD URL
	// For autoplay, start the player muted.
	PlayURL string `json:"playurl"`
}

var DefaultConfig = ServerConfig{
	LogFormat:   "text",
	LogLevel:    "INFO",
	Port:        8888,
	LiveWindowS: 300,
	TimeoutS:    60,
	MaxRequests: 0,
	ReqLimitInt: defaultReqIntervalS,
	VodRoot:     "./vod",
	// MetaRoot + means follow VodRoot, _ means no metadata
	RepDataRoot:     "+",
	WriteRepData:    false,
	PlayURL:         defaultPlayURL,
	WhiteListBlocks: "",
}

type Config struct {
	Konf      *koanf.Koanf
	ServerCfg ServerConfig
}

// LoadConfig loads defaults, config file, command line, and finally applies environment variables
//
// VodRoot is set to cwd/root by default.
func LoadConfig(args []string, cwd string) (*ServerConfig, error) {
	// First set default values
	k := koanf.New(".")
	defaults := DefaultConfig
	err := k.Load(structs.Provider(defaults, "json"), nil)
	if err != nil {
		return nil, err
	}

	f := pflag.NewFlagSet("livesim2", pflag.ContinueOnError)
	f.Usage = func() {
		parts := strings.Split(args[0], "/")
		name := parts[len(parts)-1]
		fmt.Fprintf(os.Stderr, "Run as %s [options]:\n", name)
		f.PrintDefaults()

	}
	// Path to one or more config files to load into koanf along with some config params.
	cfgFile := f.String("cfg", "", "path to a JSON config file")
	f.Int("port", k.Int("port"), "HTTP port")
	lf := strings.Join(logging.LogFormats, ", ")
	f.String("logformat", k.String("logformat"), fmt.Sprintf("log format [%s]", lf))
	ll := strings.Join(logging.LogLevels, ", ")
	f.String("loglevel", k.String("loglevel"), fmt.Sprintf("log level [%s]", ll))
	f.Int("livewindow", k.Int("livewindowS"), "default live window (seconds)")
	f.String("vodroot", k.String("vodroot"), "VoD root directory")
	f.String("repdataroot", k.String("repdataroot"), `Representation metadata root directory. "+" copies vodroot value. "-" disables usage.`)
	f.Bool("writerepdata", k.Bool("writerepdata"), "Write representation metadata if not present")
	f.String("whitelistblocks", k.String("whitelistblocks"), "comma-separated list of CIDR blocks that are not rate limited")
	f.Int("timeout", k.Int("timeoutS"), "timeout for all requests (seconds)")
	f.Int("maxrequests", k.Int("maxrequests"), "max nr of request per IP address per 24 hours")
	f.String("reqlimitlog", k.String("reqlimitlog"), "path to request limit log file (only written if maxrequests > 0)")
	f.Int("reqlimitint", k.Int("reqlimitint"), "interval for request limit i seconds (only used if maxrequests > 0)")
	f.String("domains", k.String("domains"), "One or more DNS domains (comma-separated) for auto certificate from Let's Encrypt")
	f.String("certpath", k.String("certpath"), "path to TLS certificate file (for HTTPS). Use domains instead if possible")
	f.String("keypath", k.String("keypath"), "path to TLS private key file (for HTTPS). Use domains instead if possible.")
	f.String("scheme", k.String("scheme"), "scheme used in Location and BaseURL elements. If empty, it is attempted to be auto-detected")
	f.String("host", k.String("host"), "host (and possible prefix) used in MPD elements. Overrides auto-detected full scheme://host")
	f.String("playurl", k.String("playurl"), "URL template to play mpd. %s will be replaced by MPD URL")
	if err := f.Parse(args[1:]); err != nil {
		return nil, fmt.Errorf("command line parse: %w", err)
	}

	// Load the config files provided in the commandline.
	if *cfgFile != "" {
		cf := file.Provider(*cfgFile)
		if err := k.Load(cf, json.Parser()); err != nil {
			return nil, fmt.Errorf("load config file: %w", err)
		}
	}

	// Possibly override config file with commandline parameters
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		return nil, fmt.Errorf("parsing cli: %v", err)
	}

	// Overload with environment variables
	err = k.Load(env.Provider("LIVESIM_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "LIVESIM_")), "_", ".", -1)
	}), nil)
	if err != nil {
		return nil, err
	}

	err = checkTLSParams(k)
	if err != nil {
		return nil, err
	}

	// Make vodPath absolute in case it is not already
	vodRoot := k.String("vodroot")
	if vodRoot != "" && !path.IsAbs(vodRoot) {
		vodRoot = path.Join(cwd, vodRoot)
		err = k.Load(confmap.Provider(map[string]any{
			"vodroot": vodRoot,
		}, "."), nil)
		if err != nil {
			return nil, err
		}
	}
	// Update repDataRoot to consistent value including absolute path
	repDataRoot := k.String("repdataroot")
	switch repDataRoot {
	case "+":
		// Copy repdataroot value from vodroot
		repDataRoot = vodRoot
		err = k.Load(confmap.Provider(map[string]any{
			"repdataroot": repDataRoot,
		}, "."), nil)
		if err != nil {
			return nil, err
		}
	case "-":
		// Set repdataroot to empty string, and disable writerepdata
		repDataRoot = ""
		err = k.Load(confmap.Provider(map[string]any{
			"repdataroot": "",
		}, "."), nil)
		if err != nil {
			return nil, err
		}
		err = k.Load(confmap.Provider(map[string]any{
			"writerepdata": false,
		}, "."), nil)
		if err != nil {
			return nil, err
		}
	}
	if repDataRoot != "" && !path.IsAbs(repDataRoot) {
		repDataRoot = path.Join(cwd, repDataRoot)
		err = k.Load(confmap.Provider(map[string]any{
			"repdataroot": repDataRoot,
		}, "."), nil)
		if err != nil {
			return nil, err
		}
	}

	if k.String("domains") != "" {
		err = k.Load(confmap.Provider(map[string]any{
			"port": 443,
		}, "."), nil)
		if err != nil {
			return nil, err
		}
	}

	// Unmarshal into cfg
	var cfg ServerConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func checkTLSParams(k *koanf.Koanf) error {
	domains := k.String("domains")
	certPath := k.String("certpath")
	keyPath := k.String("keypath")
	switch {
	case domains != "":
		if certPath != "" || keyPath != "" {
			return fmt.Errorf("cannot use certpath and keypath together with Let's Encrypt domains")
		}
		return nil
	case certPath == "" && keyPath == "":
		return nil // HTTP
	case certPath != "" && keyPath != "":
		return nil // HTTPS
	default:
		return fmt.Errorf("certpath and keypath must both be empty or set")
	}
}
