// Package scte35 implements parts of SCTE-35 according to SCTE-214-1 from 2022.
package scte35

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Comcast/gots/v2"
	"github.com/Comcast/gots/v2/scte35"
	"github.com/Eyevinn/mp4ff/mp4"
)

const (
	SchemeIDURI = "urn:scte:scte35:2013:bin"
)

// Returns if requested adsPerMinute value is valid (1, 2, or 3)
func IsValidSCTE35Interval(adsPerMinute int) error {
	switch adsPerMinute {
	case 1, 2, 3, 11, 12, 13:
		return nil
	default:
		return errors.New("scte35 per minute must be 1, 2, or 3")
	}
}

// CreateEmsgAhead generates an emsg SCTE-35 box if the the segment covers the time 7s before the ad start.
// Depending on perMinute parameter, the splice inserts are as follows::
// 1: 10s after full minute (20s duration)
// 2: 10s and 40s after full minute (10 duration)
// 3: 10s, 36s, 46s after full minute (10s duration)
func CreateEmsgAhead(log *slog.Logger, segStart, segEnd, timescale uint64, perMinute int) ([]*mp4.EmsgBox, error) {
	if err := IsValidSCTE35Interval(perMinute); err != nil {
		return nil, err
	}

	modMinute := segStart % (60 * timescale)
	minuteStart := segStart - modMinute
	var spliceInsertTimes []uint64
	var emsgs []*mp4.EmsgBox
	breakTypes := []scte35.SegDescType{scte35.SegDescProviderPOStart, scte35.SegDescProviderPOStart}

	adDuration := 10 * timescale
	timeSignal := false

	switch perMinute {
	case 1:
		adDuration = 20 * timescale
		spliceInsertTimes = []uint64{minuteStart + 10*timescale}
	case 2:
		spliceInsertTimes = []uint64{minuteStart + 10*timescale, minuteStart + 40*timescale}
	case 3:
		spliceInsertTimes = []uint64{minuteStart + 10*timescale, minuteStart + 36*timescale, minuteStart + 46*timescale}
	case 11:
		timeSignal = true
		adDuration = 20 * timescale
		spliceInsertTimes = []uint64{minuteStart + 10*timescale}
	case 12:
		timeSignal = true
		spliceInsertTimes = []uint64{minuteStart + 10*timescale, minuteStart + 40*timescale}
	case 13:
		timeSignal = true
		spliceInsertTimes = []uint64{minuteStart + 10*timescale, minuteStart + 36*timescale, minuteStart + 46*timescale}
	}
	// We do not need to look into next minute, since first start is 10s after full minute.
	inInterval := false
	var spliceTime uint64
	for _, sit := range spliceInsertTimes {
		announceTime := sit - 7*timescale
		if segStart < announceTime && announceTime <= segEnd {
			inInterval = true
			spliceTime = sit
			break
		}
	}
	if !inInterval {
		return nil, nil
	}
	emsgID := spliceTime / timescale
	p := SpliceInsertParams{
		PtsTime:                    uint64(spliceTime*90000/timescale) % (1 << 33),
		Duration:                   uint64(adDuration * 90000 / timescale),
		SpliceEventID:              uint32(emsgID),
		Tier:                       4095,
		UniqueProgramID:            0,
		AvailNum:                   0,
		AvailsExpected:             0,
		SpliceEventCancelIndicator: false,
		OutOfNetworkIndicator:      true,
		SpliceImmediateFlag:        false,
		AutoReturn:                 true,
	}

	e := mp4.EmsgBox{
		Version:          1,
		Flags:            0,
		TimeScale:        uint32(timescale),
		PresentationTime: uint64(spliceTime),
		EventDuration:    uint32(adDuration),
		ID:               uint32(emsgID),
		SchemeIDURI:      SchemeIDURI,
		Value:            "",
		MessageData:      CreateSpliceInsertPayload(p),
	}

	// If we're handling a timeSignal we need to add start and end segmentation descriptors into separate emsgs
	if timeSignal {
		for breakType := range breakTypes {
			e.MessageData = CreateTimeSignalInsertPayload(p, scte35.SegDescType(breakType), log)
			e.Value = "timesignal"
		}
	}

	emsgs = append(emsgs, &e)

	return emsgs, nil
}

