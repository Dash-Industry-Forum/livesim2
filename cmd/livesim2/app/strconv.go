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

// newStringConverter returns a new string converter for URL parsing.
func newStringConverter() *strConvAccErr {
	return &strConvAccErr{}
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
	keepSet := false
	vals := strings.Split(val, "-")
	utcTimingMethods := make([]UTCTimingMethod, len(vals))
	for i, val := range vals {
		utcVal := UTCTimingMethod(val)
		switch utcVal {
		case UtcTimingDirect, UtcTimingNtp, UtcTimingSntp,
			UtcTimingHttpXSDate, UtcTimingHttpXSDateMs,
			UtcTimingHttpISO, UtcTimingHttpISOMs,
			UtcTimingNone, UtcTimingHttpHead:
			utcTimingMethods[i] = utcVal
		case UtcTimingKeep:
			keepSet = true
		default:
			s.err = fmt.Errorf("key=%q, val=%q is not a valid UTC timing method", key, val)
		}
	}
	if keepSet && len(vals) > 1 {
		s.err = fmt.Errorf("UTC value keep set together with other values")
		return nil
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

// ParseSegStatusCodes parses a command line [{cycle:30, rsq: 0, code: 404, rep:video}]
func (s *strConvAccErr) ParseSegStatusCodes(key, val string) []SegStatusCodes {
	if s.err != nil {
		return nil
	}
	// remove all spaces, remove start [{ and end }], split on },{,
	trimmed := strings.ReplaceAll(val, " ", "")
	if len(trimmed) < 4 {
		s.err = fmt.Errorf("val=%q for key %q is too short", val, key)
		return nil
	}
	trimmed = trimmed[2 : len(trimmed)-2]
	parts := strings.Split(trimmed, "},{")
	codes := make([]SegStatusCodes, len(parts))
	for i, part := range parts {
		// split on , and :
		pairs := strings.Split(part, ",")
		for _, p := range pairs {
			kv := strings.Split(p, ":")
			if len(kv) != 2 {
				s.err = fmt.Errorf("val=%q for key %q is not a valid. Bad pair", val, key)
				return nil
			}
			switch kv[0] {
			case "cycle":
				codes[i].Cycle = s.Atoi("cycle", kv[1])
			case "rsq":
				codes[i].Rsq = s.Atoi("rsq", kv[1])
			case "code":
				codes[i].Code = s.Atoi("code", kv[1])
			case "rep":
				if kv[1] != "*" { // * and empty means all reps
					reps := strings.Split(kv[1], ",")
					codes[i].Reps = reps
				}
			default:
				s.err = fmt.Errorf("val=%q for key %q is not a valid. Unknown key", val, key)
			}
		}
		if codes[i].Cycle <= 0 {
			s.err = fmt.Errorf("val=%q for key %q is not a valid. cycle is too small", val, key)
		}
		if codes[i].Rsq < 0 {
			s.err = fmt.Errorf("val=%q for key %q is not a valid. rsq is too small", val, key)
		}
		if codes[i].Code < 400 || codes[i].Code > 599 {
			s.err = fmt.Errorf("val=%q for key %q is not a valid. code is not in range 400-599", val, key)
		}
	}
	return codes
}

func (s *strConvAccErr) ParseLossItvls(key, val string) []LossItvls {
	if s.err != nil {
		return nil
	}
	itvls, err := CreateAllLossItvls(val)
	if err != nil {
		s.err = fmt.Errorf("key=%s, err=%w", key, err)
		return nil
	}
	return itvls
}

func (s *strConvAccErr) ParseQuery(key, val string) *Query {
	if s.err != nil {
		return nil
	}
	q := &Query{
		parts: make(map[string][]string),
	}
	pairs := strings.Split(val, ",")
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		kv := strings.Split(pair, "=")
		switch len(kv) {
		case 2:
			q.parts[kv[0]] = append(q.parts[kv[0]], kv[1])
			parts = append(parts, fmt.Sprintf("%s=%s", kv[0], kv[1]))
		case 1:
			parts = append(parts, fmt.Sprintf("%s=%s", kv[0], kv[1]))
		default:
			s.err = fmt.Errorf("key=%s, err=%s", key, "invalid query pair")
		}
	}
	q.raw = strings.Join(parts, "&")
	return q
}
