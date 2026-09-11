// Package scte35 builds SCTE-35 cue messages for carriage in DASH, according to
// ANSI/SCTE 35 2023r1 and ANSI/SCTE 214-1 2022.
//
// A [Cue] is one splice_info_section: either a legacy splice_insert() or a time_signal()
// with one or more segmentation_descriptor() levels. The same value renders to all three
// carriage forms SCTE 214-1 defines, so they cannot drift apart:
//
//   - [Cue.Binary] is the raw splice_info_section for an inband emsg box
//     (scheme [SchemeIDURI], SCTE 214-1 §6.7.3).
//   - [Cue.BinarySignal] is <Signal><Binary> for an MPD EventStream of scheme
//     urn:scte:scte35:2014:xml+bin (§6.7.2.1).
//   - [Cue.XMLSignal] is <Signal><SpliceInfoSection> for an MPD EventStream of scheme
//     urn:scte:scte35:2013:xml.
package scte35

import (
	"encoding/base64"

	"github.com/Comcast/gots/v2"
	gsc "github.com/Comcast/gots/v2/scte35"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/dash-mpd/xml"
)

const (
	// SchemeIDURI is the scheme for inband (emsg) carriage of binary SCTE-35 messages.
	SchemeIDURI = "urn:scte:scte35:2013:bin"
	// TimescaleHz is the SCTE-35 clock: splice times and durations are 90kHz ticks.
	TimescaleHz = 90000
	// DefaultTier is the "no tier" value. SCTE-35 §9.6: 0xfff means the message is not
	// tiered and every receiver should process it.
	DefaultTier = 0xfff
)

// Command is the splice command of a cue message.
type Command uint8

const (
	// SpliceInsert is the legacy splice_insert() command, which carries the break
	// duration itself.
	SpliceInsert Command = iota
	// TimeSignal is the time_signal() command, which carries its meaning in the
	// segmentation descriptors of the message.
	TimeSignal
)

// Level is one segmentation_descriptor() of a time_signal cue. Several may share a cue:
// SCTE 35 §14 and Figure 5 show a Break containing a Placement Opportunity containing the
// individual advertisements, and every level that opens at the same instant travels in one
// message, as consecutive descriptors of its descriptor loop.
//
// The start and the end descriptor of a level share EventID (SCTE 35 §10.3.3.5), which is
// how a receiver pairs them.
type Level struct {
	TypeID      uint8  // segmentation_type_id, e.g. 0x34 Provider Placement Opportunity Start
	EventID     uint32 // segmentation_event_id, shared by the start and end of a pair
	DurationPTS uint64 // segmentation_duration in 90kHz ticks, 0 for no duration
	Num         uint8  // segment_num
	Expected    uint8  // segments_expected
	UPIDType    uint8  // segmentation_upid_type, see SCTE 35 Table 22
	UPID        []byte // segmentation_upid, empty for none
}

// Cue is one SCTE-35 splice_info_section.
type Cue struct {
	Cmd Command
	PTS uint64 // splice time in 90kHz ticks
	// Tier is the authorization tier; use DefaultTier unless a tier is being tested.
	Tier uint16

	// splice_insert() fields, unused for a time_signal.
	EventID         uint32 // splice_event_id
	OutOfNetwork    bool   // out_of_network_indicator: true when splicing into an ad
	BreakDurPTS     uint64 // break_duration in 90kHz ticks, 0 for none
	AutoReturn      bool   // break_duration.auto_return
	SpliceImmediate bool   // splice_immediate_flag

	// time_signal() descriptors, outermost level first.
	Levels []Level
}

// Binary returns the splice_info_section, from the table_id byte through the CRC_32, as
// SCTE 214-1 §6.7.3 requires it in emsg.message_data[].
func (c Cue) Binary() []byte {
	s := gsc.CreateSCTE35()
	s.SetTier(c.Tier)
	switch c.Cmd {
	case TimeSignal:
		s.SetCommandInfo(gsc.CreateTimeSignalCommand())
		descs := make([]gsc.SegmentationDescriptor, 0, len(c.Levels))
		for _, l := range c.Levels {
			descs = append(descs, l.descriptor())
		}
		s.SetDescriptors(descs)
	default:
		cmd := gsc.CreateSpliceInsertCommand()
		cmd.SetEventID(c.EventID)
		cmd.SetIsOut(c.OutOfNetwork)
		cmd.SetSpliceImmediate(c.SpliceImmediate)
		if c.BreakDurPTS != 0 {
			cmd.SetHasDuration(true)
			cmd.SetDuration(gots.PTS(c.BreakDurPTS))
			cmd.SetIsAutoReturn(c.AutoReturn)
		}
		s.SetCommandInfo(cmd)
	}
	// Set the splice time on the section rather than on the command: gots derives
	// pts_adjustment from the difference between the two, so setting only the command
	// time yields pts_adjustment = 2^33 - pts_time, and a receiver adding them modulo
	// 2^33 gets a splice time of zero.
	if !c.SpliceImmediate {
		s.SetHasPTS(true)
		s.SetPTS(gots.PTS(c.PTS))
	}
	return s.UpdateData()
}

