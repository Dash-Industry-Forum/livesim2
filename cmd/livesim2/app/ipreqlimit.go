// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// IPRequestLimiter limits the number of requests per interval
type IPRequestLimiter struct {
	MaxNrRequests int            `json:"maxNrRequests"`
	Interval      time.Duration  `json:"interval"`
	ResetTime     time.Time      `json:"resetTime"`
	Counters      map[string]int `json:"counters"`
	logFile       string         `json:"-"`
	mux           sync.Mutex     `json:"-"`
}

// NewIPRequestLimiter returns a new IPRequestLimiter with maxNrRequests per interval starting now.
// If logFile is not empty, the IPRequestLimiter is dumped to the logFile at the end of each interval.
func NewIPRequestLimiter(maxNrRequests int, interval time.Duration, start time.Time, logFile string) *IPRequestLimiter {
	return &IPRequestLimiter{
		MaxNrRequests: maxNrRequests,
		Interval:      interval,
		ResetTime:     start,
		Counters:      make(map[string]int),
		logFile:       logFile,
		mux:           sync.Mutex{},
	}
}

// NewLimiterMiddleware returns a middleware that limits the number of requests per IP address per interval
// An HTTP response 429 Too Many Requests is generated if there are too many requests
// An HTTP header named hdrName is return the number of requests and the maximum number of requests per interval
func NewLimiterMiddleware(hdrName string, reqLimiter *IPRequestLimiter) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ip, err := ipFromRequest(r)
			if err != nil {
				_, _ = w.Write([]byte("could not read client IP"))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			now := time.Now()
			count, ok := reqLimiter.Inc(now, ip)
			if !ok {
				if hdrName != "" {
					w.Header().Set(hdrName, fmt.Sprintf("%d (max %d)", count, reqLimiter.MaxNrRequests))
				}
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			if hdrName != "" {
				w.Header().Set(hdrName, fmt.Sprintf("%d (max %d)", count, reqLimiter.MaxNrRequests))
			}
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

// Inc increments the number of requests and returns number and ok value
func (il *IPRequestLimiter) Inc(now time.Time, ip string) (int, bool) {
	il.mux.Lock()
	defer il.mux.Unlock()
	if now.Sub(il.ResetTime) > il.Interval {
		if il.logFile != "" {
			il.dump()
		}
		il.Counters = make(map[string]int)
		il.ResetTime = now
	}
	il.Counters[ip]++
	val := il.Counters[ip]
	return val, val <= il.MaxNrRequests
}

// Count returns the counter value for an IP address
func (il *IPRequestLimiter) Count(ip string) int {
	il.mux.Lock()
	defer il.mux.Unlock()
	return il.Counters[ip]
}

// EndTime returns next reset time.
func (il *IPRequestLimiter) EndTime() time.Time {
	return il.ResetTime.Add(il.Interval)
}

func (il *IPRequestLimiter) dump() {
	payload, err := json.Marshal(il)
	if err != nil {
		log.Error().Err(err).Msg("could not marshal IPRequestLimiter")
		return
	}
	f, err := os.OpenFile(il.logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		log.Error().Err(err).Msg("could not open IPRequestLimiter log file")
	}
	defer f.Close()
	_, err = f.Write(payload)
	if err != nil {
		log.Error().Err(err).Msg("could not write to IPRequestLimiter log file")
	}
}

func ipFromRequest(req *http.Request) (string, error) {
	forwardIP := req.Header.Get("X-Forwarded-For")
	if forwardIP != "" {
		return forwardIP, nil
	}
	ip, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return "", err
	}
	userIP := net.ParseIP(ip)
	if userIP == nil {
		return "", fmt.Errorf("no IP found")
	}
	return userIP.String(), nil
}
