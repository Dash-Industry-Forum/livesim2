package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/Dash-Industry-Forum/livesim2/cmd/dashfetcher/app"
	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	flag "github.com/spf13/pflag"
)

var usg = `Usage of %s:

%s downloads a DASH VoD asset and stores all files in an output directory.

The -o/--outdir option provides a directory for storing the downloaded MPD and segments.
The -a/--auto option adds output subdirectories fromn the URL removing common prefix parts.

Some possible resources are available at https://cta-wave.github.io/Test-Content/.
To download one of them, try 

$ %s -a https://cta-wave.github.io/Test-Content/https://dash.akamaized.net/WAVE/vectors/cfhd_sets/12.5_25_50/t1/2022-10-17/stream.mpd
`

func parseOptions() *app.Options {
	name := os.Args[0]
	o := app.Options{}
	flag.StringVarP(&o.OutDir, "outdir", "o", ".", "output directory")
	flag.BoolVarP(&o.AutoOutDir, "auto", "a", false, "automatically add output directory parts from URL")
	logFormatUsage := fmt.Sprintf("format and type of log: %v", logging.LogFormatsCommandLine)
	flag.StringVarP(&o.LogFile, "logfile", "l", "", "log file [default stdout]")
	flag.StringVarP(&o.LogFormat, "logformat", "", "consolepretty", logFormatUsage)
	flag.StringVarP(&o.LogLevel, "loglevel", "", "info", "initial log level")
	flag.BoolVarP(&o.Version, "version", "v", false, "print version and date")
	longHelp := flag.Bool("help", false, "extended tool help")
	flag.BoolVarP(&o.Force, "force", "f", false, "force overwrite of existing files")
	flag.CommandLine.SortFlags = false // keep help output order as declared

	flag.Usage = func() {
		parts := strings.Split(name, "/")
		name := parts[len(parts)-1]
		if *longHelp {
			fmt.Fprintf(os.Stderr, usg, name, name, name)
		}
		fmt.Fprintf(os.Stderr, usg, name, name, name)
		fmt.Fprintf(os.Stderr, "\nRun as %s [options] mpdURL\n\n", name)
		flag.PrintDefaults()
		os.Exit(2)
	}

	flag.Parse()
	internal.CheckVersion(o.Version)

	if len(flag.Args()) != 1 {
		flag.Usage()
	}

	o.AssetURL = flag.Args()[0]

	return &o
}

func main() {
	o := parseOptions()

	_, err := logging.InitZerolog(o.LogLevel, o.LogFormat)

	if err != nil {
		log.Fatal().Err(err).Send()
	}

	if o.OutDir == "." {
		o.OutDir, err = os.Getwd()
		if err != nil {
			log.Fatal().Err(err).Send()
		}
	}

	if o.AutoOutDir {
		o.OutDir, err = app.AutoDir(o.AssetURL, o.OutDir)
		if err != nil {
			log.Fatal().Err(err).Send()
		}
		log.Info().Str("output dir", o.OutDir).Msg("automatic output dir for MPD")
	}

	err = app.Fetch(o)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
}
