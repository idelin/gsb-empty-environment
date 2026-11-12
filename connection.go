package netsec

import "io"

// Connection demultiplexes a raw HTTP/2 frame stream onto per-stream
// converters. It enforces the connection-level rules that a single converter
// cannot see on its own: HEADERS/CONTINUATION sequences must be contiguous and
// belong to one stream (RFC 7540 sections 6.2 and 6.10).
type Connection struct {
	reader          *FrameReader
	streams         map[uint32]*Converter
	maxDynamicTable uint32

	continuationStream uint32
	inContinuation     bool
	closed             bool
}

// NewConnection wraps frame reader r and creates converters with the given
// HPACK dynamic table size limit.
func NewConnection(r io.Reader, maxDynamicTable uint32) *Connection {
	return &Connection{
		reader:          NewFrameReader(r),
		streams:         make(map[uint32]*Converter),
		maxDynamicTable: maxDynamicTable,
	}
}

// NextFrame reads one frame and dispatches it. It returns the converter for the
// frame's stream (nil for connection-level frames) together with that
// converter's handling error. A framing error from the underlying reader is
// returned directly and means the connection must be closed.
func (conn *Connection) NextFrame() (*Converter, error) {
	if conn.closed {
		return nil, io.EOF
	}
	f, err := conn.reader.ReadFrame()
	if err != nil {
		conn.closed = true
		return nil, err
	}

	if conn.inContinuation {
		if f.Type != FrameContinuation {
			return nil, frameError(ErrCodeProtocol,
				"frame type %d interleaves a HEADERS/CONTINUATION sequence", f.Type)
		}
		if f.Stream != conn.continuationStream {
			return nil, frameError(ErrCodeProtocol,
				"CONTINUATION on stream %d while stream %d block is open",
				f.Stream, conn.continuationStream)
		}
		if f.Flags.EndHeaders() {
			conn.inContinuation = false
		}
	} else if f.Type == FrameHeaders {
		if !f.Flags.EndHeaders() {
			conn.inContinuation = true
			conn.continuationStream = f.Stream
		}
	} else if f.Type == FrameContinuation {
		return nil, frameError(ErrCodeProtocol, "CONTINUATION without HEADERS")
	}

	if f.Stream == 0 {
		return nil, nil
	}
	if conn.inContinuation {
		// Only CONTINUATION frames may appear while a block is open; this is a
		// defensive check in addition to the type switch above.
		return nil, frameError(ErrCodeProtocol,
			"stream %d frame interrupts an open header block", f.Stream)
	}
	conv := conn.streams[f.Stream]
	if conv == nil {
		if f.Type != FrameHeaders {
			return nil, frameError(ErrCodeProtocol,
				"frame type %d on idle stream %d", f.Type, f.Stream)
		}
		conv = NewConverter(f.Stream, conn.maxDynamicTable)
		conn.streams[f.Stream] = conv
	}
	return conv, conv.HandleFrame(f)
}

// Stream returns the converter registered for streamID, if any.
func (conn *Connection) Stream(streamID uint32) *Converter {
	return conn.streams[streamID]
}

// Close marks the connection drained; further NextFrame calls return io.EOF.
func (conn *Connection) Close() error {
	conn.closed = true
	return nil
}
