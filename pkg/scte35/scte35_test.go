package scte35_test

import (
	"testing"

	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSCTE35Generation(t *testing.T) {

	testCases := []struct {
		segStart    uint64
		segEnd      uint64
		timescale   uint64
		perMinute   int
		wantedEmsg  bool
		wantedPTS   uint64
		expectedErr bool
	}{
		{
			segStart:   0,
			segEnd:     180000,
			perMinute:  1,
			timescale:  90000,
			wantedEmsg: false,
		},
		{
			segStart:   180000,
			segEnd:     360000,
			perMinute:  1,
			timescale:  90000,
			wantedEmsg: true,
			wantedPTS:  900_000,
		},
		{
			segStart:   360000,
			segEnd:     540000,
			perMinute:  1,
			timescale:  90000,
			wantedEmsg: false,
		},
		{
			segStart:   2000,
			segEnd:     4000,
			perMinute:  1,
			timescale:  1000,
			wantedEmsg: true,
			wantedPTS:  10_000,
		},
		{
			perMinute:   4,
			expectedErr: true,
		},
	}

	for _, tc := range testCases {
		emsg, err := scte35.CreateEmsgAhead(tc.segStart, tc.segEnd, tc.timescale, tc.perMinute)
		if tc.expectedErr {
			assert.Error(t, err)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, tc.wantedEmsg, emsg != nil, "emsg wanted")
		if emsg != nil {
			assert.Equal(t, int(tc.timescale), int(emsg.TimeScale), "emsg timescale")
			assert.Equal(t, int(tc.wantedPTS), int(emsg.PresentationTime), "emsg PTS")
		}
	}
}
