// Package rtmpchunk demultiplexes RTMP chunk streams from an RTMP connection.
//
// A Demuxer reads the raw byte stream of an RTMP connection (typically a
// net.Conn) and reassembles RTMP messages, transparently handling the four
// chunk header formats, interleaved chunk streams, mid-stream Set Chunk Size
// control messages and extended timestamps, including the extended-timestamp
// repetition rule for Type 3 chunks. Absolute timestamps are accumulated from
// Type 0 absolute timestamps and Type 1/2 deltas in 32-bit unsigned
// arithmetic, so integer overflow implements the required wraparound.
package rtmpchunk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// DefaultChunkSize is the RTMP chunk size assumed before any Set Chunk Size
// protocol control message is received.
const DefaultChunkSize = 128

const (
	extTimestampMarker = 0x00FFFFFF
	typeSetChunkSize   = 1
)

// ErrProtocol is returned, possibly wrapped, when the byte stream is not a
// valid RTMP chunk stream.
var ErrProtocol = errors.New("rtmpchunk: protocol error")

// Message is a fully reassembled RTMP message.
type Message struct {
	ChunkStreamID   uint32
	TypeID          uint8
	MessageStreamID uint32

	// Timestamp is the absolute message timestamp in milliseconds, accumulated
	// from Type 0 absolute timestamps and Type 1/2 deltas with 32-bit
	// wraparound.
	Timestamp uint32

	// Payload is owned by the caller; the demuxer reuses no backing array
	// after returning the message.
	Payload []byte
}

// Demuxer reassembles RTMP messages from a connection's byte stream. It is
// safe for sequential use by a single goroutine, matching one connection.
type Demuxer struct {
	r         io.Reader
	chunkSize uint32
	streams   map[uint32]*streamState

	headerBuf [11]byte
	u32Buf    [4]byte
}

type streamState struct {
	initialized bool

	// hadExtTS reports whether the most recent timestamp-bearing header
	// carried an extended timestamp. Type 3 chunks repeat the extended
	// timestamp field while this stays true.
	hadExtTS bool

	// reapplyDelta is true when a Type 3 chunk starting a new message must
	// add lastDelta to the absolute timestamp. It is set by Type 1/2 headers
	// and stays set across consecutive Type 3 messages; a Type 0 header
	// clears it so the absolute timestamp is simply reused.
	reapplyDelta bool

	absTS     uint32
	lastDelta uint32

	msgLength uint32
	msgType   uint8
	streamID  uint32

	payload  []byte
	received uint32
}

// NewDemuxer returns a Demuxer reading from r. The initial chunk size is the
// RTMP default of 128 bytes; a Set Chunk Size message changes it for all
// subsequent chunks.
func NewDemuxer(r io.Reader) *Demuxer {
	return &Demuxer{
		r:         r,
		chunkSize: DefaultChunkSize,
		streams:   make(map[uint32]*streamState),
	}
}

// ChunkSize returns the chunk size currently in effect.
func (d *Demuxer) ChunkSize() uint32 { return d.chunkSize }

// ReadMessage reads chunks until one RTMP message has been fully reassembled.
// It returns io.EOF when the underlying reader is exhausted between messages;
// truncated streams produce io.ErrUnexpectedEOF or an ErrProtocol wrapper.
func (d *Demuxer) ReadMessage() (*Message, error) {
	for {
		msg, err := d.readChunk()
		if err != nil {
			return nil, err
		}
		if msg != nil {
			return msg, nil
		}
	}
}

