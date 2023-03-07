package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStppTimeMessage(t *testing.T) {

	testCases := []struct {
		lang   string
		utcMS  int
		segNr  int
		wanted string
	}{
		{
			lang:   "en",
			utcMS:  0,
			segNr:  0,
			wanted: "1970-01-01T00:00:00Z<br/>en # 0",
		},
	}

	for _, tc := range testCases {
		got := makeStppMessage(tc.lang, tc.utcMS, tc.segNr)
		require.Equal(t, tc.wanted, got)
	}
}

func TestMSToTTMLTime(t *testing.T) {

	testCases := []struct {
		ms     int
		wanted string
	}{
		{
			ms:     0,
			wanted: "00:00:00.000",
		},
		{
			ms:     36605_230,
			wanted: "10:10:05.230",
		},
	}

	for _, tc := range testCases {
		got := msToTTMLTime(tc.ms)
		require.Equal(t, tc.wanted, got)
	}
}

func TestStppTimeCues(t *testing.T) {

	testCases := []struct {
		nr          uint32
		startTimeMS uint64
		dur         uint32
		startUTCMS  uint64
		lang        string
		wanted      []StppTimeCue
	}{
		{
			nr:          0,
			startTimeMS: 0,
			dur:         2000,
			startUTCMS:  0,
			lang:        "en",
			wanted: []StppTimeCue{
				{
					Id:    "en-0",
					Begin: "00:00:00.000",
					End:   "00:00:00.900",
					Msg:   "1970-01-01T00:00:00Z",
				},
			},
		},
	}

	for _, tc := range testCases {
		require.Equal(t, tc, tc)
	}
}

func TestCalcCueItvls(t *testing.T) {

	testCases := []struct {
		desc     string
		startMS  int
		dur      int
		utcMS    int
		cueDurMS int
		wanted   []cueItvl
	}{
		{
			desc:     "long cue",
			startMS:  0,
			dur:      2000,
			utcMS:    0,
			cueDurMS: 1800,
			wanted: []cueItvl{
				{startMS: 0, endMS: 1800, utcS: 0},
			},
		},
		{
			desc:     "simple case w 2 cues",
			startMS:  0,
			dur:      2000,
			utcMS:    0,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 0, endMS: 900, utcS: 0},
				{startMS: 1000, endMS: 1900, utcS: 1},
			},
		},
		{
			desc:     "simple case w 1 cues",
			startMS:  0,
			dur:      2000,
			utcMS:    0,
			cueDurMS: 1800,
			wanted: []cueItvl{
				{startMS: 0, endMS: 1800, utcS: 0},
			},
		},
		{
			desc:     "utc shifted. Starting 100ms into second",
			startMS:  12000,
			dur:      800,
			utcMS:    12100,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 12000, endMS: 12800, utcS: 12},
			},
		},
		{
			desc:     "utc shifted. long segment",
			startMS:  12000,
			dur:      801,
			utcMS:    12100,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 12000, endMS: 12800, utcS: 12},
			},
		},
		{
			desc:     "utc shifted, somewhat short segment",
			startMS:  12000,
			dur:      799,
			utcMS:    12100,
			cueDurMS: 900,
			wanted: []cueItvl{
				{startMS: 12000, endMS: 12799, utcS: 12},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := calcCueItvls(tc.startMS, tc.dur, tc.utcMS, tc.cueDurMS)
			require.Equal(t, tc.wanted, got)
		})
	}
}
