// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Dash-Industry-Forum/livesim2/pkg/scte35"
	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/mp4"
)

// SCTE-35 ad-avail signaling (ANSI/SCTE 35 2023r1, carriage per ANSI/SCTE 214-1 2022).
//
// The ad breaks of the schedule are marked with SCTE-35 cue messages, delivered inband as
// emsg boxes (§6.7.3), in the MPD as an EventStream (§6.7.2.1), or both. A cue is either a
// legacy splice_insert() carrying the break duration, or a time_signal() carrying one or
// more segmentation_descriptor() levels — a Break holding a Placement Opportunity holding
// the individual advertisements, as in SCTE 35 Figure 5.

const (
	// scte35DefaultLeadS is how long before the splice point a cue is delivered. Seven
	// seconds is what livesim2 has always used and is a typical pre-roll.
	scte35DefaultLeadS = 7
	// scte35DefaultTimescale is the EventStream@timescale. 90kHz is the SCTE-35 clock.
	scte35DefaultTimescale = uint32(90000)
	// scte35MaxAdsPerBreak bounds the per-creative segmentation of a break.
	scte35MaxAdsPerBreak = 20
	// scte35DefaultUPIDType is SCTE 35 Table 22 type 0x0F (URI), used for the generated
	// segmentation_upid.
	scte35DefaultUPIDType = 0x0F
	// scte35EventIDSpacing spaces the event ids of the levels of one break. Ids are derived
	// from the break start second, so two levels collide only for breaks 2^20 s (12 days)
	// apart — far outside any live signaling window.
	scte35EventIDSpacing = 1 << 20
	// scte35ClosingIDOffset sets the message ids of the closing messages apart from the
	// others, which are the second of their splice point. A break that ends where the next
	// one starts has its closing message and the next opening message at the same second
	// (SCTE 35 §10.3.3.14 lets an End and the next Start reference the same splice point),
	// and with one id a client would drop the second message as a repeat (SCTE 214-1
	// §6.7.2.1 item 5).
	scte35ClosingIDOffset = uint32(1 << 31)
	// scte35MaxUPIDLen keeps a segmentation_descriptor() within its 8-bit descriptor_length:
	// the other fields livesim2 writes take 22 of the 255 bytes.
	scte35MaxUPIDLen = 233
	// scte35UPIDTypeMID is the MID segmentation_upid_type, a list of UPIDs rather than a
	// value, which the upid= option cannot express.
	scte35UPIDTypeMID = 0x0D
)

// scte35Level is a named segmentation level: the segmentation_type_id pair that opens and
// closes it, and its nesting rank (lower is more outer). See SCTE 35 Table 23.
type scte35Level struct {
	start uint8
	end   uint8
	rank  int
}

// scte35Levels are the level names accepted by the seg= and adseg= options.
var scte35Levels = map[string]scte35Level{
	"break":  {m.SegTypeBreakStart, m.SegTypeBreakEnd, 0},
	"po":     {m.SegTypeProviderPlacementOpportunityStart, m.SegTypeProviderPlacementOpportunityEnd, 1},
	"dpo":    {m.SegTypeDistributorPlacementOpportunityStart, m.SegTypeDistributorPlacementOpportunityEnd, 2},
	"ad":     {m.SegTypeProviderAdvertisementStart, m.SegTypeProviderAdvertisementEnd, 3},
	"dad":    {m.SegTypeDistributorAdvertisementStart, m.SegTypeDistributorAdvertisementEnd, 3},
	"promo":  {m.SegTypeProviderPromoStart, m.SegTypeProviderPromoEnd, 3},
	"dpromo": {m.SegTypeDistributorPromoStart, m.SegTypeDistributorPromoEnd, 3},
}

