package app

import (
	"context"
	"os"
	"strings"
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
)

func TestAddVideoInit(t *testing.T) {
	videoData, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)
	chName, chDir := "testpic", "testdir/testpic"
	ctx := context.TODO()
	chCfg := ChannelConfig{
		Name:                  chName,
		TimeShiftBufferDepthS: 60,
	}
	ch := newChannel(ctx, chCfg, chDir)
	strm := stream{
		chName:    chName,
		chDir:     chDir,
		trName:    "video",
		ext:       "cmfv",
		mediaType: "video",
	}
	sr := bits.NewFixedSliceReader(videoData)
	decFile, err := mp4.DecodeFileSR(sr)
	assert.NoError(t, err)
	init := decFile.Init
	err = ch.addInitDataAndUpdateTimescale(strm, init)
	assert.NoError(t, err)
	assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), ch.mpd.AvailabilityStartTime)
	p := ch.mpd.Periods[0]
	assert.Equal(t, 1, len(p.AdaptationSets))
	asSet := p.AdaptationSets[0]
	assert.Equal(t, uint32(1), *asSet.Id)
	assert.Equal(t, m.RFC6838ContentTypeType("video"), asSet.ContentType)
	assert.Equal(t, "video/mp4", asSet.MimeType)
	assert.Equal(t, "und", asSet.Lang)
	stl := asSet.SegmentTemplate
	assert.NotNil(t, stl)
	assert.Equal(t, "$RepresentationID$/init.cmfv", stl.Initialization)
	assert.Equal(t, "$RepresentationID$/$Number$.cmfv", stl.Media)
	assert.Equal(t, uint32(90000), *asSet.SegmentTemplate.Timescale)
	rep := asSet.Representations[0]
	assert.Equal(t, "video", rep.Id)
	assert.Equal(t, 800000, int(rep.Bandwidth))
	assert.Equal(t, "avc1.64001E", rep.Codecs)
}

func TestVideoDataFromInit(t *testing.T) {
	videoData, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)
	chName, chDir := "testpic", "testdir/testpic"
	ctx := context.TODO()
	chCfg := ChannelConfig{
		Name:                  chName,
		TimeShiftBufferDepthS: 60,
	}
	ch := newChannel(ctx, chCfg, chDir)
	strm := stream{
		chName:    chName,
		chDir:     chDir,
		trName:    "video",
		ext:       "cmfv",
		mediaType: "video",
	}
	sr := bits.NewFixedSliceReader(videoData)
	decFile, err := mp4.DecodeFileSR(sr)
	assert.NoError(t, err)
	init := decFile.Init
	err = ch.addInitDataAndUpdateTimescale(strm, init)
	assert.NoError(t, err)
	assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), ch.mpd.AvailabilityStartTime)
	p := ch.mpd.Periods[0]
	assert.Equal(t, 1, len(p.AdaptationSets))
	asSet := p.AdaptationSets[0]
	assert.Equal(t, uint32(1), *asSet.Id)
	assert.Equal(t, m.RFC6838ContentTypeType("video"), asSet.ContentType)
	assert.Equal(t, "video/mp4", asSet.MimeType)
	stl := asSet.SegmentTemplate
	assert.NotNil(t, stl)
	assert.Equal(t, "$RepresentationID$/init.cmfv", stl.Initialization)
	assert.Equal(t, "$RepresentationID$/$Number$.cmfv", stl.Media)
	assert.Equal(t, uint32(90000), *asSet.SegmentTemplate.Timescale)
	rep := asSet.Representations[0]
	assert.Equal(t, "video", rep.Id)
	assert.Equal(t, 800000, int(rep.Bandwidth))
	assert.Equal(t, "avc1.64001E", rep.Codecs)
	assert.Equal(t, 640, int(rep.Width))
	assert.Equal(t, 350, int(rep.Height))
}

func TestGetLang(t *testing.T) {
	cases := []struct {
		mdhdLang string
		elngLang string
		expected string
	}{
		{mdhdLang: "```", elngLang: "", expected: "und"},
		{mdhdLang: "se`", elngLang: "", expected: "se"},
		{mdhdLang: "swe", elngLang: "se", expected: "se"},
	}
	for _, c := range cases {
		mdia := mp4.MdiaBox{}
		mdhd := mp4.MdhdBox{}
		mdhd.SetLanguage(c.mdhdLang)
		mdia.AddChild(&mdhd)
		if c.elngLang != "" {
			elng := mp4.ElngBox{}
			elng.Language = c.elngLang
			mdia.AddChild(&elng)
		}
		gotLang := getLang(&mdia)
		assert.Equal(t, c.expected, gotLang)
	}
}

