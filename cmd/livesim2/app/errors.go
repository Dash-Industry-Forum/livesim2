// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"errors"
	"fmt"
)

var (
	errNotFound       = errors.New("not found")
	errGone           = errors.New("gone")
	errNotLive        = errors.New("the MPD cannot be made live")
	ErrAtoInfTimeline = errors.New("infinite availabilityTimeOffset for SegmentTimeline")
	// errCannotEncrypt is returned when drm or eccp is requested for a video or audio
	// track that livesim2 cannot encrypt. It maps to HTTP 400.
	errCannotEncrypt = errors.New("drm: track cannot be encrypted")
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
