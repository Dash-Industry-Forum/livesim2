package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	defer os.RemoveAll(tmpDir)
	assert.NoError(t, err)
	opts := Options{
		prefix:                "/upload",
		timeShiftBufferDepthS: 30,
		storage:               tmpDir,
	}

	data, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	receiver, err := NewReceiver(ctx, &opts, &config)
	require.NoError(t, err)
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
