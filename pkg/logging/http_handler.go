// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package logging

import (
	"fmt"
	"net/http"
)

type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

var LogRoutes = [2]Route{
	{"GET", "/loglevel", LogLevelGet},
	{"POST", "/loglevel", LogLevelSet},
}

// LogLevelGet handles loglevel GET request
func LogLevelGet(w http.ResponseWriter, r *http.Request) {
	currentLevel := LogLevel()
	fmt.Fprintln(w, currentLevel)
}

// LogLevelSet sets the loglevel from a posted form
// Can be triggered like curl -F level=debug <server>/loglevel
func LogLevelSet(w http.ResponseWriter, r *http.Request) {
	currentLevel := LogLevel()
	err := r.ParseMultipartForm(128)
	if err != nil {
		http.Error(w, "Incorrect form data", http.StatusBadRequest)
		return
	}
	newLevel := r.FormValue("level")
	err = SetLogLevel(newLevel)
	if err != nil {
		msg := fmt.Sprintf("Incorrect log level %q", newLevel)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	fmt.Fprintf(w, "%q → %q\n", currentLevel, LogLevel())
}
