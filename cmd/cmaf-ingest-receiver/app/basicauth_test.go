package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// finalRemove removes a directory with retry logic for Windows.
// On Windows, file handles may be held briefly after close, causing removal to fail.
func finalRemove(dir string) {
	maxRetries := 1
	if runtime.GOOS == "windows" {
		maxRetries = 5
	}
	var err error
	for i := 0; i < maxRetries; i++ {
		err = os.RemoveAll(dir)
		if err == nil {
			return
		}
		if i < maxRetries-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove temp dir after %d attempts: %s\n", maxRetries, err)
	}
}

func TestBasicAuth(t *testing.T) {

	config := Config{
		Channels: []ChannelConfig{
			{Name: "clear"},
			{Name: "protected", AuthUser: "user", AuthPswd: "secret"},
			{Name: "onlyuser", AuthUser: "user", AuthPswd: ""},
		},
	}
	cases := []struct {
		desc                 string
		url                  string
		user                 string
		password             string
		expectedResponseCode int
	}{
		{desc: "No password", url: "clear/video/init.cmfv", expectedResponseCode: http.StatusOK},
		{desc: "Valid password", url: "protected/video/init.cmfv", user: "user", password: "secret",
			expectedResponseCode: http.StatusOK},
		{desc: "Invalid password", url: "protected/video/init.cmfv", user: "user", password: "wrong",
			expectedResponseCode: http.StatusUnauthorized},
		{desc: "Only user specified", url: "onlyuser/video/init.cmfv", user: "user", password: "",
			expectedResponseCode: http.StatusOK},
	}

	tmpDir, err := os.MkdirTemp("", "ew-cmaf-ingest-test")
	assert.NoError(t, err)
	opts := Options{
		prefix:                "/upload",
		timeShiftBufferDepthS: 30,
		storage:               tmpDir,
	}

	data, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	receiver, err := NewReceiver(ctx, &opts, &config)
	require.NoError(t, err)
	// Cleanup: cancel context, wait for goroutines, then remove temp dir
	defer func() {
		cancel()
		receiver.WaitAll()
		finalRemove(tmpDir)
	}()
	router := setupRouter(receiver, opts.storage, "")
	server := httptest.NewServer(router)
	defer server.Close()
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			buf := bytes.NewBuffer(data)
			url := fmt.Sprintf("%s%s/%s", server.URL, opts.prefix, c.url)
			req, err := http.NewRequest(http.MethodPut, url, buf)
			require.NoError(t, err)
			req.SetBasicAuth(c.user, c.password)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, c.expectedResponseCode, resp.StatusCode)
		})
	}
}
