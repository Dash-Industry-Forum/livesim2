package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	defaultBuckets = []float64{5, 10, 20, 50, 100, 200, 500, 1000}
	prometheusMW   prometheusMiddleware
)

const (
	segmentReqsName    = "segment_requests_total"
	segmentLatencyName = "segment_request_duration_milliseconds"
	mpdReqsName        = "mpd_requests_total"
	mpdLatencyName     = "mpd_request_duration_milliseconds"
	otherReqsName      = "other_requests_total"
	otherLatencyName   = "other_request_duration_milliseconds"
)

// prometheusMiddleware provides a handler that exposes prometheus metrics for various requests
type prometheusMiddleware struct {
	segmentReqs    *prometheus.CounterVec
	segmentLatency *prometheus.HistogramVec
	mpdReqs        *prometheus.CounterVec
	mpdLatency     *prometheus.HistogramVec
	otherReqs      *prometheus.CounterVec
	otherLatency   *prometheus.HistogramVec
}

func init() {
	prometheusMW.segmentReqs = newCounter(segmentReqsName,
		"Number segment requests processed, partitioned by status code.", "livesim2")
	prometheusMW.segmentLatency = newHistogram(segmentLatencyName,
		"segment response latency.", "livesim2", defaultBuckets)
	prometheusMW.mpdReqs = newCounter(mpdReqsName,
		"Number MPD requests processed, partitioned by status code.", "livesim2")
	prometheusMW.mpdLatency = newHistogram(mpdLatencyName,
		"MPD response latency.", "livesim2", defaultBuckets)
	prometheusMW.otherReqs = newCounter(otherReqsName,
		"Number other requests processed, partitioned by status code.", "livesim2")
	prometheusMW.otherLatency = newHistogram(otherLatencyName,
		"Other response latency.", "livesim2", defaultBuckets)
}

// NewPrometheusMiddleware returns a new prometheus Middleware handler.
func NewPrometheusMiddleware() func(next http.Handler) http.Handler {
	return prometheusMW.handler
}

func (mw prometheusMiddleware) handler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := strconv.Itoa(ww.Status())
		latencyMS := float64(time.Since(start).Nanoseconds()) * 1e-6
		dotIdx := strings.LastIndex(path, ".")
		ext := ""
		if dotIdx >= 0 {
			ext = strings.ToLower(path[dotIdx:])
		}
		switch ext {
		case ".mpd":
			mw.mpdReqs.WithLabelValues(status).Inc()
			mw.mpdLatency.WithLabelValues(status).Observe(latencyMS)
		case ".cmfv", ".cmfa", ".cmft", ".mp4", ".m4s", ".m4a", ".m4t", ".m4v", ".jpg":
			mw.segmentReqs.WithLabelValues(status).Inc()
			mw.segmentLatency.WithLabelValues(status).Observe(latencyMS)
		default:
			mw.otherReqs.WithLabelValues(status).Inc()
			mw.otherLatency.WithLabelValues(status).Observe(latencyMS)
		}
	}
	return http.HandlerFunc(fn)
}

func newCounter(counterName, help, serviceName string) *prometheus.CounterVec {
	cv := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        counterName,
			Help:        help,
			ConstLabels: prometheus.Labels{"service": serviceName},
		},
		[]string{"code"},
	)
	prometheus.MustRegister(cv)
	return cv
}

func newHistogram(histogramName, help, serviceName string, buckets []float64) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:        histogramName,
		Help:        help,
		ConstLabels: prometheus.Labels{"service": serviceName},
		Buckets:     buckets,
	},
		[]string{"code"},
	)
	prometheus.MustRegister(h)
	return h
}
