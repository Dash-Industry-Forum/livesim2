package chunkparser

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunkParser(t *testing.T) {
	cases := []struct {
		path          string
		isInitSegment bool
		nrChunks      int
		dataLength    int
	}{
		{"testdata/video_init.mp4", true, 1, 748},
		{"testdata/audio_init.mp4", true, 1, 765},
		{"testdata/3_chunked.m4s", false, 4, 13786},
	}
	buf := make([]byte, 1024)
	for _, c := range cases {
		data, err := os.ReadFile(c.path)
		require.NoError(t, err)
		r := bytes.NewReader(data)
		chunks := make([]ChunkData, 0)
		cb := func(cd ChunkData) error {
			c := make([]byte, len(cd.Data))
			copy(c, cd.Data)
			chunks = append(chunks, ChunkData{Start: cd.Start, IsInitSegment: cd.IsInitSegment, Data: c})
			return nil
		}
		p := NewMP4ChunkParser(r, buf, cb)
		err = p.Parse()
		require.NoError(t, err)
		require.Equal(t, c.nrChunks, len(chunks))
		require.Equal(t, c.isInitSegment, chunks[0].IsInitSegment)
		totDataLength := 0
		biggestChunk := 0
		for _, c := range chunks {
			if len(c.Data) > biggestChunk {
				biggestChunk = len(c.Data)
			}
			totDataLength += len(c.Data)
		}
		require.Equal(t, c.dataLength, totDataLength)
		buf = p.GetBuffer()
		require.GreaterOrEqual(t, len(buf), biggestChunk)
		combinedData := make([]byte, 0, totDataLength)
		for _, c := range chunks {
			combinedData = append(combinedData, c.Data...)
		}
		require.Equal(t, data, combinedData)
	}
}
