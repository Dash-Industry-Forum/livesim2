package app

import (
	"errors"
	"fmt"
)

var (
	errNotFound = errors.New("not found")
	errGone     = errors.New("gone")
)

type errTooEarly struct {
	deltaMS int
}

func newErrTooEarly(deltaMS int) errTooEarly {
	return errTooEarly{deltaMS: deltaMS}
}

func (e errTooEarly) Error() string {
	return fmt.Sprintf("too early by %dms", e.deltaMS)
}
