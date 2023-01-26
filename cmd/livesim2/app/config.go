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
	defaultAvailabilityStartTimeS   = 0
	defaultAvailabilityTimeComplete = true
	defaultTimeShiftBufferDepthS    = 60
	defaultStartNr                  = 0
	timeShiftBufferDepthMarginS     = 10
)

type ServerConfig struct {
	LogFormat   string `json:"logformat"`
	LogLevel    string `json:"loglevel"`
	Port        int    `json:"port"`
	LiveWindowS int    `json:"livewindowS"`
	TimeoutS    int    `json:"timeoutS"`
	VodRoot     string `json:"vodroot"`
}

var DefaultConfig = ServerConfig{
	LogFormat:   "consolepretty",
	LogLevel:    "info",
	Port:        8888,
	LiveWindowS: 300,
	TimeoutS:    60,
	VodRoot:     "./vod",
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
	k.Load(structs.Provider(defaults, "json"), nil)

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
	f.Int("timeout", k.Int("timeoutS"), "timeout for all requests (seconds)")
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
	k.Load(env.Provider("LIVESIM_", ".", func(s string) string {
		return strings.Replace(strings.ToLower(
			strings.TrimPrefix(s, "LIVESIM_")), "_", ".", -1)
	}), nil)

	// Make vodPath absolute in case it is not already
	vodRoot := k.String("vodroot")
	if vodRoot != "" && !path.IsAbs(vodRoot) {
		vodRoot = path.Join(cwd, vodRoot)
		k.Load(confmap.Provider(map[string]any{
			"vodroot": vodRoot,
		}, "."), nil)
	}

	// Unmarshal into cfg
	var cfg ServerConfig
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
