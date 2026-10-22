package scte35_test

import (
	"encoding/base64"
	"testing"

	refscte35 "github.com/Comcast/scte35-go/pkg/scte35"
	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	"github.com/stretchr/testify/require"
)

// The sample messages of ANSI/SCTE 35 2023r1 §14.
const (
	samplePOStart      = "/DA0AAAAAAAA///wBQb+cr0AUAAeAhxDVUVJSAAAjn/PAAGlmbAICAAAAAAsoKGKNAIAmsnRfg==" // §14.1
	sampleSpliceInsert = "/DAvAAAAAAAA///wFAVIAACPf+/+c2nALv4AUsz1AAAAAAAKAAhDVUVJAAABNWLbowo="         // §14.2
	samplePOEnd        = "/DAvAAAAAAAA///wBQb+dGKQoAAZAhdDVUVJSAAAjn+fCAgAAAAALKChijUCAKnMZ1g="         // §14.3
)

// TestSpecSampleMessages compares Cue.Binary byte for byte with the sample messages of
// ANSI/SCTE 35 2023r1 §14. The samples set fields that a livesim2 cue never does: cw_index
// 0xFF in an unencrypted message, delivery restrictions, and in §14.2 an avail_descriptor.
// Those are set to livesim2's values in the decoded sample, which is then encoded again by
// scte35-go, an implementation independent of the gots encoder behind Cue.
func TestSpecSampleMessages(t *testing.T) {
	turnerID := []byte{0, 0, 0, 0, 0x2C, 0xA0, 0xA1, 0x8A} // segmentation_upid_type 0x08
	cases := []struct {
		desc   string
		sample string
		cue    scte35.Cue
	}{
		{
			desc:   "14.1 time_signal Placement Opportunity Start",
			sample: samplePOStart,
			cue: scte35.Cue{Cmd: scte35.TimeSignal, PTS: 0x072BD0050, Tier: scte35.DefaultTier,
				Levels: []scte35.Level{{TypeID: poStart, EventID: 0x4800008E, DurationPTS: 0x1A599B0,
					Num: 2, UPIDType: 0x08, UPID: turnerID}}},
		},
		{
			desc:   "14.2 splice_insert",
			sample: sampleSpliceInsert,
			cue: scte35.Cue{Cmd: scte35.SpliceInsert, PTS: 0x07369C02E, Tier: scte35.DefaultTier,
				EventID: 0x4800008F, OutOfNetwork: true, BreakDurPTS: 0x52CCF5, AutoReturn: true},
		},
		{
			desc:   "14.3 time_signal Placement Opportunity End",
			sample: samplePOEnd,
			cue: scte35.Cue{Cmd: scte35.TimeSignal, PTS: 0x0746290A0, Tier: scte35.DefaultTier,
				Levels: []scte35.Level{{TypeID: poEnd, EventID: 0x4800008E,
					Num: 2, UPIDType: 0x08, UPID: turnerID}}},
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			sample, err := base64.StdEncoding.DecodeString(c.sample)
			require.NoError(t, err)
			sis, err := refscte35.DecodeBytes(sample)
			require.NoError(t, err)
			again, err := sis.Encode()
			require.NoError(t, err)
			require.Equal(t, sample, again, "scte35-go encodes the decoded sample unchanged")

			sis.EncryptedPacket.CWIndex = 0
			var descs refscte35.SpliceDescriptors
			for _, d := range sis.SpliceDescriptors {
				switch d := d.(type) {
				case *refscte35.AvailDescriptor:
					continue
				case *refscte35.SegmentationDescriptor:
					d.DeliveryRestrictions = nil // delivery_not_restricted_flag = 1
				}
				descs = append(descs, d)
			}
			sis.SpliceDescriptors = descs
			want, err := sis.Encode()
			require.NoError(t, err)
			require.Equal(t, want, c.cue.Binary())
		})
	}
}
