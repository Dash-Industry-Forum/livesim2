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

// LogLevelGet - Handle loglevel GET request
func LogLevelGet(w http.ResponseWriter, r *http.Request) {
	currentLevel := GetLogLevel()
	fmt.Fprintln(w, currentLevel)
}

// LogLevelSet - Handle loglevel POST request
func LogLevelSet(w http.ResponseWriter, r *http.Request) {
	currentLevel := GetLogLevel()
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
	fmt.Fprintf(w, "%q → %q\n", currentLevel, newLevel)
}
