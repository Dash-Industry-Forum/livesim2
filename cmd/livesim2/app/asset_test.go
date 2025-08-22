// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/stretchr/testify/require"
)

type wantedAssetData struct {
	nrReps         int
	loopDurationMS int
}

type wantedRepData struct {
	nrSegs         int
	initURI        string
	mpdTimescale   int // SegmentTemplate timescale
	mediaTimescale int
	duration       int
	editListOffset int64 // Offset of the elst box in the init segment
}

func TestLoadAsset(t *testing.T) {
	logger := slog.Default()
	testCases := []struct {
		desc         string
		assetPath    string
		segmentEndNr uint32
		ad           wantedAssetData
		rds          map[string]wantedRepData
	}{
		{
			desc:      "CTA-Wave AAC with editlist",
			assetPath: "assets/WAVE/av",
			ad: wantedAssetData{
				nrReps:         2,
				loopDurationMS: 8000,
			},
			rds: map[string]wantedRepData{
				"video25fps": {
					nrSegs:         4,
					initURI:        "video25fps/init.mp4",
					mpdTimescale:   12_800,
					mediaTimescale: 12_800,
					duration:       102_400,
				},
				"aac": {
					nrSegs:         5, // To get longer duration than video
					initURI:        "aac/init.mp4",
					mpdTimescale:   48_000,
					mediaTimescale: 48_000,
					duration:       476160, // 9.92s which is fine since longer than video
					editListOffset: 2048,
				},
			},
		},
		{
			desc:         "testpic_2s",
			assetPath:    "assets/testpic_2s",
			segmentEndNr: 0, // Will not be used
			ad: wantedAssetData{
				nrReps:         5,
				loopDurationMS: 8000,
			},
			rds: map[string]wantedRepData{
				"V300": {
					nrSegs:         4,
					initURI:        "V300/init.mp4",
					mpdTimescale:   1,
					mediaTimescale: 90_000,
					duration:       720_000,
				},
				"A48": {
					nrSegs:         4,
					initURI:        "A48/init.mp4",
					mpdTimescale:   1,
					mediaTimescale: 48_000,
					duration:       384_000,
				},
			},
		},
		{
			desc:         "testpic_2s with endNumber == 2",
			assetPath:    "assets/testpic_2s",
			segmentEndNr: 2, // Shorten representations to 2 segments via SegmentTemplate,
			ad: wantedAssetData{
				nrReps:         5,
				loopDurationMS: 4000,
			},
			rds: map[string]wantedRepData{
				"V300": {
					nrSegs:         2,
					initURI:        "V300/init.mp4",
					mpdTimescale:   1,
					mediaTimescale: 90_000,
					duration:       360_000,
				},
				"A48": {
					nrSegs:         2,
					initURI:        "A48/init.mp4",
					mpdTimescale:   1,
					mediaTimescale: 48_000,
					duration:       192_512,
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			vodRoot := "testdata"
			tmpDir := t.TempDir()
			if tc.segmentEndNr > 0 {
				if runtime.GOOS == "windows" {
					return // Skip test on Windows since the tree copy does not work properly
				}
				// Copy the the asset part of testdata to a temporary directory and shorten the representations
				src := path.Join(vodRoot, tc.assetPath)
				dst := path.Join(tmpDir, tc.assetPath)
				err := copyDir(src, dst)
				require.NoError(t, err)
				vodRoot = tmpDir
				err = setSegmentEndNr(path.Join(vodRoot, tc.assetPath), tc.segmentEndNr)
				require.NoError(t, err)
			}
			vodFS := os.DirFS(vodRoot)
			for _, writeRepData := range []bool{true, false} {
				// Write repData files the first time, and read them the second
				am := newAssetMgr(vodFS, tmpDir, writeRepData)
				err := am.discoverAssets(logger)
				require.NoError(t, err)
				asset, ok := am.findAsset(tc.assetPath)
				require.True(t, ok)
				require.NotNil(t, asset)
				require.Equal(t, tc.ad.nrReps, len(asset.Reps), "nr reps in asset %s", asset.AssetPath)
				require.Equal(t, tc.ad.loopDurationMS, asset.LoopDurMS)
				for repID, wrd := range tc.rds {
					rep, ok := asset.Reps[repID]
					require.True(t, ok, "repID %s not found in asset %s", repID, asset.AssetPath)
					require.NotNil(t, rep)
					require.Equal(t, wrd.nrSegs, len(rep.Segments), "repID %s in asset %s", repID, asset.AssetPath)
					require.Equal(t, wrd.initURI, rep.InitURI)
					require.Equal(t, wrd.mpdTimescale, rep.MpdTimescale, "repID %s in asset %s", repID, asset.AssetPath)
					require.Equal(t, wrd.mediaTimescale, rep.MediaTimescale, "repID %s in asset %s", repID, asset.AssetPath)
					require.Equal(t, wrd.duration, rep.duration())
					require.Equal(t, wrd.editListOffset, rep.EditListOffset)
				}
			}
		})
	}
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

func copyDir(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		var relPath = strings.Replace(path, srcDir, "", 1)
		if relPath == "" {
			return nil
		}
		if info.IsDir() {
			return os.Mkdir(filepath.Join(dstDir, relPath), 0755)
		} else {
			var data, err = os.ReadFile(filepath.Join(srcDir, relPath))
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dstDir, relPath), data, 0644)
		}
	})
}

// Set the endNumber attribute in all MPDs SegmentTemplate elements
func setSegmentEndNr(assetDir string, endNumber uint32) error {
	files, err := os.ReadDir(assetDir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".mpd" {
			mpdPath := filepath.Join(assetDir, file.Name())

			mpd, err := m.ReadFromFile(mpdPath)
			if err != nil {
				return err
			}
			p := mpd.Periods[0]
			for _, a := range p.AdaptationSets {
				stl := a.SegmentTemplate
				stl.EndNumber = &endNumber
			}
			mpdRaw, err := mpd.WriteToString("", false)
			if err != nil {
				return err
			}
			err = os.WriteFile(mpdPath, []byte(mpdRaw), 0644)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
