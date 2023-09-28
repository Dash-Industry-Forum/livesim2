// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"net/http"

	"github.com/Dash-Industry-Forum/livesim2/internal"
)

func addVersionAndCORSHeaders(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("DASH-IF-livesim2", internal.GetVersion())
		w.Header().Add("Access-Control-Allow-Origin", "*")
		w.Header().Add("Access-Control-Allow-Private-Network", "true")
		w.Header().Add("Timing-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
