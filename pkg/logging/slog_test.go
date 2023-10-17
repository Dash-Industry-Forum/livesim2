// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package logging

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitSlog(t *testing.T) {
	cases := []struct {
		format    string
		level     string
		expectErr bool
	}{
		{"text", "DEBUG", false},
		{"json", "INFO", false},
		{"json", "WARN", false},
		{"text", "ERROR", false},
		{"fish", "DEBUG", true},
		{"text", "FISH", true},
	}

	for _, c := range cases {
		err := InitSlog(c.level, c.format)
		if c.expectErr {
			require.Error(t, err, "InitSlog(%q, %q) should fail", c.level, c.format)
		} else {
			require.NoError(t, err)
			require.Equal(t, c.level, LogLevel())
		}
	}
}
