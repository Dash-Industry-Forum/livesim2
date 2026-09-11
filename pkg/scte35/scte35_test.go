package scte35_test

import (
	"testing"

	gotsscte35 "github.com/Comcast/gots/v2/scte35"
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
			// The splice time in the payload must agree with the emsg header, i.e. be the
			// same instant expressed on the 90kHz SCTE-35 clock.
			sis := decodeSpliceInfoSection(t, emsg.MessageData)
			assert.Equal(t, 0, int(ptsAdjustment(t, emsg.MessageData)), "pts_adjustment")
			wantPTS90 := tc.wantedPTS * 90000 / tc.timescale
			assert.Equal(t, int(wantPTS90), int(sis.PTS()), "splice time in payload")
		}
	}
}

// TestSpliceInsertPayloadSpliceTime guards the pts_adjustment encoding. gots derives
// pts_adjustment from the difference between the section time and the command time, so
// setting only the latter produces pts_adjustment = 2^33 - pts_time and every receiver
// adding the two modulo 2^33 sees a splice time of 0.
func TestSpliceInsertPayloadSpliceTime(t *testing.T) {
	const ptsTime = 900_000 // 10s on the 90kHz clock
	const duration = 20 * 90_000
	payload := scte35.CreateSpliceInsertPayload(scte35.SpliceInsertParams{
		PtsTime:               ptsTime,
		Duration:              duration,
		SpliceEventID:         42,
		Tier:                  4095,
		OutOfNetworkIndicator: true,
		AutoReturn:            true,
	})

	assert.Equal(t, uint64(0), ptsAdjustment(t, payload), "pts_adjustment must be zero")

	sis := decodeSpliceInfoSection(t, payload)
	assert.Equal(t, gotsscte35.SpliceCommandType(gotsscte35.SpliceInsert), sis.Command(), "splice command type")
	assert.Equal(t, uint64(ptsTime), uint64(sis.PTS()), "splice time after pts_adjustment")
	cmd, ok := sis.CommandInfo().(gotsscte35.SpliceInsertCommand)
	require.True(t, ok, "splice_insert command")
	assert.Equal(t, uint32(42), cmd.EventID(), "splice_event_id")
	assert.True(t, cmd.IsOut(), "out_of_network_indicator")
	assert.Equal(t, uint64(duration), uint64(cmd.Duration()), "break_duration")
}

// ptsAdjustment reads the 33-bit pts_adjustment field out of a splice_info_section.
func ptsAdjustment(t *testing.T, sis []byte) uint64 {
	t.Helper()
	require.Greater(t, len(sis), 9, "splice_info_section too short")
	return uint64(sis[4]&0x01)<<32 | uint64(sis[5])<<24 | uint64(sis[6])<<16 |
		uint64(sis[7])<<8 | uint64(sis[8])
}

// decodeSpliceInfoSection parses a raw splice_info_section (as carried in an emsg).
// gots' parser expects an MPEG-2 section with a leading pointer_field, so one is added.
func decodeSpliceInfoSection(t *testing.T, sis []byte) gotsscte35.SCTE35 {
	t.Helper()
	parsed, err := gotsscte35.NewSCTE35(append([]byte{0x00}, sis...))
	require.NoError(t, err, "decode splice_info_section")
	return parsed
}
