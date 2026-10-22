package chunkparser

import (
	"bytes"
	"io"
	"os"
	"testing"
	"testing/iotest"
	"time"

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

// TestChunkParserIncomplete checks that content that ends inside a box is detected from
// the box sizes, while the complete chunks before it are still passed to the callback.
// Content that ends after a complete chunk cannot be told from a shorter segment.
func TestChunkParserIncomplete(t *testing.T) {
	data, err := os.ReadFile("testdata/3_chunked.m4s")
	require.NoError(t, err)
	// The four chunks end after 3769, 7037, 10625 and 13786 bytes.
	cases := []struct {
		desc     string
		length   int
		nrChunks int
		wantErr  error
	}{
		{desc: "complete", length: 13786, nrChunks: 4},
		{desc: "ends after a chunk", length: 7037, nrChunks: 2},
		{desc: "ends in a box header", length: 7037 + 4, nrChunks: 2, wantErr: io.ErrUnexpectedEOF},
		{desc: "ends in a moof", length: 7037 + 100, nrChunks: 2, wantErr: io.ErrUnexpectedEOF},
		{desc: "ends in the last mdat", length: 13786 - 100, nrChunks: 3, wantErr: io.ErrUnexpectedEOF},
	}
	readers := []struct {
		name      string
		newReader func([]byte) io.Reader
	}{
		{"all at once", func(b []byte) io.Reader { return bytes.NewReader(b) }},
		{"byte by byte", func(b []byte) io.Reader { return iotest.OneByteReader(bytes.NewReader(b)) }},
		{"EOF with data", func(b []byte) io.Reader { return iotest.DataErrReader(bytes.NewReader(b)) }},
	}
	for _, c := range cases {
		for _, rd := range readers {
			t.Run(c.desc+"/"+rd.name, func(t *testing.T) {
				var received []byte
				p := NewMP4ChunkParser(rd.newReader(data[:c.length]), make([]byte, 1024), func(cd ChunkData) error {
					received = append(received, cd.Data...)
					return nil
				})
				err := p.Parse()
				if c.wantErr != nil {
					require.ErrorIs(t, err, c.wantErr)
				} else {
					require.NoError(t, err)
				}
				ends := []int{0, 3769, 7037, 10625, 13786}
				require.Equal(t, data[:ends[c.nrChunks]], received, "data of the complete chunks")
			})
		}
	}
}

// TestChunkParserBoxSize checks that box sizes 0 and 1, which the parser does not support,
// give an error instead of making Parse loop forever.
func TestChunkParserBoxSize(t *testing.T) {
	for _, size := range []byte{0, 1} {
		data := []byte{0, 0, 0, size, 'm', 'o', 'o', 'f', 1, 2, 3, 4}
		p := NewMP4ChunkParser(bytes.NewReader(data), make([]byte, 64), func(ChunkData) error { return nil })
		done := make(chan error, 1)
		go func() { done <- p.Parse() }()
		select {
		case err := <-done:
			require.ErrorContains(t, err, "moof box size")
		case <-time.After(5 * time.Second):
			t.Fatalf("Parse did not return for box size %d", size)
		}
	}
}

// countingReader counts the bytes read from r.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += n
	return n, err
}

// TestChunkParserAllocation checks that the buffer grows with the data that arrives, not to
// the size that a box header claims, and that SetMaxChunkSize rejects a box from its header.
func TestChunkParserAllocation(t *testing.T) {
	noop := func(ChunkData) error { return nil }
	// An mdat header that claims 1 GiB, followed by only 100 bytes.
	data := append([]byte{0x40, 0, 0, 0, 'm', 'd', 'a', 't'}, make([]byte, 100)...)
	p := NewMP4ChunkParser(bytes.NewReader(data), make([]byte, 1024), noop)
	require.ErrorIs(t, p.Parse(), io.ErrUnexpectedEOF)
	require.LessOrEqual(t, len(p.GetBuffer()), 1024+minGrowth, "buffer size")

	cr := &countingReader{r: bytes.NewReader(data)}
	p = NewMP4ChunkParser(cr, make([]byte, 1024), noop)
	p.SetMaxChunkSize(1 << 20)
	require.ErrorIs(t, p.Parse(), ErrChunkTooLarge)
	require.Equal(t, 8, cr.n, "only the box header is read")

	// The largest chunk of 3_chunked.m4s, including its styp, has 3769 bytes.
	segment, err := os.ReadFile("testdata/3_chunked.m4s")
	require.NoError(t, err)
	p = NewMP4ChunkParser(bytes.NewReader(segment), make([]byte, 1024), noop)
	p.SetMaxChunkSize(3769)
	require.NoError(t, p.Parse())
	p = NewMP4ChunkParser(bytes.NewReader(segment), make([]byte, 1024), noop)
	p.SetMaxChunkSize(3768)
	require.ErrorIs(t, p.Parse(), ErrChunkTooLarge)
}
