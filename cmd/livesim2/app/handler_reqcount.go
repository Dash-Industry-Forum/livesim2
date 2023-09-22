// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

func (s *Server) reqCountHandlerFunc(w http.ResponseWriter, r *http.Request) {
	if s.reqLimiter == nil {
		_, _ = io.WriteString(w, "No request limit configured")
		return
	}
	ip, err := ipFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	count := s.reqLimiter.Count(ip)
	_, _ = io.WriteString(w, fmt.Sprintf("%d (max %d) until %s", count, s.reqLimiter.MaxNrRequests,
		s.reqLimiter.EndTime().Format(time.RFC822)))
}