// SCTE35Config configures SCTE-35 ad-avail signaling for a live stream.
type SCTE35Config struct {
	AdBreaks             // fixed breaks (offset from AST) or a periodic recurrence
	Cmd         string   `json:"Cmd"`                   // "insert" or "timesignal"
	Levels      []string `json:"Levels,omitempty"`      // segmentation levels, outermost first
	AdsPerBreak int      `json:"AdsPerBreak,omitempty"` // per-creative segments, 0 for none
	AdLevel     string   `json:"AdLevel,omitempty"`     // level used for the per-creative segments
	Emsg        bool     `json:"Emsg"`                  // inband carriage
	MPDForm     string   `json:"MPDForm"`               // "off", "bin" or "xml"
	LeadS       int      `json:"LeadS"`                 // cue delivery ahead of the splice point
	End         bool     `json:"End"`                   // also emit the closing message
	Repeat      bool     `json:"Repeat,omitempty"`      // repeat the cue in every segment of the lead window
	UPIDType    uint8    `json:"UPIDType,omitempty"`    // segmentation_upid_type
	UPID        string   `json:"UPID,omitempty"`        // segmentation_upid value
	Value       string   `json:"Value,omitempty"`       // @value of the emsg and the (Inband)EventStream
	Timescale   uint32   `json:"Timescale"`             // EventStream@timescale
	Slate       bool     `json:"Slate"`                 // AD BREAK countdown slate inside the breaks
	PreS        int      `json:"PreS,omitempty"`        // pre-break countdown, seconds before the break
}

// scte35LegacyPresets are the pre-1.14 scte35_1|2|3 variants, expressed in the current
// schedule grammar. They are splice_insert cues on the main timeline with no slate, which
// is what those URLs have always produced. Note that the periodic schedule is anchored to
// the wall clock rather than to the availabilityStartTime, so a stream shifted with start_
// or startrel_ now places its breaks at the same UTC seconds as every other session.
var scte35LegacyPresets = map[string]string{
	"1": "p60:20@10",
	"2": "p60:10@10,40",
	"3": "p60:10@10,36,46",
}

