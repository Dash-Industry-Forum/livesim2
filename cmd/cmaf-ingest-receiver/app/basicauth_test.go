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

	opts := Options{
		prefix:                "/upload",
		timeShiftBufferDepthS: 30,
		storage:               t.TempDir(),
	}

	data, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	receiver, err := NewReceiver(ctx, &opts, &config)
	require.NoError(t, err)
	// Cleanup: cancel context and wait for goroutines
	defer func() {
		cancel()
		receiver.WaitAll()
	}()
	router := setupRouter(receiver, opts.storage, "")
	server := httptest.NewTestServer(t, router)
	client := server.Client()
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			buf := bytes.NewBuffer(data)
			url := fmt.Sprintf("%s%s/%s", server.URL, opts.prefix, c.url)
			req, err := http.NewRequest(http.MethodPut, url, buf)
			require.NoError(t, err)
			req.SetBasicAuth(c.user, c.password)
			resp, err := client.Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, c.expectedResponseCode, resp.StatusCode)
		})
	}
}
