package app

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const expectedOutput = `<?xml version="1.0" encoding="UTF-8"?>
<tt xmlns:ttp="http://www.w3.org/ns/ttml#parameter" xmlns="http://www.w3.org/ns/ttml"
    xmlns:tts="http://www.w3.org/ns/ttml#styling" xmlns:ttm="http://www.w3.org/ns/ttml#metadata"
    xmlns:ebuttm="urn:ebu:metadata" xmlns:ebutts="urn:ebu:style"
    xml:lang="en" xml:space="default"
    ttp:timeBase="media"
    ttp:cellResolution="32 15">
  <head>
    <metadata>
      <ttm:title>DASH-IF Live Simulator 2</ttm:title>
      <ebuttm:documentMetadata>
        <ebuttm:conformsToStandard>urn:ebu:distribution:2014-01</ebuttm:conformsToStandard>
        <ebuttm:authoredFrameRate>30</ebuttm:authoredFrameRate>
      </ebuttm:documentMetadata>
    </metadata>
    <styling>
      <style xml:id="s0" tts:fontStyle="normal" tts:fontFamily="sansSerif" tts:fontSize="100%" tts:lineHeight="normal"
      tts:color="#FFFFFF" tts:wrapOption="noWrap" tts:textAlign="center"/>
      <style xml:id="s1" tts:color="#00FF00" tts:backgroundColor="#000000" ebutts:linePadding="0.5c"/>
      <style xml:id="s2" tts:color="#ff0000" tts:backgroundColor="#000000" ebutts:linePadding="0.5c"/>
    </styling>
    <layout>
      <region xml:id="r0" tts:origin="15% 80%" tts:extent="70% 20%" tts:overflow="visible" tts:displayAlign="before"/>
      <region xml:id="r1" tts:origin="15% 20%" tts:extent="70% 20%" tts:overflow="visible" tts:displayAlign="before"/>
    </layout>
  </head>
  <body style="s0">
<div region="r0">
<p xml:id="id0" begin="0" end="1"><span style="s1">utc0</span></p>
<p xml:id="id1" begin="1" end="2"><span style="s1">utc1</span></p>
</div>
  </body>
</tt>
`

func TestTemplates(t *testing.T) {
	templateRoot := os.DirFS("templates")
	textTemplates, err := compileTextTemplates(templateRoot, "")
	require.NoError(t, err)
	stppData := StppTimeData{
		Lang: "en",
		Cues: []StppTimeCue{
			{Id: "id0", Begin: "0", End: "1", Msg: "utc0"},
			{Id: "id1", Begin: "1", End: "2", Msg: "utc1"},
		},
	}
	var buf bytes.Buffer
	err = textTemplates.ExecuteTemplate(&buf, "stpptime.gotxt", stppData)
	require.NoError(t, err)
	require.Equal(t, expectedOutput, buf.String())
}

func TestHTMLTemplates(t *testing.T) {
	templateRoot := os.DirFS("templates")
	textTemplates, err := compileHTMLTemplates(templateRoot, "")
	require.NoError(t, err)
	require.Equal(t, `; defined templates are: "welcome.gohtml"`, textTemplates.DefinedTemplates())
}
