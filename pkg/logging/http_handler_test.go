package logging

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

// Get log level from server and verify the level
func verifyLogLevel(t *testing.T, server *httptest.Server, level string) {
	req, err := http.NewRequest("GET", server.URL+"/loglevel", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	respBody, err := io.ReadAll(resp.Body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NotEqual(t, 0, len(respBody))
	require.Equal(t, level, string(respBody))
}

func postLoglevel(t *testing.T, server *httptest.Server, level string) (*http.Response, []byte) {
	// multipart/form-data
	template := "--ZZZ\r\nContent-Disposition: form-data; name=\"level\"\r\n\r\n%s\r\n--ZZZ--\r\n"
	body := fmt.Sprintf(template, level)
	reader := strings.NewReader(body)
	req, err := http.NewRequest("POST", server.URL+"/loglevel", reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=ZZZ")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	respBody, err := io.ReadAll(resp.Body)

	defer resp.Body.Close()
	require.NoError(t, err)

	return resp, respBody
}

// TestHandleLoglevel - Test of log level handler
func TestHandleLoglevelX(t *testing.T) {
	// Initialize logging to debug
	_, err := InitZerolog("debug", LogJSON)
	require.NoError(t, err)

	// Create test server with loglevel routes
	router := chi.NewRouter()
	for _, route := range LogRoutes {
		router.MethodFunc(route.Method, route.Path, route.Handler)
	}
	ts := httptest.NewServer(router)
	defer ts.Close()

	// Verify initial log level
	verifyLogLevel(t, ts, "debug\n")

	// Set log level to info
	resp, _ := postLoglevel(t, ts, "info")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify new log level
	verifyLogLevel(t, ts, "info\n")

	// Set invalid log level
	resp, _ = postLoglevel(t, ts, "banana")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	// Log level should still be info
	verifyLogLevel(t, ts, "info\n")
}
