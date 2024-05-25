package patch

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/beevik/etree"
	"github.com/stretchr/testify/require"
)

func TestNewPatchDoc(t *testing.T) {
	d := etree.NewDocument()
	err := d.ReadFromFile("testdata/testpic_2s_1.mpd")
	require.NoError(t, err)
	err = d.ReadFromFile("testdata/testpic_2s_2.mpd")
	require.NoError(t, err)
	oldRoot := d.Root()
	newRoot := d.Root()
	pDoc, err := newPatchDoc(oldRoot, newRoot)
	require.NoError(t, err)
	require.NotNil(t, pDoc)
}

const wantedPatchSegmentTimelineTime = (`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<Patch xmlns="urn:mpeg:dash:schema:mpd-patch:2020" ` +
	`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="urn:mpeg:dash:schema:mpd-patch:2020 DASH-MPD-PATCH.xsd" ` +
	`mpdId="base" originalPublishTime="2024-03-28T15:43:10Z" publishTime="2024-03-28T15:43:18Z">` + "\n" +
	`  <replace sel="/MPD/@publishTime">2024-03-28T15:43:18Z</replace>` + "\n" +
	`  <replace sel="/MPD/PatchLocation[1]">` + "\n" +
	`    <PatchLocation ttl="60">/patch/livesim2/patch_60/segtimeline_1/testpic_2s/Manifest.mpp?publishTime=2024-03-28T15%3A43%3A18Z</PatchLocation>` + "\n" +
	`  </replace>` + "\n" +
	`  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>` + "\n" +
	`  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">` + "\n" +
	`    <S t="82158745728000" d="96256" r="2"/>` + "\n" +
	`  </add>` + "\n" +
	`  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>` + "\n" +
	`  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">` + "\n" +
	`    <S t="154047648240000" d="180000" r="30"/>` + "\n" +
	`  </add>` + "\n" +
	`</Patch>` + "\n")

const wantedPatchSegmentTimelineNumber = (`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<Patch xmlns="urn:mpeg:dash:schema:mpd-patch:2020" ` +
	`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="urn:mpeg:dash:schema:mpd-patch:2020 DASH-MPD-PATCH.xsd" ` +
	`mpdId="base" originalPublishTime="2024-03-28T15:43:10Z" publishTime="2024-03-28T15:43:18Z">` + "\n" +
	`  <replace sel="/MPD/@publishTime">2024-03-28T15:43:18Z</replace>` + "\n" +
	`  <replace sel="/MPD/PatchLocation[1]">` + "\n" +
	`    <PatchLocation ttl="60">/patch/livesim2/patch_60/segtimelinenr_1/testpic_2s/Manifest.mpp?publishTime=2024-03-28T15%3A43%3A18Z</PatchLocation>` + "\n" +
	`  </replace>` + "\n" +
	`  <replace sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/@startNumber">855820268</replace>` + "\n" +
	`  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>` + "\n" +
	`  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;1&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">` + "\n" +
	`    <S t="82158745728000" d="96256" r="2"/>` + "\n" +
	`  </add>` + "\n" +
	`  <replace sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/@startNumber">855820268</replace>` + "\n" +
	`  <remove sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline/S[1]"/>` + "\n" +
	`  <add sel="/MPD/Period[@id=&apos;P0&apos;]/AdaptationSet[@id=&apos;2&apos;]/SegmentTemplate/SegmentTimeline" pos="prepend">` + "\n" +
	`    <S t="154047648240000" d="180000" r="30"/>` + "\n" +
	`  </add>` + "\n" +
	`</Patch>` + "\n")

