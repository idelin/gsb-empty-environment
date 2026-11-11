package warcwriter

import (
	"bufio"
	"compress/gzip"
	"io"
)

// gzipMembers iterates over the independent gzip members of stream. Each
// returned reader covers exactly one member and leaves stream positioned at
// the start of the next member once drained (Multistream(false)). The reader
// must be fully consumed (or discarded) before the next call.
func gzipMembers(stream io.Reader) func() (io.ReadCloser, error) {
	var source io.Reader = stream
	if _, ok := stream.(io.ByteReader); !ok {
		// bufio.Reader implements ReadByte and only buffers one byte at a
		// time at the boundary, so the stream position stays well-defined.
		source = bufio.NewReader(stream)
	}
	reader := new(gzip.Reader)
	started := false
	return func() (io.ReadCloser, error) {
		if !started {
			gz, err := gzip.NewReader(source)
			if err != nil {
				return nil, err
			}
			gz.Multistream(false)
			reader = gz
			started = true
			return gz, nil
		}
		if err := reader.Reset(source); err != nil {
			return nil, err
		}
		reader.Multistream(false)
		return reader, nil
	}
}

// newSingleStreamReader opens exactly one gzip member at the stream's
// current position and stops at its end.
func newSingleStreamReader(stream io.Reader) (*gzip.Reader, error) {
	source := stream
	if _, ok := stream.(io.ByteReader); !ok {
		source = bufio.NewReader(stream)
	}
	gz, err := gzip.NewReader(source)
	if err != nil {
		return nil, err
	}
	gz.Multistream(false)
	return gz, nil
}
