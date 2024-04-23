package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/logging"
	"github.com/stretchr/testify/require"
)

var wantedPatchSegTimelineTime = `<?xml version="1.0" encoding="UTF-8"?>
<Patch xmlns="urn:mpeg:dash:schema:mpd-patch:2020" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="urn:mpeg:dash:schema:mpd-patch:2020 DASH-MPD-PATCH.xsd" mpdId="base" originalPublishTime="2024-04-02T15:50:56Z" publishTime="2024-04-02T15:51:40Z">
  <replace sel="/MPD/@publishTime">2024-04-02T15:51:40Z</replace>
  <replace sel="/MPD/PatchLocation[1]">
    <PatchLocation ttl="60">/patch/livesim2/patch_60/segtimeline_1/testpic_2s/Manifest.mpp?publishTime=2024-04-02T15%3A51%3A40Z</PatchLocation>
  </replace>
  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>
  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>
  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">
    <S t="82179505824768" d="95232"/>
  </add>
  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[15]" pos="after">
    <S d="96256" r="1"/>
  </add>
  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>
  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">
    <S t="154086573420000" d="180000" r="30"/>
  </add>
</Patch>
`

var wantedPatchSegTimelineNumberWithAddAtEnd = `<?xml version="1.0" encoding="UTF-8"?>
<Patch xmlns="urn:mpeg:dash:schema:mpd-patch:2020" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="urn:mpeg:dash:schema:mpd-patch:2020 DASH-MPD-PATCH.xsd" mpdId="base" originalPublishTime="2024-04-16T07:34:38Z" publishTime="2024-04-16T07:34:56Z">
  <replace sel="/MPD/@publishTime">2024-04-16T07:34:56Z</replace>
  <replace sel="/MPD/PatchLocation[1]">
    <PatchLocation ttl="60">/patch/livesim2/patch_60/segtimelinenr_1/testpic_2s/Manifest.mpp?publishTime=2024-04-16T07%3A34%3A56Z</PatchLocation>
  </replace>
  <replace sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/@startNumber">856626417</replace>
  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>
  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">
    <S t="82236136032256" d="96256" r="1"/>
  </add>
  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[15]" pos="after">
    <S d="95232"/>
  </add>
  <replace sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/@startNumber">856626417</replace>
  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>
  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">
    <S t="154192755060000" d="180000" r="30"/>
  </add>
</Patch>
`

func TestPatchHandler(t *testing.T) {
	cfg := ServerConfig{
		VodRoot:   "testdata/assets",
		TimeoutS:  0,
		LogFormat: logging.LogDiscard,
	}
	err := logging.InitSlog(cfg.LogLevel, cfg.LogFormat)
	require.NoError(t, err)
	server, err := SetupServer(context.Background(), &cfg)
	require.NoError(t, err)
	ts := httptest.NewServer(server.Router)
	defer ts.Close()
	testCases := []struct {
		desc              string
		url               string
		wantedStatusCode  int
		wantedContentType string
		wantedBody        string
		wantedExpires     string
	}{
		{
			desc:              "segTimeline no update yet",
			url:               "/patch/livesim2/patch_60/segtimeline_1/testpic_2s/Manifest.mpp?publishTime=2024-04-16T07:34:38Z&nowDate=2024-04-16T07:34:39Z",
			wantedStatusCode:  http.StatusTooEarly,
			wantedContentType: "text/plain; charset=utf-8",
			wantedBody:        "",
		},
		{
			desc:              "segTimeline too late",
			url:               "/patch/livesim2/patch_60/segtimeline_1/testpic_2s/Manifest.mpp?publishTime=2024-04-16T07:34:38Z&nowDate=2024-04-16T07:44:39Z",
			wantedStatusCode:  http.StatusGone,
			wantedContentType: "text/plain; charset=utf-8",
			wantedBody:        "",
		},
		{
			desc:              "segTimeline",
			url:               "/patch/livesim2/patch_60/segtimeline_1/testpic_2s/Manifest.mpp?publishTime=2024-04-02T15:50:56Z&nowDate=2024-04-02T15:51:40Z",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: "application/dash-patch+xml",
			wantedBody:        wantedPatchSegTimelineTime,
			wantedExpires:     "Tue, 02 Apr 2024 15:52:06 GMT",
		},
		{
			desc:              "segTimeline with Number",
			url:               "/patch/livesim2/patch_60/segtimelinenr_1/testpic_2s/Manifest.mpp?publishTime=2024-04-16T07:34:38Z&nowDate=2024-04-16T07:34:57Z",
			wantedStatusCode:  http.StatusOK,
			wantedContentType: "application/dash-patch+xml",
			wantedBody:        wantedPatchSegTimelineNumberWithAddAtEnd,
			wantedExpires:     "Tue, 16 Apr 2024 07:35:48 GMT",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			resp, body := testFullRequest(t, ts, "GET", tc.url, nil)
			require.Equal(t, tc.wantedStatusCode, resp.StatusCode)
			require.Equal(t, tc.wantedContentType, resp.Header.Get("Content-Type"))
			if tc.wantedStatusCode != http.StatusOK {
				return
			}
			if tc.wantedExpires != "" {
				require.Equal(t, tc.wantedExpires, resp.Header.Get("Expires"))
			}
			bodyStr := string(body)
			require.Equal(t, tc.wantedBody, bodyStr)
		})
	}
}