func (d *Demuxer) readChunk() (*Message, error) {
	csid, headerType, err := d.readBasicHeader()
	if err != nil {
		return nil, err
	}

	st := d.streams[csid]
	if st == nil {
		st = &streamState{}
		d.streams[csid] = st
	}
	if !st.initialized && headerType != 0 {
		return nil, fmt.Errorf("%w: chunk stream %d starts with type %d header",
			ErrProtocol, csid, headerType)
	}

	switch headerType {
	case 0:
		if err := d.readFull(d.headerBuf[:11]); err != nil {
			return nil, err
		}
		h := d.headerBuf[:11]
		tsField := readUint24(h[0:3])
		st.msgLength = readUint24(h[3:6])
		st.msgType = h[6]
		st.streamID = binary.LittleEndian.Uint32(h[7:11])

		if tsField == extTimestampMarker {
			st.hadExtTS = true
			if st.absTS, err = d.readExtTimestamp(); err != nil {
				return nil, err
			}
		} else {
			st.hadExtTS = false
			st.absTS = tsField
		}
		st.lastDelta = 0
		st.reapplyDelta = false
		st.beginMessage()

	case 1:
		if err := d.readFull(d.headerBuf[:7]); err != nil {
			return nil, err
		}
		h := d.headerBuf[:7]
		st.msgLength = readUint24(h[3:6])
		st.msgType = h[6]
		if err := d.applyDelta(st, readUint24(h[0:3])); err != nil {
			return nil, err
		}
		st.reapplyDelta = true
		st.beginMessage()

	case 2:
		if err := d.readFull(d.headerBuf[:3]); err != nil {
			return nil, err
		}
		if err := d.applyDelta(st, readUint24(d.headerBuf[:3])); err != nil {
			return nil, err
		}
		st.reapplyDelta = true
		st.beginMessage()

	case 3:
		// The extended timestamp is repeated on every Type 3 chunk while
		// the preceding timestamp-bearing header used one.
		if st.hadExtTS {
			if _, err := d.readExtTimestamp(); err != nil {
				return nil, err
			}
		}
		if st.received == 0 {
			// A Type 3 header starting a brand-new message reuses the
			// previous message's header. After a Type 1/2 header the last
			// delta is re-applied for each new Type 3 message; after a
			// Type 0 header the absolute timestamp is simply unchanged.
			if st.reapplyDelta {
				st.absTS += st.lastDelta
			}
			st.beginMessage()
		}
		// Otherwise this is a continuation chunk: the timestamp is unchanged.

	default:
		return nil, fmt.Errorf("%w: unknown chunk header type %d", ErrProtocol, headerType)
	}

	st.initialized = true

	// Consume at most one chunk's worth of message payload.
	remaining := st.msgLength - st.received
	n := d.chunkSize
	if remaining < n {
		n = remaining
	}
	if n > 0 {
		if err := d.readFull(st.payload[st.received : st.received+n]); err != nil {
			return nil, err
		}
		st.received += n
	}

	if st.received != st.msgLength {
		return nil, nil
	}

	msg := &Message{
		ChunkStreamID:   csid,
		TypeID:          st.msgType,
		MessageStreamID: st.streamID,
		Timestamp:       st.absTS,
		Payload:         st.payload,
	}
	st.payload = nil
	st.received = 0

	// Set Chunk Size (protocol control message type 1): the message itself
	// is chunked with the old size; the new size applies to later chunks.
	if msg.TypeID == typeSetChunkSize && len(msg.Payload) == 4 {
		if size := binary.BigEndian.Uint32(msg.Payload) & 0x7FFFFFFF; size != 0 {
			d.chunkSize = size
		}
	}
	return msg, nil
}

// applyDelta consumes an optional extended timestamp and adds the delta to the
// running absolute timestamp. Unsigned addition wraps at 2^32, implementing
// RTMP timestamp rollover.
func (d *Demuxer) applyDelta(st *streamState, field uint32) error {
	st.hadExtTS = field == extTimestampMarker
	if st.hadExtTS {
		delta, err := d.readExtTimestamp()
		if err != nil {
			return err
		}
		st.lastDelta = delta
	} else {
		st.lastDelta = field
	}
	st.absTS += st.lastDelta
	return nil
}

func (d *Demuxer) readBasicHeader() (csid uint32, fmt byte, err error) {
	first, err := d.readByte()
	if err != nil {
		return 0, 0, err
	}
	fmt = first >> 6
	switch low := first & 0x3F; low {
	case 0: // 2-byte form: csid 64..319
		b, err := d.readByte()
		if err != nil {
			return 0, 0, err
		}
		return 64 + uint32(b), fmt, nil
	case 1: // 3-byte form: csid 64..65599
		var h [2]byte
		if err := d.readFull(h[:]); err != nil {
			return 0, 0, err
		}
		return 64 + uint32(h[0]) + uint32(h[1])<<8, fmt, nil
	default: // 1-byte form: csid 2..63
		return uint32(low), fmt, nil
	}
}

func (d *Demuxer) readExtTimestamp() (uint32, error) {
	if err := d.readFull(d.u32Buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(d.u32Buf[:]), nil
}

func (d *Demuxer) readByte() (byte, error) {
	var b [1]byte
	err := d.readFull(b[:])
	return b[0], err
}

func (d *Demuxer) readFull(p []byte) error {
	_, err := io.ReadFull(d.r, p)
	return err
}

func (st *streamState) beginMessage() {
	st.payload = make([]byte, st.msgLength)
	st.received = 0
}

func readUint24(b []byte) uint32 {
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}
