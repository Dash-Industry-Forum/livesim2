package app

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/internal"
	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var usg = `Usage of %s:

%s receives streams of CMAF ingest segments sent to prefix/* and stores them at storage/*.

The name structures should be one of:
* prefix/asset/Streams(track.cmf?)
* prefix/asset/track/segmentID.cmf?
(with cmf? being CMAF file extension .cmfv, .cmfa, .cmft, .cmfm)

In the first case, the name is repeated all the time for all segments of the same track.
In the second case, the name is changed for each segment of the track.

In both cases, the names and paths of the generated files will be
* storage/asset/track/init.cmf?
* storage/asset/track/sequenceNr.cmf?

where seguenceNr is extraced from the mdhd box in the segment.
These numbers are assumed increase by one for each segment.

A DASH MPD is generated for each asset and stored as storage/asset/manifest.mpd.
The track name is mapped to RepresentationID in the MPD.

MPDs can also be receieved, but will just be stored as storage/asset/received.mpd.
DELETE requests are accepted, but not used, since there is a build in max buffer time
resulting in a maximum number of segments stored. The time is reflected in the
timeShiftBufferDepth in the MPD.
`

type Options struct {
	port       int
	storage    string
	prefix     string
	logLevel   string
	logFormat  string
	maxBufferS int
	version    bool
}

const (
	defaultStorageRoot = "./storage"
	defaultPort        = 8080
	defaultLogLevel    = "info"
	defaultLogFormat   = "text"
	defaultPrefix      = "/upload"
	defaultMaxBufferS  = 90
)

func ParseOptions() (*Options, error) {
	var opts Options
	flag.IntVar(&opts.port, "port", defaultPort, "HTTP receiver port")
	flag.StringVar(&opts.storage, "storage", defaultStorageRoot, "Storage root directory")
	flag.StringVar(&opts.prefix, "prefix", defaultPrefix, "Prefix to remove from upload URLS")
	flag.StringVar(&opts.logLevel, "loglevel", defaultLogLevel, "Log level (info, debug, warn)")
	flag.StringVar(&opts.logFormat, "logformat", defaultLogFormat, "Log format (text, json)")
	flag.IntVar(&opts.maxBufferS, "maxbuffer", defaultMaxBufferS, "Max cyclic buffer duration in seconds")
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

func Run() error {
	opts, err := ParseOptions()
	if err != nil {
		flag.Usage()
		return fmt.Errorf("failed to parse options: %w", err)
	}
	if opts.version {
		internal.PrintVersion()
		return nil
	}
	err = logging.InitSlog(opts.logLevel, opts.logFormat)
	if err != nil {
		return fmt.Errorf("failed to init logging: %w", err)
	}
	r := chi.NewRouter()
	r.Use(middleware.Logger)

	receiver, err := NewReceiver(opts)
	if err != nil {
		return fmt.Errorf("failed to create receiver: %w", err)
	}

	r.Put(fmt.Sprintf("%s/*", opts.prefix), receiver.SegmentHandlerFunc)
	r.Post(fmt.Sprintf("%s/*", opts.prefix), receiver.SegmentHandlerFunc)
	r.Delete(fmt.Sprintf("%s/*", opts.prefix), receiver.DeleteHandlerFunc)

	slog.Info("Starting server", "port", opts.port)
	err = http.ListenAndServe(fmt.Sprintf(":%d", opts.port), r)
	if err != nil {
		return err
	}
	return nil
}
