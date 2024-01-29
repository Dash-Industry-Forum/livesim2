package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
)

const (
	laURLSuffix = "/eccp.json"
)

// laURLHandlerFunc handles LA-URL requests where a POST request provides key IDs via JSON.
// The response is a JSON array of key IDs.
// Protocol defined in https://dashif.org/docs/IOP-Guidelines/DASH-IF-IOP-Part6-v5.0.0.pdf.
func (s *Server) laURLHandlerFunc(w http.ResponseWriter, r *http.Request) {
	log := logging.SubLoggerWithRequestID(slog.Default(), r)
	uPath := r.URL.Path
	if !strings.HasSuffix(uPath, laURLSuffix) {
		msg := fmt.Sprintf("URL does not end with %s", laURLSuffix)
		log.Error(msg)
		http.Error(w, msg, http.StatusBadRequest)
	}
	// Parse JSON request body which looks like {"kids":["nrQFDeRLSAKTLifXUIPiZg"],"type":"temporary"}
	// We only care about the kids array.
	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		msg := "ReadAll error"
		log.Error(msg, "err", err)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	r.Body.Close()
	var reqData LaURLRequest
	err = json.Unmarshal(reqBody, &reqData)
	if err != nil {
		msg := "Unmarshal error"
		log.Error(msg, "err", err)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	log.Debug("laURL request", "data", reqData)
	// Create response
	var respData LaURLResponse
	for _, kid := range reqData.KIDs {
		kid = unpackBase64(kid)
		kid16, err := id16FromBase64(kid)
		if err != nil {
			msg := "id16FromBase64 error"
			log.Error(msg, "err", err)
			http.Error(w, msg, http.StatusInternalServerError)
			return
		}
		key := kidToKey(kid16)
		keyStr := urlSafeBase64(key.PackBase64())
		kidStr := urlSafeBase64(kid)
		respData.Keys = append(respData.Keys, CCPKey{
			Kty: "oct",
			K:   keyStr,
			Kid: kidStr,
		})
	}
	respData.Type = "temporary"
	log.Debug("laURL response", "data", respData)
	respBody, err := json.Marshal(respData)
	if err != nil {
		msg := "Marshal error"
		log.Error(msg, "err", err)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(respBody)
	if err != nil {
		log.Error("Could not write http response", "url", r.URL, "err", err)
	}
}

func urlSafeBase64(b64 string) string {
	b := strings.Replace(b64, "=", "", -1)
	b = strings.ReplaceAll(b, "+", "-")
	b = strings.ReplaceAll(b, "/", "_")
	return b
}

// LAURLRequest is the JSON request body for DASH-IF laURL request
type LaURLRequest struct {
	// KIDs is a slice of base64-encoded key IDs
	KIDs []string `json:"kids"`
	Type string   `json:"type"`
}

func parseLaURLBody(body []byte) (kids []id16, err error) {
	var reqData LaURLRequest
	err = json.Unmarshal(body, &reqData)
	if err != nil {
		return nil, err
	}
	kids = make([]id16, 0, len(reqData.KIDs))
	for _, b64KID := range reqData.KIDs {
		k, err := id16FromTruncatedBase64(b64KID)
		if err != nil {
			return nil, err
		}
		kids = append(kids, k)
	}
	return kids, nil
}

type CCPKey struct {
	// Kty is the key type and should have value oct
	Kty string `json:"kty"`
	// K is the base64-encoded key
	K string `json:"k"`
	// Kid is the base64-encoded key ID
	Kid string `json:"kid"`
}

type LaURLResponse struct {
	Keys []CCPKey `json:"keys"`
	Type string   `json:"type"`
}

type keyAndID struct {
	key id16
	id  id16
}

func generateLaURLResponse(keyAndIDs []keyAndID) LaURLResponse {
	r := LaURLResponse{
		Type: "temporary",
		Keys: make([]CCPKey, 0, len(keyAndIDs)),
	}

	for _, ki := range keyAndIDs {
		r.Keys = append(r.Keys, CCPKey{
			Kty: "oct",
			K:   ki.key.PackBase64(),
			Kid: ki.id.PackBase64(),
		})
	}
	return r
}
