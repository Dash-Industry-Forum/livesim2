package chunkparser

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrChunkTooLarge is returned by Parse when a chunk would be larger than the maximum set
// with SetMaxChunkSize.
var ErrChunkTooLarge = errors.New("chunk too large")

// minGrowth is the smallest number of bytes by which the buffer grows.
const minGrowth = 32 * 1024

// MP4ChunkParser is a parser for fragmented mp4 content.
// The callback is called on end of segment of end of fragment (chunk) if fragments are detected.
type MP4ChunkParser struct {
	r          io.Reader
	callBack   func(cd ChunkData) error
	buf        []byte
	contentEnd int
	eof        bool // r has returned io.EOF
	maxChunk   int  // maximum chunk size, 0 for no limit
}

// NewMP4ChunkParser creates a new MP4ChunkParser with an initial buffer.
// The buffer is grown as needed and can be retrieved for reuse with GetBuffer().
func NewMP4ChunkParser(r io.Reader, buf []byte, callback func(cd ChunkData) error) *MP4ChunkParser {
	return &MP4ChunkParser{
		r:        r,
		callBack: callback,
		buf:      buf,
	}
}

// SetMaxChunkSize limits the size of a chunk: the boxes up to and including an mdat, or all
// boxes of content without mdat, such as an init segment. Parse returns ErrChunkTooLarge as
// soon as a box header gives a larger size, before the data of the box is read.
// The default 0 means no limit.
func (p *MP4ChunkParser) SetMaxChunkSize(maxSize int) {
	p.maxChunk = maxSize
}

// Parse parses mp4 content from a reader and sends fragments (ending with mdat) via callback.
// Init segments are detected by the presence of a moov box.
// The parsing continues until EOF is reached, or an error occurs.
// The content must end with a complete box. Since the box sizes give the number of
// bytes to expect, content that ends inside a box, such as a segment that was cut
// short, gives io.ErrUnexpectedEOF. Box sizes 0 (to the end of the file) and 1
// (64-bit size) are not supported and give an error.
func (p *MP4ChunkParser) Parse() error {
	boxStart := 0 // start of the next box in buf
	cd := ChunkData{
		Start:         0,
		IsInitSegment: false,
		Data:          nil,
	}
	for {
		err := p.readUntil(boxStart + 8)
		if err == io.EOF {
			if p.contentEnd > boxStart {
				return fmt.Errorf("box header: %w", io.ErrUnexpectedEOF)
			}
			// EOF at the end of a box. Send any boxes after the last mdat.
			if p.contentEnd > 0 {
				cd.Data = p.buf[:p.contentEnd]
				return p.callBack(cd)
			}
			return nil
		}
		if err != nil {
			return err
		}
		size := int(binary.BigEndian.Uint32(p.buf[boxStart : boxStart+4]))
		boxType := string(p.buf[boxStart+4 : boxStart+8])
		if size < 8 {
			return fmt.Errorf("%s box size %d not supported", boxType, size)
		}
		boxEnd := boxStart + size
		err = p.readUntil(boxEnd)
		if err == io.EOF {
			return fmt.Errorf("%s box with %d of %d bytes: %w", boxType, p.contentEnd-boxStart, size, io.ErrUnexpectedEOF)
		}
		if err != nil {
			return err
		}
		switch boxType {
		case "moov":
			cd.IsInitSegment = true
		case "mdat":
			// mdat is complete
			cd.Data = p.buf[:boxEnd]
			err := p.callBack(cd)
			if err != nil {
				return err
			}
			// Reset for next chunk
			cd.Start += uint32(boxEnd)
			cd.Data = nil
			copy(p.buf, p.buf[boxEnd:p.contentEnd])
			p.contentEnd -= boxEnd
			boxEnd = 0
		}
		boxStart = boxEnd
	}
}

// GetBuffer returns the buffer used by the parser.
// The buffer is resized as needed during parsing.
// The intention is to reuse the buffer for subsequent parsing
// of the same stream of segments to avoid unnecessary allocations.
func (p *MP4ChunkParser) GetBuffer() []byte {
	return p.buf
}

// readUntil reads from the reader until the contentEnd is reached.
// It returns io.EOF if the reader ends before that.
// The buffer grows as the data arrives, at most doubling, rather than to the contentEnd
// that a box header gives, so a box size that no data backs cannot allocate much memory.
func (p *MP4ChunkParser) readUntil(contentEnd int) error {
	if p.maxChunk > 0 && contentEnd > p.maxChunk {
		return fmt.Errorf("%w: %d bytes, more than %d", ErrChunkTooLarge, contentEnd, p.maxChunk)
	}
	for p.contentEnd < contentEnd {
		if p.eof {
			return io.EOF
		}
		if p.contentEnd == len(p.buf) {
			newBuf := make([]byte, min(contentEnd, max(2*len(p.buf), len(p.buf)+minGrowth)))
			copy(newBuf, p.buf[:p.contentEnd])
			p.buf = newBuf
		}
		n, err := p.r.Read(p.buf[p.contentEnd:min(contentEnd, len(p.buf))])
		p.contentEnd += n
		if err == io.EOF {
			p.eof = true
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ChunkData provides a raw fmp4 chunk or a full segment.
// Start provides an offset in bytes in the segment.
type ChunkData struct {
	Start         uint32
	IsInitSegment bool
	Data          []byte
}
