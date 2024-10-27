// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadAsset(t *testing.T) {
	vodFS := os.DirFS("testdata")
	tmpDir := t.TempDir()
	am := newAssetMgr(vodFS, tmpDir, true)
	logger := slog.Default()
	err := am.discoverAssets(logger)
	require.NoError(t, err)
	// This was first time
	asset, ok := am.findAsset("assets/testpic_2s")
	require.True(t, ok)
	require.Equal(t, 5, len(asset.Reps))
	rep := asset.Reps["V300"]
	assert.Equal(t, "V300/init.mp4", rep.InitURI)
	assert.Equal(t, 4, len(rep.Segments))
	assert.Equal(t, 90000, rep.MediaTimescale)
	assert.Equal(t, 1, rep.MpdTimescale)
	assert.Equal(t, 720_000, rep.duration())
	assert.Equal(t, 8000, asset.LoopDurMS)
	// Second time we load using gzipped repData files
	am = newAssetMgr(vodFS, tmpDir, true)
	err = am.discoverAssets(logger)
	require.NoError(t, err)
	asset, ok = am.findAsset("assets/testpic_2s")
	require.True(t, ok)
	require.Equal(t, 5, len(asset.Reps))
	rep = asset.Reps["V300"]
	assert.Equal(t, "V300/init.mp4", rep.InitURI)
	assert.Equal(t, 4, len(rep.Segments))
	assert.Equal(t, 90000, rep.MediaTimescale)
	assert.Equal(t, 1, rep.MpdTimescale)
	assert.Equal(t, 720_000, rep.duration())
	assert.Equal(t, 8000, asset.LoopDurMS)
}

func TestAssetLookupForNameOverlap(t *testing.T) {
	am := assetMgr{}
	am.assets = make(map[string]*asset)
	am.assets["assets/testpic_2s"] = &asset{AssetPath: "assets/testpic_2s"}
	am.assets["assets/testpic_2s_1"] = &asset{AssetPath: "assets/testpic_2s_1"}
	uri := "assets/testpic_2s_1/rep1/init.mp4"
	a, ok := am.findAsset(uri)
	require.True(t, ok)
	require.Equal(t, "assets/testpic_2s_1", a.AssetPath)
}
