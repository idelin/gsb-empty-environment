package rtmpchunk

import (
	"bytes"
	"encoding/binary"
)

// chunkEncoder serializes messages into RTMP chunks with the same rules the
// demuxer must understand. Per-csid state drives Type 1/2/3 header selection
// and extended-timestamp repetition.
type chunkEncoder struct {
	chunkSize uint32
	state     map[uint32]*encState
}

type encState struct {
	lastAbsTS     uint32
	lastDelta     uint32
	hadExtTS      bool
	reapplyDelta  bool
	lastMsgLength uint32
	lastMsgType   uint8
	lastStreamID  uint32
	used          bool
}

func newChunkEncoder(chunkSize uint32) *chunkEncoder {
	return &chunkEncoder{chunkSize: chunkSize, state: make(map[uint32]*encState)}
}

type encInput struct {
	csid     uint32
	typeID   uint8
	streamID uint32
	ts       uint32
	payload  []byte
}

// Encode emits one message as one or more chunks.
func (e *chunkEncoder) Encode(in encInput) [][]byte {
	st := e.state[in.csid]
	if st == nil {
		st = &encState{}
		e.state[in.csid] = st
	}

	msgLength := uint32(len(in.payload))
	delta := in.ts - st.lastAbsTS

	// Header format follows field changes, per the RTMP spec:
	// Type 0: first message or new message stream id;
	// Type 1: message length/type changed;
	// Type 2: only the timestamp delta changed;
	// Type 3: every field (including delta) matches the previous message.
	var fmt int
	switch {
	case !st.used || in.streamID != st.lastStreamID:
		fmt = 0
	case msgLength != st.lastMsgLength || in.typeID != st.lastMsgType:
		fmt = 1
	case delta != st.lastDelta:
		fmt = 2
	default:
		fmt = 3
	}

	// Publish this message's timing state up front so continuation Type 3
	// chunks repeat the correct extended timestamp.
	st.lastAbsTS = in.ts
	st.lastDelta = delta

	var chunks [][]byte
	offset := uint32(0)
	first := true
	for {
		n := msgLength - offset
		if n > e.chunkSize {
			n = e.chunkSize
		}

		var h []byte
		if first {
			h = e.encodeHeader(in, st, fmt, msgLength, delta)
		} else {
			h = e.encodeType3(in.csid, st)
		}

		chunk := make([]byte, 0, len(h)+int(n))
		chunk = append(chunk, h...)
		chunk = append(chunk, in.payload[offset:offset+n]...)
		chunks = append(chunks, chunk)

		offset += n
		first = false
		if offset >= msgLength {
			break
		}
	}

	st.lastMsgLength = msgLength
	st.lastMsgType = in.typeID
	st.lastStreamID = in.streamID
	st.used = true
	return chunks
}

func (e *chunkEncoder) encodeHeader(in encInput, st *encState, fmt int,
	msgLength, delta uint32) []byte {

	var b bytes.Buffer
	b.Write(encodeBasicHeader(in.csid, byte(fmt)))

	switch fmt {
	case 0:
		ext := in.ts >= extTimestampMarker
		st.hadExtTS = ext
		st.reapplyDelta = false
		write24(&b, markerOrValue(in.ts, ext))
		write24(&b, msgLength)
		b.WriteByte(in.typeID)
		var sid [4]byte
		binary.LittleEndian.PutUint32(sid[:], in.streamID)
		b.Write(sid[:])
		if ext {
			write32(&b, in.ts)
		}

	case 1:
		ext := delta >= extTimestampMarker
		st.hadExtTS = ext
		st.reapplyDelta = true
		write24(&b, markerOrValue(delta, ext))
		write24(&b, msgLength)
		b.WriteByte(in.typeID)
		if ext {
			write32(&b, delta)
		}

	case 2:
		ext := delta >= extTimestampMarker
		st.hadExtTS = ext
		st.reapplyDelta = true
		write24(&b, markerOrValue(delta, ext))
		if ext {
			write32(&b, delta)
		}

	case 3:
		// New Type 3-header message: repeat the extended timestamp field.
		if st.hadExtTS {
			if st.reapplyDelta {
				write32(&b, st.lastDelta)
			} else {
				write32(&b, st.lastAbsTS)
			}
		}
	}
	return b.Bytes()
}

func (e *chunkEncoder) encodeType3(csid uint32, st *encState) []byte {
	var b bytes.Buffer
	b.Write(encodeBasicHeader(csid, 3))
	if st.hadExtTS {
		if st.reapplyDelta {
			write32(&b, st.lastDelta)
		} else {
			write32(&b, st.lastAbsTS)
		}
	}
	return b.Bytes()
}

func encodeBasicHeader(csid uint32, fmt byte) []byte {
	switch {
	case csid >= 2 && csid <= 63:
		return []byte{fmt<<6 | byte(csid)}
	case csid >= 64 && csid <= 319:
		return []byte{fmt << 6, byte(csid - 64)}
	case csid >= 320 && csid <= 65599:
		v := csid - 64
		return []byte{fmt<<6 | 1, byte(v), byte(v >> 8)}
	default:
		panic("csid out of encodable range")
	}
}

func markerOrValue(v uint32, ext bool) uint32 {
	if ext {
		return extTimestampMarker
	}
	return v
}

func write24(b *bytes.Buffer, v uint32) {
	b.Write([]byte{byte(v >> 16), byte(v >> 8), byte(v)})
}

func write32(b *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	b.Write(buf[:])
}

func joinChunks(chunkSets ...[][]byte) []byte {
	var b bytes.Buffer
	for _, set := range chunkSets {
		for _, c := range set {
			b.Write(c)
		}
	}
	return b.Bytes()
}

func payload(size int, start byte) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = start + byte(i)
	}
	return p
}

func assertMessage(t testingT, got *Message, want encInput) {
	t.Helper()
	if got.ChunkStreamID != want.csid {
		t.Fatalf("csid = %d, want %d", got.ChunkStreamID, want.csid)
	}
	if got.TypeID != want.typeID {
		t.Fatalf("type = %d, want %d", got.TypeID, want.typeID)
	}
	if got.MessageStreamID != want.streamID {
		t.Fatalf("stream id = %d, want %d", got.MessageStreamID, want.streamID)
	}
	if got.Timestamp != want.ts {
		t.Fatalf("timestamp = %d, want %d", got.Timestamp, want.ts)
	}
	if !bytes.Equal(got.Payload, want.payload) {
		t.Fatalf("payload mismatch (got %d bytes, want %d)",
			len(got.Payload), len(want.payload))
	}
}
