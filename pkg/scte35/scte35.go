// Package scte35 implements parts of SCTE-35 according to SCTE-214-1 from 2022.
package scte35

import (
	"errors"

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
	case 1, 2, 3:
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
func CreateEmsgAhead(segStart, segEnd, timescale uint64, perMinute int) (*mp4.EmsgBox, error) {
	if err := IsValidSCTE35Interval(perMinute); err != nil {
		return nil, err
	}
	modMinute := segStart % (60 * timescale)
	minuteStart := segStart - modMinute
	var spliceInsertTimes []uint64
	adDuration := 10 * timescale
	switch perMinute {
	case 1:
		adDuration = 20 * timescale
		spliceInsertTimes = []uint64{minuteStart + 10*timescale}
	case 2:
		spliceInsertTimes = []uint64{minuteStart + 10*timescale, minuteStart + 40*timescale}
	case 3:
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
	return &e, nil
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
