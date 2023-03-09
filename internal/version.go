// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package internal

import (
	_ "embed"
	"fmt"
	"strconv"
	"time"
)

var (
	commitVersion string = "v0.5.1"     // Should be updated during build
	commitDate    string = "1678368502" // commitDate in Epoch seconds (can be filled/updated in during build)
)

// GetVersion - get version, commitHash, commitDate, expirationDate depending on what is inserted
func GetVersion() string {
	seconds, _ := strconv.Atoi(commitDate)
	msg := commitVersion
	if commitDate != "" {
		t := time.Unix(int64(seconds), 0)
		msg += fmt.Sprintf(", date: %s", t.Format("2006-01-02"))
	}
	return msg
}

// CheckVersion
func CheckVersion(printVersion bool) {
	if printVersion {
		PrintVersion()
	}
}

// PrintVersion prints the version to stdout.
func PrintVersion() {
	fmt.Printf("%s\n", GetVersion())
}