// Base64 returns the binary message base64-encoded, the form the <Binary> element takes.
func (c Cue) Base64() string {
	return base64.StdEncoding.EncodeToString(c.Binary())
}

// BinarySignal returns the message as <Signal><Binary>, the only form allowed for an
// EventStream of scheme urn:scte:scte35:2014:xml+bin.
func (c Cue) BinarySignal() *m.SignalType {
	sig := m.NewSignal()
	sig.Binary = m.NewBinary(c.Base64())
	return sig
}

// XMLSignal returns the message as <Signal><SpliceInfoSection>, describing the same cue as
// [Cue.Binary] in the XML representation of SCTE 35 §12.
func (c Cue) XMLSignal() *m.SignalType {
	sis := m.NewSpliceInfoSection()
	sis.ProtocolVersion = m.Ptr(uint8(0))
	sis.PtsAdjustment = 0
	sis.Tier = m.Ptr(c.Tier)
	switch c.Cmd {
	case TimeSignal:
		ts := m.NewTimeSignal()
		ts.SpliceTime = m.NewSpliceTime()
		ts.SpliceTime.PtsTime = m.Ptr(c.PTS)
		sis.TimeSignal = ts
		for _, l := range c.Levels {
			sis.SegmentationDescriptors = append(sis.SegmentationDescriptors, l.xmlDescriptor())
		}
	default:
		si := m.NewSpliceInsert()
		si.SpliceEventId = m.Ptr(c.EventID)
		si.SpliceEventCancelIndicator = m.Ptr(false)
		si.OutOfNetworkIndicator = m.Ptr(c.OutOfNetwork)
		si.SpliceImmediateFlag = m.Ptr(c.SpliceImmediate)
		si.UniqueProgramId = m.Ptr(uint16(0))
		si.AvailNum = m.Ptr(uint8(0))
		si.AvailsExpected = m.Ptr(uint8(0))
		if !c.SpliceImmediate {
			st := m.NewSpliceTime()
			st.PtsTime = m.Ptr(c.PTS)
			si.Program = &m.SpliceInsertProgramType{
				XMLName:    xml.Name{Space: m.SCTE35Namespace, Local: "Program"},
				SpliceTime: st,
			}
		}
		if c.BreakDurPTS != 0 {
			si.BreakDuration = m.NewBreakDuration(c.AutoReturn, c.BreakDurPTS)
		}
		sis.SpliceInsert = si
	}
	sig := m.NewSignal()
	sig.SpliceInfoSection = sis
	return sig
}

// descriptor builds the gots segmentation_descriptor for the binary form.
func (l Level) descriptor() gsc.SegmentationDescriptor {
	d := gsc.CreateSegmentationDescriptor()
	d.SetEventID(l.EventID)
	d.SetTypeID(gsc.SegDescType(l.TypeID))
	d.SetIsEventCanceled(false)
	// program_segmentation_flag: the descriptor applies to the whole program rather than
	// to listed components.
	d.SetHasProgramSegmentation(true)
	// delivery_not_restricted_flag: livesim2 signals no delivery restrictions, which
	// makes the four restriction fields reserved (and omits DeliveryRestrictions in XML).
	d.SetIsDeliveryNotRestricted(true)
	d.SetHasSubSegments(false)
	if l.DurationPTS != 0 {
		d.SetHasDuration(true)
		d.SetDuration(gots.PTS(l.DurationPTS))
	}
	d.SetUPIDType(gsc.SegUPIDType(l.UPIDType))
	d.SetUPID(l.UPID)
	d.SetSegmentNumber(l.Num)
	d.SetSegmentsExpected(l.Expected)
	return d
}

// xmlDescriptor builds the same descriptor in the XML form. DeliveryRestrictions is absent,
// which is how the XML representation expresses delivery_not_restricted_flag = 1.
func (l Level) xmlDescriptor() *m.SegmentationDescriptorType {
	d := m.NewSegmentationDescriptor()
	d.SegmentationEventId = m.Ptr(l.EventID)
	d.SegmentationEventCancelIndicator = m.Ptr(false)
	d.SegmentationTypeId = m.Ptr(l.TypeID)
	d.SegmentNum = m.Ptr(l.Num)
	d.SegmentsExpected = m.Ptr(l.Expected)
	if l.DurationPTS != 0 {
		d.SegmentationDuration = m.Ptr(l.DurationPTS)
	}
	if len(l.UPID) > 0 {
		upid := m.NewSegmentationUpid(string(l.UPID))
		upid.SegmentationUpidType = m.Ptr(l.UPIDType)
		d.SegmentationUpids = []*m.SegmentationUpidType{upid}
	}
	return d
}
