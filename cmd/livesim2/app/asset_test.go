// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAsset(t *testing.T) {
	vodFS := os.DirFS("testdata")
	am := newAssetMgr(vodFS)
	err := am.discoverAssets()
	require.NoError(t, err)
	asset, ok := am.findAsset("assets/testpic_2s")
	require.True(t, ok)
	require.Equal(t, 2, len(asset.Reps))
	rep := asset.Reps["V300"]
	assert.Equal(t, "V300/init.mp4", rep.initURI)
	assert.Equal(t, 4, len(rep.segments))
	assert.Equal(t, 90000, rep.MediaTimescale)
	assert.Equal(t, 1, rep.MpdTimescale)
	assert.Equal(t, 720_000, rep.duration())
	assert.Equal(t, 8000, asset.LoopDurMS)
}
