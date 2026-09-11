package scte35_test

import (
	"testing"

	gotsscte35 "github.com/Comcast/gots/v2/scte35"
	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	"github.com/Eyevinn/dash-mpd/xml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	ptsTime  = 900_000     // 10s on the 90kHz clock
	breakDur = 20 * 90_000 // 20s
	adDur    = 10 * 90_000 // 10s
	poStart  = 0x34        // Provider Placement Opportunity Start
	poEnd    = 0x35        // Provider Placement Opportunity End
	brkStart = 0x22        // Break Start
	adStart  = 0x30        // Provider Advertisement Start
)

// TestSpliceInsertBinary guards the pts_adjustment encoding. gots derives pts_adjustment from
// the difference between the section time and the command time, so setting only the latter
// produces pts_adjustment = 2^33 - pts_time and every receiver adding the two modulo 2^33 sees
// a splice time of 0.
func TestSpliceInsertBinary(t *testing.T) {
	cue := scte35.Cue{
		Cmd:          scte35.SpliceInsert,
		PTS:          ptsTime,
		Tier:         scte35.DefaultTier,
		EventID:      42,
		OutOfNetwork: true,
		BreakDurPTS:  breakDur,
		AutoReturn:   true,
	}
	payload := cue.Binary()
	assert.Equal(t, uint64(0), ptsAdjustment(t, payload), "pts_adjustment must be zero")

	sis := decodeSpliceInfoSection(t, payload)
	assert.Equal(t, gotsscte35.SpliceCommandType(gotsscte35.SpliceInsert), sis.Command(), "splice command type")
	assert.Equal(t, uint64(ptsTime), uint64(sis.PTS()), "splice time after pts_adjustment")
	assert.Equal(t, uint16(scte35.DefaultTier), sis.Tier(), "tier")
	cmd, ok := sis.CommandInfo().(gotsscte35.SpliceInsertCommand)
	require.True(t, ok, "splice_insert command")
	assert.Equal(t, uint32(42), cmd.EventID(), "splice_event_id")
	assert.True(t, cmd.IsOut(), "out_of_network_indicator")
	assert.Equal(t, uint64(breakDur), uint64(cmd.Duration()), "break_duration")
}

// TestTimeSignalLevels checks that the levels of a nested cue all travel in one message, as
// consecutive segmentation descriptors sharing one time_signal (SCTE 35 §14, Figure 5).
func TestTimeSignalLevels(t *testing.T) {
	cue := scte35.Cue{
		Cmd:  scte35.TimeSignal,
		PTS:  ptsTime,
		Tier: scte35.DefaultTier,
		Levels: []scte35.Level{
			{TypeID: brkStart, EventID: 7001, DurationPTS: breakDur, UPIDType: 0x0F, UPID: []byte("urn:test:1")},
			{TypeID: poStart, EventID: 7002, DurationPTS: breakDur, UPIDType: 0x0F, UPID: []byte("urn:test:1")},
			{TypeID: adStart, EventID: 7003, DurationPTS: adDur, Num: 1, Expected: 2,
				UPIDType: 0x0F, UPID: []byte("urn:test:1")},
		},
	}
	sis := decodeSpliceInfoSection(t, cue.Binary())
	assert.Equal(t, uint64(0), ptsAdjustment(t, cue.Binary()), "pts_adjustment")
	assert.Equal(t, gotsscte35.SpliceCommandType(gotsscte35.TimeSignal), sis.Command(), "splice command type")
	assert.Equal(t, uint64(ptsTime), uint64(sis.PTS()), "splice time")
	descs := sis.Descriptors()
	require.Len(t, descs, 3, "one descriptor per level")
	for i, want := range []struct {
		typeID  uint8
		eventID uint32
		dur     uint64
	}{
		{brkStart, 7001, breakDur},
		{poStart, 7002, breakDur},
		{adStart, 7003, adDur},
	} {
		assert.Equal(t, gotsscte35.SegDescType(want.typeID), descs[i].TypeID(), "segmentation_type_id %d", i)
		assert.Equal(t, want.eventID, descs[i].EventID(), "segmentation_event_id %d", i)
		assert.Equal(t, want.dur, uint64(descs[i].Duration()), "segmentation_duration %d", i)
		assert.Equal(t, "urn:test:1", string(descs[i].UPID()), "segmentation_upid %d", i)
	}
	assert.Equal(t, uint8(1), descs[2].SegmentNumber(), "segment_num")
	assert.Equal(t, uint8(2), descs[2].SegmentsExpected(), "segments_expected")
}