type SpliceInsertParams struct {
	PtsTime                    uint64
	Duration                   uint64
	SpliceEventID              uint32
	Tier                       uint16
	UniqueProgramID            uint16
	AvailNum                   uint8
	AvailsExpected             uint8
	SpliceEventCancelIndicator bool
	OutOfNetworkIndicator      bool
	SpliceImmediateFlag        bool
	AutoReturn                 bool
}

// CreateSpliceInsertPayload creates a SCTE-35 splice_info_section including CRC.
func CreateSpliceInsertPayload(p SpliceInsertParams) []byte {
	s := scte35.CreateSCTE35()
	s.SetTier(uint16(p.Tier))
	cmd := scte35.CreateSpliceInsertCommand()
	cmd.SetUniqueProgramId(p.UniqueProgramID)
	cmd.SetEventID(p.SpliceEventID)
	cmd.SetAvailNum(p.AvailNum)
	cmd.SetAvailsExpected(p.AvailsExpected)
	cmd.SetIsEventCanceled(p.SpliceEventCancelIndicator)
	if p.Duration != 0 {
		cmd.SetHasDuration(true)
		cmd.SetDuration(gots.PTS(p.Duration))
		cmd.SetIsAutoReturn(p.AutoReturn)
	}
	cmd.SetHasPTS(true)
	cmd.SetPTS(gots.PTS(p.PtsTime))
	cmd.SetIsOut(p.OutOfNetworkIndicator)
	cmd.SetSpliceImmediate(p.SpliceImmediateFlag)
	s.SetCommandInfo(cmd)
	return s.UpdateData()
}

func CreateTimeSignalInsertPayload(p SpliceInsertParams, breakType scte35.SegDescType, log *slog.Logger) []byte {
	// Create a time_signal structure (second option to signal Ad Break).
	t := scte35.CreateSCTE35()
	t.SetTier(uint16(p.Tier))

	cmd := scte35.CreateTimeSignalCommand()
	cmd.SetHasPTS(true)

	if breakType == scte35.SegDescProviderPOEnd {
		cmd.SetPTS(gots.PTS(p.PtsTime + p.Duration))
	} else {
		cmd.SetPTS(gots.PTS(p.PtsTime))
	}

	log.Debug("time_signal", "value", cmd)

	descriptors := CreateDescriptors(p, breakType)

	for i, d := range descriptors {
		log.Debug(
			"descriptor",
			"index", i,
			"value", fmt.Sprintf("%+v", d),
		)
	}

	t.SetDescriptors(descriptors)

	t.SetCommandInfo(cmd)
	log.Debug("scte35", "value", fmt.Sprintf("%+v", t))

	d := t.Descriptors()

	log.Debug("SCTE35 Descriptors", "descriptors", fmt.Sprintf("%+v", d[0]))

	return t.UpdateData()
}

func CreateDescriptors(p SpliceInsertParams, breakType scte35.SegDescType) []scte35.SegmentationDescriptor {
	// Create a descriptor

	var descList []scte35.SegmentationDescriptor

	desc := scte35.CreateSegmentationDescriptor()

	if p.Duration != 0 {
		desc.SetHasDuration(true)
		desc.SetDuration(gots.PTS(p.Duration))
	}

	desc.SetEventID(p.SpliceEventID)
	desc.SetTypeID(breakType)
	desc.SetIsEventCanceled(p.SpliceEventCancelIndicator)
	desc.SetIsWebDeliveryAllowed(true)
	desc.SetHasNoRegionalBlackout(true)
	desc.SetSegmentNumber(1)
	desc.SetSegmentsExpected(1)
	desc.SetHasSubSegments(false)

	descList = append(descList, desc)

	return descList
}