// CreateSCTE35Config parses the value of an "scte35" URL option.
//
// Grammar: ( 1 | 2 | 3 ) | ( <off>:<dur>[,...] | p<period>:<dur>[@<off>,...] )[;key=val;...]
// keys: cmd=, seg=, ads=, adseg=, emsg=, mpd=, lead=, end=, repeat=, upid=, value=, ts=,
// slate=, pre=
//
// Examples: p60:20@10;cmd=timesignal;seg=break,po => a 20s Provider Placement Opportunity
// inside a Break, 10s after every full UTC minute. 30:15;mpd=xml => one splice_insert 30s
// into the stream, signaled in the MPD only.
func CreateSCTE35Config(val string) (*SCTE35Config, error) {
	if val == "" {
		return nil, fmt.Errorf("empty scte35 config")
	}
	if hasExtraSpaces(val) {
		return nil, fmt.Errorf("scte35 config %q has extra spaces", val)
	}
	preset, isLegacy := scte35LegacyPresets[val]
	if isLegacy {
		val = preset
	}
	cfg := &SCTE35Config{
		Cmd:       "insert",
		Levels:    []string{"po"},
		AdLevel:   "ad",
		Emsg:      true,
		MPDForm:   "off",
		LeadS:     scte35DefaultLeadS,
		UPIDType:  scte35DefaultUPIDType,
		Timescale: scte35DefaultTimescale,
		// The breaks of a new-grammar stream show the AD BREAK countdown slate, so that a
		// signaled avail is visible rather than only announced. The legacy presets keep
		// serving the underlying content, which is what those URLs have always done.
		Slate: !isLegacy,
	}
	parts := strings.Split(val, ";")
	ab, err := parseAdBreaks("scte35", parts[0])
	if err != nil {
		return nil, err
	}
	cfg.AdBreaks = ab
	endSet := false
	for _, kv := range parts[1:] {
		key, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("scte35 param %q must be key=val", kv)
		}
		switch key {
		case "cmd":
			if v != "insert" && v != "timesignal" {
				return nil, fmt.Errorf("scte35 cmd %q: must be insert or timesignal", v)
			}
			cfg.Cmd = v
		case "seg":
			cfg.Levels, err = parseSCTE35Levels(v)
		case "adseg":
			if _, ok := scte35Levels[v]; !ok {
				return nil, fmt.Errorf("scte35 adseg %q: unknown segmentation level", v)
			}
			cfg.AdLevel = v
		case "ads":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > scte35MaxAdsPerBreak {
				return nil, fmt.Errorf("scte35 ads %q: must be 0-%d", v, scte35MaxAdsPerBreak)
			}
			cfg.AdsPerBreak = n
		case "emsg":
			cfg.Emsg, err = parseFlag("scte35 emsg", v)
		case "mpd":
			switch v {
			case "off", "bin", "xml":
				cfg.MPDForm = v
			default:
				return nil, fmt.Errorf("scte35 mpd %q: must be off, bin or xml", v)
			}
		case "lead":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("scte35 lead %q: must be >= 0", v)
			}
			cfg.LeadS = n
		case "end":
			cfg.End, err = parseFlag("scte35 end", v)
			endSet = true
		case "repeat":
			cfg.Repeat, err = parseFlag("scte35 repeat", v)
		case "upid":
			t, value, ok := strings.Cut(v, ":")
			if !ok {
				return nil, fmt.Errorf("scte35 upid %q: must be <type>:<value>", v)
			}
			n, err := strconv.ParseUint(t, 0, 8)
			switch {
			case err != nil:
				return nil, fmt.Errorf("scte35 upid %q: bad type", v)
			case n == 0 && value != "":
				return nil, fmt.Errorf("scte35 upid %q: type 0 means no UPID and takes no value", v)
			case n != 0 && value == "":
				return nil, fmt.Errorf("scte35 upid %q: needs a value, or use type 0 for no UPID", v)
			case n == scte35UPIDTypeMID:
				return nil, fmt.Errorf("scte35 upid %q: MID (0x0D) is not supported", v)
			case len(value) > scte35MaxUPIDLen:
				return nil, fmt.Errorf("scte35 upid %q: longer than %d bytes", v, scte35MaxUPIDLen)
			}
			cfg.UPIDType, cfg.UPID = uint8(n), value
		case "slate":
			cfg.Slate, err = parseFlag("scte35 slate", v)
		case "pre":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("scte35 pre %q: must be >= 0", v)
			}
			cfg.PreS = n
		case "value":
			cfg.Value = v
		case "ts":
			n, err := parseUint32(v)
			if err != nil || n == 0 {
				return nil, fmt.Errorf("scte35 ts %q: must be a positive 32-bit integer", v)
			}
			cfg.Timescale = n
		default:
			return nil, fmt.Errorf("scte35 param %q: unknown key", key)
		}
		if err != nil {
			return nil, err
		}
	}
	if !endSet {
		// A time_signal level is only closed by its paired end descriptor, so the closing
		// message is part of the signaling. A splice_insert with auto_return returns by
		// itself, so its explicit return message is opt-in.
		cfg.End = cfg.Cmd == "timesignal"
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseSCTE35Levels parses a comma-separated segmentation level list, e.g. "break,po".
// (A "+" would be more evocative, but the config URL is unescaped with url.QueryUnescape,
// which turns it into a space.)
func parseSCTE35Levels(v string) ([]string, error) {
	if v == "" {
		return nil, nil // signal the breaks with the command alone
	}
	names := strings.Split(v, ",")
	rank := -1
	for _, n := range names {
		l, ok := scte35Levels[n]
		if !ok {
			return nil, fmt.Errorf("scte35 seg %q: unknown segmentation level", n)
		}
		if l.rank <= rank {
			return nil, fmt.Errorf("scte35 seg %q: levels must be listed outermost first, without repeats", v)
		}
		rank = l.rank
	}
	return names, nil
}

// parseFlag parses a 0/1 (or false/true) option value.
func parseFlag(what, v string) (bool, error) {
	switch v {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s %q: must be 0 or 1", what, v)
	}
}

// validate checks the combinations the grammar itself cannot express.
func (c *SCTE35Config) validate() error {
	if !c.Emsg && c.MPDForm == "off" {
		return fmt.Errorf("scte35: emsg=0 needs mpd=bin or mpd=xml, or nothing is signaled")
	}
	if c.PreS > 0 {
		if !c.Slate {
			return fmt.Errorf("scte35 pre needs slate=1: the countdown is rendered on the video")
		}
		// The countdown must not reach back into the preceding break, which owns those
		// seconds and renders its own countdown there.
		if gap, ok := c.minGapS(); ok && c.PreS > gap {
			return fmt.Errorf("scte35 pre=%d does not fit in the %d s between breaks", c.PreS, gap)
		}
	}
	if c.Cmd == "insert" {
		if c.AdsPerBreak > 0 {
			return fmt.Errorf("scte35 ads needs cmd=timesignal")
		}
		return nil
	}
	if len(c.Levels) == 0 && c.AdsPerBreak == 0 {
		return fmt.Errorf("scte35 cmd=timesignal needs at least one seg level or ads>0")
	}
	if c.AdsPerBreak > 0 {
		// The creatives are the innermost level. An Advertisement must not contain nested
		// Advertisements, nor a Promo nested Promos (SCTE 35 §10.3.3.14).
		ad := scte35Levels[c.AdLevel]
		for _, name := range c.Levels {
			if scte35Levels[name].rank >= ad.rank {
				return fmt.Errorf("scte35 ads=%d: the %s creatives would be nested in the %s level",
					c.AdsPerBreak, c.AdLevel, name)
			}
		}
	}
	// Cue messages are identified by the second of their splice point, so two splice points
	// of one break must be at least a second apart.
	if c.AdsPerBreak > 0 {
		for _, d := range c.breakDurations() {
			if d < c.AdsPerBreak {
				return fmt.Errorf("scte35 ads=%d needs a break of at least %d s, got %d s",
					c.AdsPerBreak, c.AdsPerBreak, d)
			}
		}
	}
	return nil
}

// breakDurations returns the distinct break durations of the schedule.
func (c *SCTE35Config) breakDurations() []int {
	if c.Periodic != nil {
		return []int{c.Periodic.DurationS}
	}
	durs := make([]int, 0, len(c.Breaks))
	for _, b := range c.Breaks {
		durs = append(durs, b.DurationS)
	}
	return durs
}

// ParseSCTE35Config parses an scte35 option value, accumulating any error on the converter.
func (s *strConvAccErr) ParseSCTE35Config(key, val string) *SCTE35Config {
	if s.err != nil {
		return nil
	}
	cfg, err := CreateSCTE35Config(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return cfg
}

// scte35CuePoint is one cue message to deliver, with the splice point it applies to.
type scte35CuePoint struct {
	atS  int64      // splice point, in seconds from the availabilityStartTime
	durS int        // the duration to advertise for this message, 0 when it only closes
	id   uint64     // emsg.id and Event@id: unique per message (see scte35MsgID)
	cue  scte35.Cue // the message itself
}

// scte35MsgID is the emsg.id and Event@id of a message with its splice point at second
// atS since the epoch: that second, moved by scte35ClosingIDOffset for a closing message.
// Within a break the messages are at least a second apart (see validate), and the only
// other message that can share a closing message's second opens the next break.
func scte35MsgID(atS int64, closing bool) uint64 {
	id := uint32(atS)
	if closing {
		id += scte35ClosingIDOffset
	}
	return uint64(id)
}

// eventID derives a splice_event_id / segmentation_event_id for one level of a break. It is
// a function of the break start, so the start and the end message of a level carry the same
// id (SCTE 35 §10.3.3.5) and both survive an MPD refresh unchanged. Slot 0 is the break
// start second itself, which is what the pre-1.14 splice_insert messages used.
func scte35EventID(breakStartS int64, slot int) uint32 {
	return uint32(breakStartS) + uint32(slot)*scte35EventIDSpacing
}

// cuePoints returns the cue messages for one break occurrence, in splice-point order.
// astS is the availabilityStartTime in seconds since the epoch; the returned times are
// relative to it, as all media times in a livesim2 stream are.
func (c *SCTE35Config) cuePoints(b adBreakInst, astS int) []scte35CuePoint {
	breakStartS := int64(astS) + b.offsetS
	msgID := func(atS int64, closing bool) uint64 { return scte35MsgID(int64(astS)+atS, closing) }
	pts := func(atS int64) uint64 { return uint64(atS) * scte35.TimescaleHz % (1 << 33) }
	durPTS := uint64(b.durS) * scte35.TimescaleHz

	if c.Cmd == "insert" {
		out := []scte35CuePoint{{
			atS:  b.offsetS,
			durS: b.durS,
			id:   msgID(b.offsetS, false),
			cue: scte35.Cue{
				Cmd:          scte35.SpliceInsert,
				PTS:          pts(b.offsetS),
				Tier:         scte35.DefaultTier,
				EventID:      scte35EventID(breakStartS, 0),
				OutOfNetwork: true,
				BreakDurPTS:  durPTS,
				AutoReturn:   true,
			},
		}}
		if c.End {
			// The return to the network, which SCTE-35 signals as a splice_insert with
			// out_of_network_indicator = 0 and no break duration.
			endS := b.offsetS + int64(b.durS)
			out = append(out, scte35CuePoint{
				atS: endS,
				id:  msgID(endS, true),
				cue: scte35.Cue{
					Cmd:     scte35.SpliceInsert,
					PTS:     pts(endS),
					Tier:    scte35.DefaultTier,
					EventID: scte35EventID(breakStartS, 1),
				},
			})
		}
		return out
	}
	return c.timeSignalCuePoints(b, breakStartS, msgID, pts)
}

// timeSignalCuePoints builds the time_signal messages of one break: the opening message
// carrying the start descriptor of every level, one message at each boundary between
// creatives, and the closing message carrying the end descriptors in reverse order.
func (c *SCTE35Config) timeSignalCuePoints(b adBreakInst, breakStartS int64,
	msgID func(int64, bool) uint64, pts func(int64) uint64) []scte35CuePoint {

	durPTS := uint64(b.durS) * scte35.TimescaleHz
	level := func(name string, slot int, open bool, durPTS uint64, upid []byte) scte35.Level {
		l := scte35Levels[name]
		typeID := l.start
		if !open {
			typeID = l.end
			durPTS = 0 // the duration belongs to the opening descriptor
		}
		return scte35.Level{
			TypeID:      typeID,
			EventID:     scte35EventID(breakStartS, slot),
			DurationPTS: durPTS,
			UPIDType:    c.UPIDType,
			UPID:        upid,
		}
	}
	// Slots: one per named level, then one per creative segment.
	adSlot := func(i int) int { return len(c.Levels) + i - 1 }
	adStartS := func(i int) int64 {
		return b.offsetS + int64(b.durS)*int64(i)/int64(max(c.AdsPerBreak, 1))
	}
	adDurPTS := func(i int) uint64 {
		return uint64(adStartS(i+1)-adStartS(i)) * scte35.TimescaleHz
	}
	// creative is the descriptor of creative i (1-based). SCTE 35 2023r1 §10.3.3.14.4 numbers
	// the advertisements of a break with sub_segment_num and sub_segments_expected on their
	// Starts, where the segmentation type has them. segment_num and segments_expected number
	// the Breaks of a Program, which livesim2 does not do (§10.3.3.14.2), so they stay 0.
	creative := func(i int, open bool) scte35.Level {
		l := level(c.AdLevel, adSlot(i), open, adDurPTS(i-1), c.upid(b.id, c.AdLevel, i))
		if open && scte35.CarriesSubSegments(l.TypeID) {
			l.HasSubSegments, l.SubNum, l.SubExpected = true, uint8(i), uint8(c.AdsPerBreak)
		}
		return l
	}

	// Opening message: every level starts, plus the first creative.
	open := scte35CuePoint{atS: b.offsetS, durS: b.durS, id: msgID(b.offsetS, false)}
	open.cue = scte35.Cue{Cmd: scte35.TimeSignal, PTS: pts(b.offsetS), Tier: scte35.DefaultTier}
	for j, name := range c.Levels {
		open.cue.Levels = append(open.cue.Levels, level(name, j, true, durPTS, c.upid(b.id, name, 0)))
	}
	if c.AdsPerBreak > 0 {
		open.cue.Levels = append(open.cue.Levels, creative(1, true))
	}
	out := []scte35CuePoint{open}

	// One message at each boundary between creatives: the previous one ends, the next starts.
	for i := 1; i < c.AdsPerBreak; i++ {
		atS := adStartS(i)
		cp := scte35CuePoint{atS: atS, durS: int(adDurPTS(i) / scte35.TimescaleHz), id: msgID(atS, false)}
		cp.cue = scte35.Cue{
			Cmd:    scte35.TimeSignal,
			PTS:    pts(atS),
			Tier:   scte35.DefaultTier,
			Levels: []scte35.Level{creative(i, false), creative(i+1, true)},
		}
		out = append(out, cp)
	}

	if !c.End {
		return out
	}
	// Closing message: the last creative ends, then the levels close innermost first.
	endS := b.offsetS + int64(b.durS)
	closing := scte35CuePoint{atS: endS, id: msgID(endS, true)}
	closing.cue = scte35.Cue{Cmd: scte35.TimeSignal, PTS: pts(endS), Tier: scte35.DefaultTier}
	if c.AdsPerBreak > 0 {
		closing.cue.Levels = append(closing.cue.Levels, creative(c.AdsPerBreak, false))
	}
	for j := len(c.Levels) - 1; j >= 0; j-- {
		closing.cue.Levels = append(closing.cue.Levels, level(c.Levels[j], j, false, 0, c.upid(b.id, c.Levels[j], 0)))
	}
	return append(out, closing)
}

// upid returns the segmentation_upid of one level of break breakID, and of creative n (1-based)
// when n > 0. The one given with upid= is used for all of them, type 0 meaning none. Otherwise
// each level and creative gets its own URI, since every Start and End is to be uniquely
// identifiable by its UPID (SCTE 35 §10.3.3.13.1 and §10.3.3.14.1): the break itself
// urn:dashif:livesim2:break:<id>, and a level inside it that URI with :<level>[:<n>] appended.
func (c *SCTE35Config) upid(breakID uint64, level string, n int) []byte {
	switch {
	case c.UPIDType == 0:
		return nil
	case c.UPID != "":
		return []byte(c.UPID)
	}
	u := fmt.Appendf(nil, "urn:dashif:livesim2:break:%d", breakID)
	if level != "break" {
		u = fmt.Appendf(u, ":%s", level)
	}
	if n > 0 {
		u = fmt.Appendf(u, ":%d", n)
	}
	return u
}

// adSignaling is one active ad-signaling option and the break schedule it uses.
type adSignaling struct {
	opt string
	ab  *AdBreaks
	// multiPeriod tells whether the option puts events in the MPD, which the multi-period
	// options cannot carry (see verifyAdSignaling).
	inMPD bool
}

// activeAdSignaling lists the ad-signaling options set on a request.
func activeAdSignaling(cfg *ResponseConfig) []adSignaling {
	var out []adSignaling
	if cfg.SGAI != nil {
		out = append(out, adSignaling{"sgai", &cfg.SGAI.AdBreaks, true})
	}
	if cfg.SVTA != nil {
		out = append(out, adSignaling{"svta", &cfg.SVTA.AdBreaks, true})
	}
	if cfg.SCTE35 != nil {
		out = append(out, adSignaling{"scte35", &cfg.SCTE35.AdBreaks, cfg.SCTE35.MPDForm != "off"})
	}
	return out
}

// verifyAdSignaling checks the ad-signaling options against each other and against the
// multi-period options.
func verifyAdSignaling(cfg *ResponseConfig) error {
	active := activeAdSignaling(cfg)
	if len(active) == 0 {
		return nil
	}
	multiPeriod := cfg.PeriodsPerHour != nil || cfg.XlinkPeriodsPerHour != nil ||
		cfg.EtpPeriodsPerHour != nil || cfg.InsertAdFlag
	for _, a := range active {
		// The ad-break EventStream lives in the first Period. splitPeriod clones that Period
		// verbatim for every generated period, which would duplicate the events with
		// presentation times that are no longer rebased, so the multi-period options are out
		// for any signaling that goes into the MPD. Inband-only signaling is unaffected.
		if multiPeriod && a.inMPD {
			return fmt.Errorf("%s cannot be combined with periods/xlink/etp/insertad", a.opt)
		}
	}
	if cfg.SGAI != nil && cfg.SVTA != nil {
		// Both describe the ad of the same window on the main timeline (and both drive the
		// slate), so combining them would double-signal it. Use sgai_...;svta=1 to describe
		// the creatives of an SGAI ad pod.
		return fmt.Errorf("svta cannot be combined with sgai")
	}
	// Several signalings may mark the same breaks — SCTE-35 announcing the avail that an
	// Alternative-MPD event then fills is the point of combining them — but they must agree
	// on where the breaks are, since the stream has only one set of them.
	for _, a := range active[1:] {
		if !a.ab.equal(active[0].ab) {
			return fmt.Errorf("%s and %s must use the same ad-break schedule", active[0].opt, a.opt)
		}
	}
	return nil
}

// scte35SignalWindowS is how far outside the segment being generated cue points are looked
// for. The lead time bounds how early a cue is delivered, so one lead in each direction
// covers every message that can land in the segment.
func (c *SCTE35Config) signalWindowS() int {
	return c.LeadS
}

// scte35EmsgsForSegment returns the SCTE-35 emsg boxes to insert into a segment covering
// the media-time interval [segStart, segEnd) of the given timescale (media times are
// relative to the availabilityStartTime).
//
// A cue is delivered in the segment holding its announce point, LeadS seconds before the
// splice point: the one with segStart < announce <= segEnd, as the pre-1.14 splice_insert
// scheduling had it. A splice point less than LeadS after the availabilityStartTime is
// announced at the stream start, in the first segment. With Repeat the cue is also delivered
// in every later segment starting before the splice point, which is legal (SCTE 214-1
// §6.7.3 item 6 lets a client discard an emsg id it has already seen) and makes that
// discarding testable.
func scte35EmsgsForSegment(cfg *ResponseConfig, segStart, segEnd, timescale uint64) []*mp4.EmsgBox {
	sc := cfg.SCTE35
	if sc == nil || !sc.Emsg {
		return nil
	}
	segStartMS := int64(cfg.StartTimeS)*1000 + mediaTimeMS(segStart, timescale)
	w := sc.signalWindowS()
	insts := sc.instances(int(segStartMS), cfg.StartTimeS, w, w)
	var emsgs []*mp4.EmsgBox
	for _, b := range insts {
		for _, cp := range sc.cuePoints(b, cfg.StartTimeS) {
			spliceTicks := uint64(cp.atS) * timescale
			announceTicks := uint64(0)
			if cp.atS > int64(sc.LeadS) {
				announceTicks = spliceTicks - uint64(sc.LeadS)*timescale
			}
			deliver := segStart < announceTicks && announceTicks <= segEnd ||
				announceTicks == 0 && segStart == 0
			if sc.Repeat {
				deliver = deliver || announceTicks <= segEnd && segStart < spliceTicks
			}
			if !deliver {
				continue
			}
			emsgs = append(emsgs, &mp4.EmsgBox{
				Version:          1,
				TimeScale:        uint32(timescale),
				PresentationTime: spliceTicks,
				// A closing message has no duration of its own; its level was opened with
				// the duration (SCTE 214-1 §6.7.2.1 item 2 says the same for MPD events).
				EventDuration: uint32(uint64(cp.durS) * timescale),
				ID:            uint32(cp.id),
				SchemeIDURI:   scte35.SchemeIDURI,
				Value:         sc.Value,
				MessageData:   cp.cue.Binary(),
			})
		}
	}
	return emsgs
}

// addSCTE35Events injects the SCTE-35 EventStream into the period: one Event per cue
// message, carrying the message as <Signal><Binary> (mpd=bin, scheme
// urn:scte:scte35:2014:xml+bin) or as <Signal><SpliceInfoSection> (mpd=xml, scheme
// urn:scte:scte35:2013:xml).
//
// Event@presentationTime is an absolute offset from the availabilityStartTime, as the period
// starts at 0, so it is stable across MPD refreshes. Events are kept for as long as the
// segments covering them are available (SCTE 214-1 §6.7.2.1 item 4), and appear one lead
// time before their splice point, the same moment the inband cue does.
func addSCTE35Events(period *m.Period, cfg *ResponseConfig, nowMS int) {
	sc := cfg.SCTE35
	if sc == nil || sc.MPDForm == "off" {
		return
	}
	lookBackS := 0
	if cfg.TimeShiftBufferDepthS != nil {
		lookBackS = *cfg.TimeShiftBufferDepthS
	}
	insts := sc.instances(nowMS, cfg.StartTimeS, sc.LeadS, lookBackS)
	if len(insts) == 0 {
		return
	}
	scheme := m.SCTE35SchemeIdXMLBin
	if sc.MPDForm == "xml" {
		scheme = m.SCTE35SchemeIdXML
	}
	es := &m.EventStreamType{
		SchemeIdUri: m.AnyURI(scheme),
		Value:       sc.Value,
		Timescale:   m.Ptr(sc.Timescale),
	}
	nowS := int64(nowMS/1000) - int64(cfg.StartTimeS)
	for _, b := range insts {
		for _, cp := range sc.cuePoints(b, cfg.StartTimeS) {
			if cp.atS-int64(sc.LeadS) > nowS {
				continue // not announced yet
			}
			ev := &m.EventType{
				PresentationTime: uint64(cp.atS) * uint64(sc.Timescale),
				// Always set, so that a closing message is written with duration="0" rather
				// than with no @duration at all, which DASH defines as an unknown duration.
				// SCTE 214-1 §6.7.2.1 item 2: an event that closes another one "should have
				// a duration of zero and shall not be infinite".
				Duration: m.Ptr(uint64(cp.durS) * uint64(sc.Timescale)),
				Id:       m.Ptr(cp.id),
			}
			if sc.MPDForm == "xml" {
				ev.Signal = cp.cue.XMLSignal()
			} else {
				ev.Signal = cp.cue.BinarySignal()
			}
			es.Events = append(es.Events, ev)
		}
	}
	if len(es.Events) == 0 {
		return
	}
	period.EventStreams = append(period.EventStreams, es)
}
