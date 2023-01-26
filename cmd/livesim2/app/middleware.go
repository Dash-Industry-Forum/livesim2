package app

import (
	"net/http"

	"github.com/Dash-Industry-Forum/livesim2/internal"
)

func addVersionAndCORSHeaders(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("DASH-IF livesim2", internal.GetVersion())
		w.Header().Add("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
