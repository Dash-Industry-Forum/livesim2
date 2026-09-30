// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package internal

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionString(t *testing.T) {
	built := func(version, vcsTime string) *debug.BuildInfo {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/Dash-Industry-Forum/livesim2", Version: version}}
		if vcsTime != "" {
			info.Settings = []debug.BuildSetting{
				{Key: "vcs.revision", Value: "f419ca8067b3b02528ebdf809fdf7d1c3dcfaba6"},
				{Key: "vcs.time", Value: vcsTime},
			}
		}
		return info
	}
	cases := []struct {
		desc string
		info *debug.BuildInfo
		want string
	}{
		{"tag build", built("v1.13.0", "2026-08-11T08:00:00Z"), "v1.13.0, date: 2026-08-11"},
		{"build after the tag, with local changes",
			built("v1.13.1-0.20260930184241-f419ca8067b3+dirty", "2026-09-30T18:42:41Z"),
			"v1.13.1-0.20260930184241-f419ca8067b3+dirty, date: 2026-09-30"},
		{"go install of a release has no vcs time", built("v1.13.0", ""), "v1.13.0"},
		{"the date is in UTC", built("v1.13.0", "2026-08-11T23:30:00-02:00"), "v1.13.0, date: 2026-08-12"},
		{"workspace build", built("(devel)", ""), "(devel)"},
		{"build of files, not a package", built("", ""), "(devel)"},
		{"no build information", nil, "(devel)"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			require.Equal(t, c.want, versionString(c.info))
		})
	}
}

// TestVersion checks that a test binary reports what the toolchain embedded
// in it, without asserting a value: that depends on how the test was built.
func TestVersion(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	require.True(t, ok)
	require.Equal(t, versionString(info), Version())
}
