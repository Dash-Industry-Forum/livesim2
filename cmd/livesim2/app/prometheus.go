// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

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
	mpdReqsName    = "mpd_requests_total"
	mpdLatencyName = "mpd_request_duration_milliseconds"
	segReqsName    = "segment_requests_total"
	segLatencyName = "segment_request_duration_milliseconds"
	service        = "livesim2"
)

// prometheusMiddleware provides a handler that exposes prometheus metrics for various requests
type prometheusMiddleware struct {
	mpdReqs    *prometheus.CounterVec
	mpdLatency *prometheus.HistogramVec
	segReqs    *prometheus.CounterVec
	segLatency *prometheus.HistogramVec
}

func init() {
	prometheusMW.mpdReqs = newCounter(mpdReqsName,
		"Number MPD requests processed, partitioned by status code.", service)
	prometheusMW.mpdLatency = newHistogram(mpdLatencyName,
		"MPD response latency.", service, defaultBuckets)
	prometheusMW.segReqs = newCounter(segReqsName,
		"Number segment requests processed, partitioned by status code.", service)
	prometheusMW.segLatency = newHistogram(segLatencyName,
		"Segment response latency.", service, defaultBuckets)
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
		extIdx := strings.LastIndex(path, ".")
		if extIdx < 0 {
			return
		}

		switch ext := path[extIdx:]; ext {
		case ".mpd":
			mw.mpdReqs.WithLabelValues(status).Inc()
			mw.mpdLatency.WithLabelValues(status).Observe(latencyMS)
		case ".m4s", ".cmfv", ".cmfa", ".cmft":
			mw.segReqs.WithLabelValues(status).Inc()
			mw.segLatency.WithLabelValues(status).Observe(latencyMS)
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
