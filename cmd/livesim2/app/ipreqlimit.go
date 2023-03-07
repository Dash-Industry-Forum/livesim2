// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// IPRequestLimiter limits the number of requests per interval
type IPRequestLimiter struct {
	maxNrRequests int
	interval      time.Duration
	resetTime     time.Time
	counters      map[string]int
	mux           sync.Mutex
}

// NewIPRequestLimiter returns a middleware that limits the number of requests per IP address per interval
//
// An HTTP response 429 Too Many Requests is generated if there are too many requests
// A header with the
func NewIPRequestLimiter(hdrName string, maxNrRequests int, interval time.Duration) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		reqLtr := IPRequestLimiter{
			maxNrRequests: maxNrRequests,
			interval:      interval,
			resetTime:     time.Now(),
			counters:      make(map[string]int),
			mux:           sync.Mutex{},
		}
		fn := func(w http.ResponseWriter, r *http.Request) {
			ip, err := getIP(r)
			if err != nil {
				_, _ = w.Write([]byte("could not read client IP"))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			now := time.Now()
			count, ok := reqLtr.Inc(now, ip)
			if !ok {
				if hdrName != "" {
					w.Header().Set(hdrName, fmt.Sprintf("%d (max %d)", count, reqLtr.maxNrRequests))
				}
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			if hdrName != "" {
				w.Header().Set(hdrName, fmt.Sprintf("%d (max %d)", count, reqLtr.maxNrRequests))
			}
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

// Inc incremets the number of requests and returns number and ok value
func (il *IPRequestLimiter) Inc(now time.Time, key string) (int, bool) {
	il.mux.Lock()
	defer il.mux.Unlock()
	if now.Sub(il.resetTime) > il.interval {
		il.counters = make(map[string]int)
		il.resetTime = now
	}
	il.counters[key]++
	val := il.counters[key]
	return val, val <= il.maxNrRequests
}

func getIP(req *http.Request) (string, error) {
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
