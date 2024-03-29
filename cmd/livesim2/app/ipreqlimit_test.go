// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestLimiter(t *testing.T) {

	endpointCalledCount := int64(0)

	maxNrRequests := 5
	maxTime := 100 * time.Millisecond
	ltr := NewIPRequestLimiter(maxNrRequests, maxTime, time.Now(), "")
	l := NewLimiterMiddleware("limiter", ltr)

	handler := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&endpointCalledCount, 1)
	}

	mux := http.NewServeMux()

	finalHandler := http.HandlerFunc(handler)
	mux.Handle("/", l(finalHandler))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	for i := 0; i < maxNrRequests; i++ {
		doRequestAndCheckResponse(t, ts, i+1, maxNrRequests, http.StatusOK)
	}
	for i := maxNrRequests; i <= maxNrRequests+2; i++ {
		doRequestAndCheckResponse(t, ts, i+1, maxNrRequests, http.StatusTooManyRequests)
	}
	time.Sleep(maxTime)
	for i := 0; i < maxNrRequests; i++ {
		doRequestAndCheckResponse(t, ts, i+1, maxNrRequests, http.StatusOK)
	}
}

func doRequestAndCheckResponse(t *testing.T, ts *httptest.Server, reqNr, maxNrRequests int, wantedStatus int) {
	t.Helper()
	res, err := http.Get(ts.URL)
	if err != nil {
		t.Error(err)
	}
	limitHeader := res.Header.Get("limiter")
	wantedHeader := fmt.Sprintf("%d (max %d)", reqNr, maxNrRequests)
	if limitHeader != wantedHeader {
		t.Errorf("wanted %q, but got %q", wantedHeader, limitHeader)
	}
	if res.StatusCode != wantedStatus {
		t.Errorf("got status %d instead of %d", res.StatusCode, wantedStatus)
	}
}
