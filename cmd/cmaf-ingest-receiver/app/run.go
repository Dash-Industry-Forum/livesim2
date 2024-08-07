package app

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var usg = `Usage of %s:

%s receives streams of CMAF ingest segments sent to prefix/* and stores them at storage/*.

The name structures should be one of:
* prefix/channel/Streams(track.cmf?)
* prefix/channel/track/segmentID.cmf?
(with cmf? being CMAF file extension .cmfv, .cmfa, .cmft, .cmfm)

In the first case, the name is repeated all the time for all segments of the same track.
In the second case, the name is changed for each segment of the track.

In both cases, the names and paths of the generated files will be
* storage/channel/track/init.cmf?
* storage/channel/track/sequenceNr.cmf?

where sequenceNr is extracted from the mdhd box in the segment.
These numbers are assumed to increase by one for each segment.

A DASH MPD is generated for each channel and stored as storage/channel/manifest.mpd.
The track name is mapped to RepresentationID in the MPD.

MPDs can also be received, but will just be stored as storage/channel/received.mpd.
DELETE requests are accepted, but not used, since there is a build in max buffer time
resulting in a maximum number of segments stored. The time is reflected in the
timeShiftBufferDepth in the MPD.

The availabilityTimeOffset is extracted from the init segment if later than or equal
to 1970-01-01. If not, the availabilityTimeOffset is set to 0n (interpreted as 1970-01-01).

There are some configuration on channel level that can be set in a config file depending
on channel name:

1. timeShiftBufferDepthS: The timeShiftBufferDepth in seconds for circularBuffer. Default is 90s.
2. startNr: The start number for the first segment. Default is 0. (AWS MediaLive starts at 1)
`

type Options struct {
	port                  int
	storage               string
	prefix                string
	logLevel              string
	logFormat             string
	configFile            string
	timeShiftBufferDepthS int
	version               bool
}

const (
	defaultStorageRoot           = "./storage"
	defaultPort                  = 8080
	defaultLogLevel              = "info"
	defaultLogFormat             = "text"
	defaultPrefix                = "/upload"
	defaultTimeShiftBufferDepthS = 90
	gracefulShutdownWait         = 2 * time.Second
)

func ParseOptions() (*Options, error) {
	var opts Options
	flag.IntVar(&opts.port, "port", defaultPort, "HTTP receiver port")
	flag.StringVar(&opts.storage, "storage", defaultStorageRoot, "Storage root directory")
	flag.StringVar(&opts.prefix, "prefix", defaultPrefix, "Prefix to remove from upload URLS")
	flag.StringVar(&opts.logLevel, "loglevel", defaultLogLevel, "Log level (info, debug, warn)")
	flag.StringVar(&opts.logFormat, "logformat", defaultLogFormat, "Log format (text, json)")
	flag.IntVar(&opts.timeShiftBufferDepthS, "tsbd", defaultTimeShiftBufferDepthS, "Default timeShiftBufferDepth in seconds")
	flag.StringVar(&opts.configFile, "config", "", "Config file with channel-specific settings")
	flag.BoolVar(&opts.version, "version", false, "Get version")

	flag.Usage = func() {
		parts := strings.Split(os.Args[0], "/")
		name := parts[len(parts)-1]
		fmt.Fprintf(os.Stderr, usg, name, name)
		fmt.Fprintf(os.Stderr, "\nRun as: %s options with options:\n\n", name)
		flag.PrintDefaults()
	}

	flag.Parse()
	return &opts, nil
}

func Run(opts *Options) error {
	if opts.version {
		internal.PrintVersion()
		return nil
	}
	err := logging.InitSlog(opts.logLevel, opts.logFormat)
	if err != nil {
		return fmt.Errorf("failed to init logging: %w", err)
	}
	var cfg *Config
	if opts.configFile != "" {
		cfg, err = readConfig(opts.configFile)
		if err != nil {
			return fmt.Errorf("failed to read config %s: %w", opts.configFile, err)
		}
	}

	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancelBkg := context.WithCancel(context.Background())
	startIssue := make(chan error, 1)
	stopServer := make(chan error, 1)

	receiver, err := NewReceiver(ctx, opts, cfg)
	if err != nil {
		cancelBkg()
		return fmt.Errorf("failed to create receiver: %w", err)
	}

	go func() {
		var err error
		select {
		case err = <-startIssue:
			slog.Error("server start issue", "err", err)
		case <-stopSignal:
		}
		cancelBkg()
		stopServer <- err
	}()

	router := setupRouter(receiver)

	http_srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", opts.port),
		Handler: router,
	}

	go func() {
		slog.Info("Server started", "port", opts.port)
		err := http_srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			slog.Error("Server issue", "err", err)
			startIssue <- err
		}
	}()

	err = <-stopServer // Wait here for stop signal
	if err != nil {
		return fmt.Errorf("server errorr: %w", err)
	}
	slog.Info("Server to be stopped")
	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
	defer func() {
		slog.Info("Server stopped")
		cancelTimeout()
		time.Sleep(gracefulShutdownWait)
	}()

	if err := http_srv.Shutdown(timeoutCtx); err != nil {
		return fmt.Errorf("HTTP server shutdown failed: %w", err)
	}
	return nil
}

func setupRouter(r *Receiver) *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Put(fmt.Sprintf("%s/*", r.prefix), r.SegmentHandlerFunc)
	router.Post(fmt.Sprintf("%s/*", r.prefix), r.SegmentHandlerFunc)
	router.Delete(fmt.Sprintf("%s/*", r.prefix), r.DeleteHandlerFunc)
	return router
}
