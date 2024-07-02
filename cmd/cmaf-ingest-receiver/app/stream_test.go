package app

import (
	"os"
	"testing"

	m "github.com/Eyevinn/dash-mpd/mpd"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/assert"
)

func TestAddVideoInit(t *testing.T) {
	videoData, err := os.ReadFile("testdata/video/init.cmfv")
	assert.NoError(t, err)
	fs := NewFullStream("testpic", 60)
	strm := stream{
		asset:     "testpic",
		name:      "video",
		ext:       "cmfv",
		mediaType: "video",
		assetDir:  "testdir",
	}
	err = fs.AddInitData(strm, videoData)
	assert.NoError(t, err)
	assert.Equal(t, m.DateTime("1970-01-01T00:00:00Z"), fs.MPD.AvailabilityStartTime)
	p := fs.MPD.Periods[0]
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