// TestEndLevelSharesEventID checks the pairing rule of SCTE 35 §10.3.3.5: the start and the
// end descriptor of a level carry the same segmentation_event_id, and only the start one
// carries the duration.
func TestEndLevelSharesEventID(t *testing.T) {
	start := scte35.Cue{Cmd: scte35.TimeSignal, PTS: ptsTime, Tier: scte35.DefaultTier,
		Levels: []scte35.Level{{TypeID: poStart, EventID: 99, DurationPTS: breakDur}}}
	end := scte35.Cue{Cmd: scte35.TimeSignal, PTS: ptsTime + breakDur, Tier: scte35.DefaultTier,
		Levels: []scte35.Level{{TypeID: poEnd, EventID: 99}}}

	sd := decodeSpliceInfoSection(t, start.Binary()).Descriptors()[0]
	ed := decodeSpliceInfoSection(t, end.Binary()).Descriptors()[0]
	assert.Equal(t, sd.EventID(), ed.EventID(), "the pair shares segmentation_event_id")
	assert.Equal(t, uint64(breakDur), uint64(sd.Duration()), "start carries the duration")
	assert.Equal(t, uint64(0), uint64(ed.Duration()), "end carries no duration")
}

// TestBinarySignal checks the <Signal><Binary> form used by urn:scte:scte35:2014:xml+bin.
func TestBinarySignal(t *testing.T) {
	cue := scte35.Cue{Cmd: scte35.SpliceInsert, PTS: ptsTime, Tier: scte35.DefaultTier,
		EventID: 42, OutOfNetwork: true, BreakDurPTS: breakDur, AutoReturn: true}
	out, err := xml.Marshal(cue.BinarySignal())
	require.NoError(t, err)
	want := `<Signal xmlns="http://www.scte.org/schemas/35"><Binary>` + cue.Base64() + `</Binary></Signal>`
	assert.Equal(t, want, string(out))
}

// TestXMLSignal checks the <Signal><SpliceInfoSection> form used by urn:scte:scte35:2013:xml,
// which must describe the same cue as the binary one.
func TestXMLSignal(t *testing.T) {
	cases := []struct {
		desc string
		cue  scte35.Cue
		want string
	}{
		{
			desc: "splice_insert",
			cue: scte35.Cue{Cmd: scte35.SpliceInsert, PTS: ptsTime, Tier: scte35.DefaultTier,
				EventID: 42, OutOfNetwork: true, BreakDurPTS: breakDur, AutoReturn: true},
			want: `<Signal xmlns="http://www.scte.org/schemas/35">` +
				`<SpliceInfoSection protocolVersion="0" tier="4095">` +
				`<SpliceInsert spliceEventId="42" spliceEventCancelIndicator="false" outOfNetworkIndicator="true"` +
				` spliceImmediateFlag="false" uniqueProgramId="0" availNum="0" availsExpected="0">` +
				`<Program><SpliceTime ptsTime="900000"></SpliceTime></Program>` +
				`<BreakDuration autoReturn="true" duration="1800000"></BreakDuration>` +
				`</SpliceInsert></SpliceInfoSection></Signal>`,
		},
		{
			desc: "time_signal with a placement opportunity",
			cue: scte35.Cue{Cmd: scte35.TimeSignal, PTS: ptsTime, Tier: scte35.DefaultTier,
				Levels: []scte35.Level{{TypeID: poStart, EventID: 99, DurationPTS: breakDur,
					UPIDType: 0x0F, UPID: []byte("urn:test:1")}}},
			want: `<Signal xmlns="http://www.scte.org/schemas/35">` +
				`<SpliceInfoSection protocolVersion="0" tier="4095">` +
				`<TimeSignal><SpliceTime ptsTime="900000"></SpliceTime></TimeSignal>` +
				`<SegmentationDescriptor segmentationEventId="99" segmentationEventCancelIndicator="false"` +
				` segmentationDuration="1800000" segmentationTypeId="52" segmentNum="0" segmentsExpected="0">` +
				`<SegmentationUpid segmentationUpidType="15">urn:test:1</SegmentationUpid>` +
				`</SegmentationDescriptor></SpliceInfoSection></Signal>`,
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			out, err := xml.Marshal(c.cue.XMLSignal())
			require.NoError(t, err)
			assert.Equal(t, c.want, string(out))
		})
	}
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