func TestDiff(t *testing.T) {
	cases := []struct {
		desc             string
		oldMPD           string
		newMPD           string
		wantedDiff       string
		wantedDiffFile   string
		wantedExpiration time.Time
		wantedErr        string
	}{
		{
			desc:             "multiPeriodPatch",
			oldMPD:           "testdata/multiperiod_1.mpd",
			newMPD:           "testdata/multiperiod_2.mpd",
			wantedDiffFile:   "testdata/multiperiod_patch.mpp",
			wantedExpiration: time.Date(2024, 4, 21, 6, 12, 8, 0, time.UTC),
		},
		{
			desc:             "multiPeriodPatch at period change",
			oldMPD:           "testdata/segtimeline_multiper_full_min.mpd",
			newMPD:           "testdata/segtimeline_multiper_after_full_min.mpd",
			wantedDiffFile:   "testdata/segtimeline_multiper_patch_after_full_min.mpp",
			wantedExpiration: time.Date(2024, 5, 24, 15, 13, 10, 0, time.UTC),
		},
		{
			desc:      "too big publishTime diff vs ttl",
			oldMPD:    "testdata/testpic_2s_1.mpd",
			newMPD:    "testdata/testpic_2s_2_late_publish.mpd",
			wantedErr: ErrPatchTooLate.Error(),
		},
		{
			desc:             "segmentTimelineTime",
			oldMPD:           "testdata/testpic_2s_1.mpd",
			newMPD:           "testdata/testpic_2s_2.mpd",
			wantedDiff:       wantedPatchSegmentTimelineTime,
			wantedExpiration: time.Date(2024, 3, 28, 15, 44, 20, 0, time.UTC),
		}, {
			desc:             "segmentTimelineNumber",
			oldMPD:           "testdata/testpic_2s_snr_1.mpd",
			newMPD:           "testdata/testpic_2s_snr_2.mpd",
			wantedDiff:       wantedPatchSegmentTimelineNumber,
			wantedExpiration: time.Date(2024, 3, 28, 15, 44, 20, 0, time.UTC),
		},
		{
			desc:       "no diff",
			oldMPD:     "testdata/testpic_2s_snr_1.mpd",
			newMPD:     "testdata/testpic_2s_snr_1.mpd",
			wantedDiff: "",
			wantedErr:  ErrPatchSamePublishTime.Error(),
		},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			in1, err := os.ReadFile(c.oldMPD)
			require.NoError(t, err)
			in2, err := os.ReadFile(c.newMPD)
			require.NoError(t, err)
			patch, expiration, err := MPDDiff(in1, in2)
			if c.wantedErr != "" {
				require.Error(t, err, c.wantedErr)
				return
			}
			require.NoError(t, err)
			expirationDiff := c.wantedExpiration.Sub(expiration)
			require.Equal(t, time.Duration(0), expirationDiff)
			patch.Indent(2)
			out, err := patch.WriteToString()
			wantedDiff := c.wantedDiff
			if c.wantedDiffFile != "" {
				//os.WriteFile(c.wantedDiffFile, []byte(out), 0o644)
				d, err := os.ReadFile(c.wantedDiffFile)
				require.NoError(t, err)
				wantedDiff = string(d)
				wantedDiff = strings.Replace(wantedDiff, "\r\n", "\n", -1) // Windows line endings
			}
			require.NoError(t, err)
			require.Equal(t, wantedDiff, out)
		})
	}
}

func TestAttrDiff(t *testing.T) {
	oldAttr := []etree.Attr{
		{Key: "publishTime", Value: "2021-07-01T00:00:00Z"},
		{Key: "duration", Value: "PT2S"},
		{Key: "minimumupdatePeriod", Value: "PT2S"},
	}
	newAttr := []etree.Attr{
		{Key: "publishTime", Value: "2021-07-01T00:00:10Z"},
		{Key: "availabilityStartTime", Value: "1970-01-01T00:00:00Z"},
		{Key: "minimumupdatePeriod", Value: "PT2S"},
	}
	ac, err := compareAttributes(oldAttr, newAttr)
	require.NoError(t, err)
	expected := attrChange{
		Added:   []etree.Attr{{Key: "availabilityStartTime", Value: "1970-01-01T00:00:00Z"}},
		Removed: []etree.Attr{{Key: "duration", Value: "PT2S"}},
		Changed: []etree.Attr{{Key: "publishTime", Value: "2021-07-01T00:00:10Z"}},
	}
	diff := cmp.Diff(expected, ac, cmp.Options{cmp.Comparer(func(x, y etree.Attr) bool {
		return x.Key == y.Key && x.Value == y.Value
	})})
	require.Equal(t, "", diff)
}
