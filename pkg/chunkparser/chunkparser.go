package chunkparser

import (
	"encoding/binary"
	"io"
)

// MP4ChunkParser is a parser for fragmented mp4 content.
// The callback is called on end of segment of end of fragment (chunk) if fragments are detected.
type MP4ChunkParser struct {
	r          io.Reader
	callBack   func(cd ChunkData) error
	buf        []byte
	contentEnd int
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

// Parse parses mp4 content from a reader and sends fragments (ending with mdat) via callback.
// Init segments are detected by the presence of a moov box.
// The parsing always and when eof is reached, or an error occurs.
// If io.EOF is returned at an end of a box, no error is returned.
func (p *MP4ChunkParser) Parse() error {
	// No content-length, so read multiple times until EOF
	currBox := ""
	nextBoxStart := uint32(0)
	mdatEnd := uint32(0)
	cd := ChunkData{
		Start:         0,
		IsInitSegment: false,
		Data:          nil,
	}
	for {
		err := p.readUntil(int(nextBoxStart) + 8)
		if err != nil {
			if err != io.EOF {
				return err
			}
			// EOF
			if p.contentEnd > 0 {
				cd.Data = p.buf[:p.contentEnd]
				err := p.callBack(cd)
				if err != nil {
					return err
				}
			}
			return nil
		}
		size := binary.BigEndian.Uint32(p.buf[nextBoxStart : nextBoxStart+4])
		currBox = string(p.buf[nextBoxStart+4 : nextBoxStart+8])
		nextBoxStart += size
		switch currBox {
		case "moov":
			cd.IsInitSegment = true
		case "mdat":
			mdatEnd = nextBoxStart
		}
		err = p.readUntil(int(nextBoxStart))
		if err != nil && err != io.EOF {
			return err
		}
		if mdatEnd == uint32(p.contentEnd) {
			// mdat is complete
			cd.Data = p.buf[:mdatEnd]
			err := p.callBack(cd)
			if err != nil {
				return err
			}
			// Reset for next chunk
			cd.Start += mdatEnd
			cd.Data = nil
			copy(p.buf, p.buf[mdatEnd:p.contentEnd])
			p.contentEnd -= int(mdatEnd)
			nextBoxStart -= mdatEnd
			mdatEnd = 0
		}
		if err == io.EOF {
			if p.contentEnd > 0 {
				cd.Data = p.buf[:p.contentEnd]
				err := p.callBack(cd)
				if err != nil {
					return err
				}
			}
			return nil
		}
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
// The buffer is resized as needed.
func (p *MP4ChunkParser) readUntil(contentEnd int) error {
	if p.contentEnd >= contentEnd {
		return nil
	}
	for {
		if contentEnd > len(p.buf) {
			// Resize buffer
			newBuf := make([]byte, contentEnd-len(p.buf)+1024)
			p.buf = append(p.buf, newBuf...)
		}
		n, err := p.r.Read(p.buf[p.contentEnd:contentEnd])
		p.contentEnd += n
		if err != nil {
			return err
		}
		if p.contentEnd >= contentEnd {
			return nil
		}
	}
}

// ChunkData provides a raw fmp4 chunk or a full segment.
// Start provides an offset in bytes in the segment.
type ChunkData struct {
	Start         uint32
	IsInitSegment bool
	Data          []byte
}
