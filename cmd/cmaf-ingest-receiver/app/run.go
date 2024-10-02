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
	"github.com/caddyserver/certmagic"
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

The availabilityTimeOffset is extracted from the init segment if later than or equal
to 1970-01-01. If not, the availabilityTimeOffset is set to 0 (interpreted as 1970-01-01).

MPDs can also be received, but will just be stored as storage/channel/received.mpd.
DELETE requests are accepted, but not used, since there is a build in max buffer time
resulting in a maximum number of segments stored. The time is reflected in the
timeShiftBufferDepth in the MPD.

A DASH MPD with $Number$ is generated for each channel and stored as storage/channel/manifest.mpd.
The track name is mapped to RepresentationID in the MPD.

In addition, once two video segments of the same duration have been received, a SegmentTimelineNr
MPD is generated and stored as storage/channel/timelineNr.mpd. This MPD has a SegmentTimeline
with $Number$ addressing and is updated for full segment arrival.

There are some configuration on channel level that can be set in a config file depending
on channel name. The main parts are:

1. timeShiftBufferDepthS: The timeShiftBufferDepth in seconds for circularBuffer. Default is 90s.
2. startNr: The start number for the first segment. Default is 0. (AWS MediaLive starts at 1)
3. authUser and authPassword: If set, all requests must have this user and password (basic auth)
4. timeShiftBufferDepthS: The timeShiftBufferDepth in seconds for circularBuffer.

Furthermore, there is a configuration on representation level for a channel.
The main keys available are:

1. language: The language of the representation
2. role: The role of the representation
3. displayName: The display name of the representation
4. bitrate: The bitrate of the representation
`

type Options struct {
	port                  int
	portHttps             int
	storage               string
	prefix                string
	logLevel              string
	logFormat             string
	configFile            string
	domains               string
	certPath              string
	keyPath               string
	fileServerPath        string
	timeShiftBufferDepthS uint64
	receiveNrRawSegments  uint64
	version               bool
}

const (
	defaultStorageRoot           = "./storage"
	defaultPort                  = 8080
	defaultPortHttps             = 443
	defaultLogLevel              = "info"
	defaultLogFormat             = "text"
	defaultPrefix                = "/upload"
	defaultTimeShiftBufferDepthS = 90
	gracefulShutdownWait         = 2 * time.Second
)

func ParseOptions() (*Options, error) {
	var opts Options
	flag.IntVar(&opts.port, "port", defaultPort, "HTTP receiver port")
	flag.IntVar(&opts.portHttps, "porthttps", defaultPortHttps, "CMAF HTTPS receiver port")
	flag.StringVar(&opts.domains, "domains", "", "One or more DNS domains (comma-separated) for auto certificate from Let's Encrypt")
	flag.StringVar(&opts.certPath, "certpath", "", "Path to TLS certificate file (for HTTPS). Use domains instead if possible")
	flag.StringVar(&opts.keyPath, "keypath", "", "Path to TLS private key file (for HTTPS). Use domains instead if possible.")
	flag.StringVar(&opts.storage, "storage", defaultStorageRoot, "Storage root directory")
	flag.StringVar(&opts.prefix, "prefix", defaultPrefix, "Prefix to remove from upload URLS")
	flag.StringVar(&opts.logLevel, "loglevel", defaultLogLevel, "Log level (info, debug, warn)")
	flag.StringVar(&opts.logFormat, "logformat", defaultLogFormat, "Log format (text, json)")
	flag.Uint64Var(&opts.timeShiftBufferDepthS, "tsbd", defaultTimeShiftBufferDepthS, "Default timeShiftBufferDepth in seconds")
	flag.Uint64Var(&opts.receiveNrRawSegments, "recRawNr", 0, "Default number of raw segments to receive and store (turns off all parsing)")
	flag.StringVar(&opts.configFile, "config", "", "Config file with channel-specific settings")
	flag.StringVar(&opts.fileServerPath, "fileserverpath", "", "HTTP path for generated segments and manifests (for testing)")
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
	cfg := GetEmptyConfig()
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

	router := setupRouter(receiver, opts.storage, opts.fileServerPath)

	var http_srv *http.Server

	if opts.domains == "" && (opts.certPath == "" || opts.keyPath == "") {
		http_srv = &http.Server{
			Addr:    fmt.Sprintf(":%d", opts.port),
			Handler: router,
		}
	}

	go func() {
		var err error

		switch {
		case opts.domains != "":
			domains := strings.Split(opts.domains, ",")
			slog.Info("Started receiving CMAF ingest ACME HTTPS server", "domains", domains)
			err = certmagic.HTTPS(domains, router)
		case opts.certPath != "" && opts.keyPath != "":
			slog.Info("Starting receiving CMAF ingest HTTPS server with explicit certpath and keypath",
				"port", opts.portHttps)
			err = http.ListenAndServeTLS(fmt.Sprintf(":%d", opts.portHttps),
				opts.certPath, opts.keyPath, router)
		default:
			slog.Info("Starting receiving CMAF ingest HTTP server", "port", opts.port)
			err = http_srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			slog.Default().Error(err.Error())
			startIssue <- err
		}
	}()

	err = <-stopServer // Wait here for stop signal
	if err != nil {
		return fmt.Errorf("server error: %w", err)
	}
	slog.Info("Server to be stopped")
	if http_srv != nil { // TODO. Nicer shutdown for https and ACME cases as well
		timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
		defer func() {
			slog.Info("Server stopped")
			cancelTimeout()
			time.Sleep(gracefulShutdownWait)
		}()

		if err := http_srv.Shutdown(timeoutCtx); err != nil {
			return fmt.Errorf("HTTP server shutdown failed: %w", err)
		}
	}
	return nil
}

func setupRouter(r *Receiver, storage, fileServerPath string) *chi.Mux {
	router := chi.NewRouter()
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)
	router.Use(addCorsHeaders)
	router.Put(fmt.Sprintf("%s/*", r.prefix), r.SegmentHandlerFunc)
	router.Post(fmt.Sprintf("%s/*", r.prefix), r.SegmentHandlerFunc)
	router.Delete(fmt.Sprintf("%s/*", r.prefix), r.DeleteHandlerFunc)
	if fileServerPath != "" {
		router.MethodFunc("GET", fmt.Sprintf("/%s/*", fileServerPath), makeDashHandlerFunc(storage))
		router.MethodFunc("HEAD", "/*", dashHandlerFunc)
		router.MethodFunc("OPTIONS", "/*", optionsHandlerFunc)
	}

	return router
}

func makeDashHandlerFunc(rootDir string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		rp := rctx.RoutePattern()
		pathPrefix := strings.TrimSuffix(rp, "/*")
		fs := http.StripPrefix(pathPrefix, http.FileServer(http.Dir(rootDir)))
		fs.ServeHTTP(w, r)
	}
}

// vodHandlerFunc handles static files in tree starting at dashRoot.
func dashHandlerFunc(w http.ResponseWriter, r *http.Request) {

}

// optionsHandlerFunc provides the allowed methods.
func optionsHandlerFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, GET, HEAD, POST")
	w.WriteHeader(http.StatusNoContent)
}

func addCorsHeaders(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("ew-cmaf-ingest", "v0.5")
		w.Header().Add("Access-Control-Allow-Origin", "*")
		w.Header().Add("Access-Control-Allow-Methods", "POST, GET, HEAD, OPTIONS")
		w.Header().Add("Access-Control-Allow-Headers", "Content-Type, Accept")
		w.Header().Add("Timing-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
