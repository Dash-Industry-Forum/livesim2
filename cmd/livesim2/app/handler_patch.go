package app

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/patch"
)

type rec struct {
	body   []byte
	status int
}

func (r *rec) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}

func (r *rec) WriteHeader(status int) {
	r.status = status
}

func (r *rec) Header() http.Header {
	return http.Header{}
}

// patchHandlerFunc returns an MPD patch
func (s *Server) patchHandlerFunc(w http.ResponseWriter, r *http.Request) {
	origQuery := r.URL.RawQuery
	q := r.URL.Query()
	publishTime := q.Get("publishTime")
	if publishTime == "" {
		slog.Warn("publishTime query is required, but not provided in patch request")
		http.Error(w, "publishTime query is required", http.StatusBadRequest)
	}
	old := &rec{}
	oldQuery := removeQuery(origQuery, "nowMS")
	oldQuery = removeQuery(oldQuery, "nowDate")
	mpdPath := mpdPathFromPatchPath(r.URL.Path)
	r.URL.Path = mpdPath
	r.URL.RawQuery = oldQuery
	s.livesimHandlerFunc(old, r)

	new := &rec{}
	newQuery := removeQuery(origQuery, "publishTime")
	r.URL.RawQuery = newQuery
	s.livesimHandlerFunc(new, r)

	doc, err := patch.MPDDiff(old.body, new.body)
	switch {
	case errors.Is(err, patch.ErrPatchSamePublishTime):
		http.Error(w, err.Error(), http.StatusTooEarly)
		return
	case errors.Is(err, patch.ErrPatchTooLate):
		http.Error(w, err.Error(), http.StatusGone)
		return
	case err != nil:
		slog.Error("MPDDiff", "err", err)
		http.Error(w, "MPDDiff", http.StatusInternalServerError)
		return
	}
	doc.Indent(2)
	b, err := doc.WriteToBytes()
	if err != nil {
		slog.Error("WriteToBytes", "err", err)
		http.Error(w, "WriteToBytes", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/dash-patch+xml")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, err = w.Write(b)
	if err != nil {
		slog.Error("Write", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

func mpdPathFromPatchPath(patchPath string) string {
	mpdPath := strings.Replace(patchPath, ".mpp", ".mpd", 1)
	//TODO. Handle a possible set prefix before patch
	return strings.TrimPrefix(mpdPath, "/patch")
}

func removeQuery(query, key string) string {
	q := strings.Split(query, "&")
	for i, kv := range q {
		if strings.HasPrefix(kv, key+"=") {
			q = append(q[:i], q[i+1:]...)
			break
		}
	}
	return strings.Join(q, "&")
}
