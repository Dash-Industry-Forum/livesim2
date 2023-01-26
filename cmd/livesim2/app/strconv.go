package app

import (
	"fmt"
	"strconv"
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
