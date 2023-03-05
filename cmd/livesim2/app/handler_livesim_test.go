package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeStppInitSegment(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	_, err := logging.InitZerolog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()
	testCases := []struct {
		desc               string
		asset              string
		url                string
		segmentMimeType    string
		lang               string
		mediaTimescale     int
		expectedStatusCode int
	}{
		{
			desc:               "init segment with matching language",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/testpic_2s/timestpp-en/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "en",
			mediaTimescale:     SUBS_STPP_TIMESCALE,
			expectedStatusCode: http.StatusOK,
		},
		{
			desc:               "init segment but language not matching URL => NotFound",
			asset:              "testpic_2s",
			url:                "/livesim2/timesubsstpp_en,sv/testpic_2s/timestpp-se/init.mp4",
			segmentMimeType:    "application/mp4",
			lang:               "se",
			mediaTimescale:     SUBS_STPP_TIMESCALE,
			expectedStatusCode: http.StatusNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, tc.expectedStatusCode, resp.StatusCode)
			if tc.expectedStatusCode != http.StatusOK {
				return
			}
			sr := bits.NewFixedSliceReader(body)
			mp4d, err := mp4.DecodeFileSR(sr)
			require.NoError(t, err)
			mediaTimescale := int(mp4d.Moov.Trak.Mdia.Mdhd.Timescale)
			assert.Equal(t, SUBS_STPP_TIMESCALE, mediaTimescale)
			lang := mp4d.Moov.Trak.Mdia.Elng.Language
			assert.Equal(t, tc.lang, lang)
		})
	}
}

func testFullRequest(t *testing.T, ts *httptest.Server, method, path string, reqBody io.Reader) (*http.Response, []byte) {
	req, err := http.NewRequest(method, ts.URL+path, reqBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	defer resp.Body.Close()

	return resp, respBody
}