func TestLabelsFromInit(t *testing.T) {
	videoData, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)
	labeledInit := func() *mp4.InitSegment {
		decFile, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(videoData))
		assert.NoError(t, err)
		udta := &mp4.UdtaBox{}
		udta.AddChild(&mp4.LablBox{Flags: mp4.LablIsGroupLabelFlag, LabelID: 1, Language: "en", Label: "Main video"})
		udta.AddChild(&mp4.LablBox{LabelID: 1, Language: "sv-SE", Label: "Huvudvideo"})
		decFile.Init.Moov.Trak.AddChild(udta)
		return decFile.Init
	}
	chName, chDir := "testpic", "testdir/testpic"

	chCfg := ChannelConfig{Name: chName, TimeShiftBufferDepthS: 60}
	ch := newChannel(context.TODO(), chCfg, chDir)
	for _, trName := range []string{"video", "video2"} {
		strm := stream{chName: chName, chDir: chDir, trName: trName, ext: "cmfv", mediaType: "video"}
		err = ch.addInitDataAndUpdateTimescale(strm, labeledInit())
		assert.NoError(t, err)
	}
	asSet := ch.mpd.Periods[0].AdaptationSets[0]
	assert.Equal(t, 2, len(asSet.Representations))
	assert.Equal(t, []*m.LabelType{{Id: 1, Lang: "en", Value: "Main video"}}, asSet.GroupLabels,
		"one group label for both representations")
	for _, rep := range asSet.Representations {
		assert.Equal(t, []*m.LabelType{{Id: 1, Lang: "sv-SE", Value: "Huvudvideo"}}, rep.Labels)
	}
	mpdOut, err := ch.mpd.WriteToString("", false)
	assert.NoError(t, err)
	assert.Contains(t, mpdOut, `<GroupLabel id="1" lang="en">Main video</GroupLabel>`)
	assert.Contains(t, mpdOut, `<Label id="1" lang="sv-SE">Huvudvideo</Label>`)

	// A configured displayName replaces the labels of the track.
	chCfg.Reps = []RepresentationConfig{{Name: "video", DisplayName: "Configured name"}}
	ch = newChannel(context.TODO(), chCfg, chDir)
	strm := stream{chName: chName, chDir: chDir, trName: "video", ext: "cmfv", mediaType: "video"}
	err = ch.addInitDataAndUpdateTimescale(strm, labeledInit())
	assert.NoError(t, err)
	asSet = ch.mpd.Periods[0].AdaptationSets[0]
	assert.Nil(t, asSet.GroupLabels)
	assert.Equal(t, []*m.LabelType{{Value: "Configured name"}}, asSet.Representations[0].Labels)
}

func TestGroupLabelPlacement(t *testing.T) {
	videoData, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)
	chName, chDir := "testpic", "testdir/testpic"
	chCfg := ChannelConfig{
		Name:                  chName,
		TimeShiftBufferDepthS: 60,
		// Different languages put the tracks in different Adaptation Sets.
		Reps: []RepresentationConfig{{Name: "en", Language: "en"}, {Name: "sv", Language: "sv"}, {Name: "sv2", Language: "sv"}},
	}
	ch := newChannel(context.TODO(), chCfg, chDir)
	addTrack := func(trName string, labls ...*mp4.LablBox) {
		t.Helper()
		decFile, err := mp4.DecodeFileSR(bits.NewFixedSliceReader(videoData))
		assert.NoError(t, err)
		udta := &mp4.UdtaBox{}
		for _, labl := range labls {
			udta.AddChild(labl)
		}
		decFile.Init.Moov.Trak.AddChild(udta)
		strm := stream{chName: chName, chDir: chDir, trName: trName, ext: "cmfv", mediaType: "video"}
		assert.NoError(t, ch.addInitDataAndUpdateTimescale(strm, decFile.Init))
	}
	langGroup := func() *mp4.LablBox {
		return &mp4.LablBox{Flags: mp4.LablIsGroupLabelFlag, LabelID: 1, Label: "Language"}
	}
	p := ch.mpd.Periods[0]

	addTrack("en", langGroup(), &mp4.LablBox{LabelID: 1, Language: "en", Label: "English"},
		&mp4.LablBox{Flags: mp4.LablIsGroupLabelFlag, LabelID: 2, Language: "en", Label: "Main video"},
		&mp4.LablBox{LabelID: 2, Language: "sv", Label: "Huvudvideo"})
	assert.Empty(t, p.GroupLabels)
	assert.Equal(t, []*m.LabelType{{Id: 1, Value: "Language"}, {Id: 2, Lang: "en", Value: "Main video"}},
		p.AdaptationSets[0].GroupLabels, "all labels in one Adaptation Set")

	addTrack("sv", langGroup(), &mp4.LablBox{LabelID: 1, Language: "sv", Label: "Svenska"})
	addTrack("sv2", langGroup(), &mp4.LablBox{LabelID: 1, Language: "sv", Label: "Svenska HD"})
	assert.Equal(t, 2, len(p.AdaptationSets))
	assert.Equal(t, []*m.LabelType{{Id: 1, Value: "Language"}}, p.GroupLabels,
		"group 1 spans two Adaptation Sets")
	assert.Equal(t, []*m.LabelType{{Id: 2, Lang: "en", Value: "Main video"}}, p.AdaptationSets[0].GroupLabels)
	assert.Empty(t, p.AdaptationSets[1].GroupLabels)

	mpdOut, err := ch.mpd.WriteToString("", false)
	assert.NoError(t, err)
	assert.Equal(t, 1, strings.Count(mpdOut, `<GroupLabel id="1">Language</GroupLabel>`))
	assert.Equal(t, 1, strings.Count(mpdOut, `<GroupLabel id="2" lang="en">Main video</GroupLabel>`))
}
