// Copyright 2026, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package internal

import (
	"fmt"
	"runtime/debug"
	"time"
)

// Version returns the version of the running binary and the time of its
// commit, such as "v1.13.0, date: 2026-08-11", from the build information the
// Go toolchain embeds.
//
// Since Go 1.24, building a package in a git checkout in module mode embeds
// the version from the tag: v1.13.0 at the tag, a pseudo-version such as
// v1.13.1-0.20260930184241-f419ca8067b3 after it, and +dirty with uncommitted
// changes, untracked files included. go install of a released version embeds
// that version. A build in workspace mode (go.work) embeds neither version nor
// commit, and gives "(devel)"; so does a build of files, such as
// go build ./cmd/livesim2/main.go, and a build without the .git directory.
func Version() string {
	info, _ := debug.ReadBuildInfo()
	return versionString(info)
}

// versionString formats the version from the build information, which may be nil.
func versionString(info *debug.BuildInfo) string {
	if info == nil {
		return "(devel)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	for _, s := range info.Settings {
		if s.Key != "vcs.time" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
			return fmt.Sprintf("%s, date: %s", version, t.UTC().Format(time.DateOnly))
		}
	}
	return version
}
