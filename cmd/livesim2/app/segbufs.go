// Copyright 2026, DASH-Industry-Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	"io"
	"io/fs"
	"sync"
)

// segBufPool holds buffers for the VoD segment data read for a request, so that
// a request does not allocate a new buffer for every segment that it reads.
var segBufPool = sync.Pool{New: func() any { return new([]byte) }}

// segBufs are the pooled buffers that a request has read VoD segments into.
// A decoded segment, and the chunks made from it, refer to the sample data in
// these buffers without copying it, so they may only be released once the
// response has been written.
type segBufs []*[]byte

// read reads the file name from vodFS into a pooled buffer and returns its data.
func (b *segBufs) read(vodFS fs.FS, name string) ([]byte, error) {
	f, err := vodFS.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := int(fi.Size())
	buf := segBufPool.Get().(*[]byte)
	if cap(*buf) < size {
		*buf = make([]byte, size)
	}
	data := (*buf)[:size]
	if _, err := io.ReadFull(f, data); err != nil {
		segBufPool.Put(buf)
		return nil, err
	}
	*b = append(*b, buf)
	return data, nil
}

// release returns the buffers to the pool. Nothing read into them may be used after that.
func (b *segBufs) release() {
	for _, buf := range *b {
		segBufPool.Put(buf)
	}
	*b = nil
}
