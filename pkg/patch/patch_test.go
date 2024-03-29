package patch

import (
	"os"
	"testing"

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
		desc       string
		oldMPD     string
		newMPD     string
		wantedDiff string
		wantedErr  string
	}{
		{
			desc:       "too big publishTime diff vs ttl",
			oldMPD:     "testdata/testpic_2s_1.mpd",
			newMPD:     "testdata/testpic_2s_2_late_publish.mpd",
			wantedDiff: "",
			wantedErr:  ErrPatchTooLate.Error(),
		},
		{
			desc:       "segmentTimelineTime",
			oldMPD:     "testdata/testpic_2s_1.mpd",
			newMPD:     "testdata/testpic_2s_2.mpd",
			wantedDiff: wantedPatchSegmentTimelineTime,
		}, {
			desc:       "segmentTimelineNumber",
			oldMPD:     "testdata/testpic_2s_snr_1.mpd",
			newMPD:     "testdata/testpic_2s_snr_2.mpd",
			wantedDiff: wantedPatchSegmentTimelineNumber,
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
			patch, err := MPDDiff(in1, in2)
			if c.wantedErr != "" {
				require.Error(t, err, c.wantedErr)
				return
			}
			require.NoError(t, err)
			patch.Indent(2)
			out, err := patch.WriteToString()
			//os.WriteFile(fmt.Sprintf("%s.mpp", c.desc), []byte(out), 0o644)
			require.NoError(t, err)
			require.Equal(t, c.wantedDiff, out)
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
