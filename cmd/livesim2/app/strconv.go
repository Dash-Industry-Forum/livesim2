// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type strConvAccErr struct {
	err error
}

func (s *strConvAccErr) Atoi(key, val string) int {
	if s.err != nil {
		return 0
	}
	valInt, err := strconv.Atoi(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return 0
	}
	return valInt
}

func (s *strConvAccErr) AtoiPtr(key, val string) *int {
	if s.err != nil {
		return nil
	}
	valInt, err := strconv.Atoi(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return &valInt
}

// Atof parses a non-infinite floating point number
func (s *strConvAccErr) Atof(key, val string) *float64 {
	if s.err != nil {
		return nil
	}
	valFloat, err := strconv.ParseFloat(val, 64)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return &valFloat
}

// AtofPos parses a non-negative floating point number
func (s *strConvAccErr) AtofPosPtr(key, val string) *float64 {
	if s.err != nil {
		return nil
	}
	valFloat, err := strconv.ParseFloat(val, 64)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	if valFloat < 0 {
		s.err = fmt.Errorf("key=%s, val=%s must be non-negative", key, val)
		return nil
	}
	return &valFloat
}

// SplitUTCTimings splits a hyphen-separated list of UTC timing methods.
func (s *strConvAccErr) SplitUTCTimings(key, val string) []UTCTimingMethod {
	if s.err != nil {
		return nil
	}
	vals := strings.Split(val, "-")
	utcTimingMethods := make([]UTCTimingMethod, len(vals))
	for i, val := range vals {
		utcVal := UTCTimingMethod(val)
		switch utcVal {
		case UtcTimingDirect, UtcTimingNtp, UtcTimingSntp, UtcTimingHttpXSDate, UtcTimingHttpISO,
			UtcTimingNone, UtcTimingHead:
			utcTimingMethods[i] = utcVal
		default:
			s.err = fmt.Errorf("key=%q, val=%q is not a valid UTC timing method", key, val)
		}
	}
	return utcTimingMethods
}

// AtofInf parses a floating point number or the value "inf"
func (s *strConvAccErr) AtofInf(key, val string) float64 {
	if s.err != nil {
		return 0
	}
	if val == "inf" {
		return math.Inf(+1)
	}
	valFloat, err := strconv.ParseFloat(val, 64)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return 0
	}
	return valFloat
}
