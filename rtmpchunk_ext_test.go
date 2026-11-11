package rtmpchunk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// Byte-level check of the extended-timestamp repetition rule: a Type 0 chunk
// with an extended timestamp is followed by Type 3 continuation chunks, each
// carrying the repeated 4-byte extended timestamp.
func TestExtendedTimestampRawLayout(t *testing.T) {
	enc := newChunkEncoder(128)
	in := encInput{csid: 2, typeID: 9, streamID: 1, ts: 0x10000000,
		payload: payload(300, 1)}
	raw := joinChunks(enc.Encode(in))

	// First chunk: fmt0 basic header (1) + 11-byte message header with the
	// 24-bit timestamp field set to 0xFFFFFF, then the 4-byte ext timestamp.
	if raw[0] != 0x02 {
		t.Fatalf("first basic header = %#x, want 0x02", raw[0])
	}
	if got := readUint24(raw[1:4]); got != extTimestampMarker {
		t.Fatalf("timestamp field = %#x, want marker", got)
	}
	if got := binary.BigEndian.Uint32(raw[12:16]); got != 0x10000000 {
		t.Fatalf("ext timestamp = %#x", got)
	}
	// First chunk is 1 + 11 + 4 + 128 = 144 bytes.
	second := raw[144:]
	if second[0] != 0xC2 { // fmt 3, csid 2
		t.Fatalf("second basic header = %#x, want 0xc2", second[0])
	}
	if got := binary.BigEndian.Uint32(second[1:5]); got != 0x10000000 {
		t.Fatalf("repeated ext timestamp = %#x", got)
	}

	got := decodeAll(t, bytes.NewReader(raw))
	if len(got) != 1 {
		t.Fatalf("messages = %d, want 1", len(got))
	}
	assertMessage(t, got[0], in)
}

// Extended timestamps across Type 1/2/3 headers, including a new Type 3
// message re-applying an extended delta exactly once.
func TestExtendedTimestampDeltaAndReapply(t *testing.T) {
	enc := newChunkEncoder(128)
	inputs := []encInput{
		// Type 0 with an extended absolute timestamp.
		{csid: 2, typeID: 9, streamID: 1, ts: 0x10000000, payload: payload(50, 1)},
		// Type 1: length changes; extended delta 0x20000000.
		{csid: 2, typeID: 9, streamID: 1, ts: 0x30000000, payload: payload(60, 2)},
		// Type 2: same length/type, another extended delta 0x20000000.
		{csid: 2, typeID: 9, streamID: 1, ts: 0x50000000, payload: payload(60, 3)},
		// Type 3 new message: identical length/type and same extended
		// delta, so the delta is re-applied once (no double counting).
		{csid: 2, typeID: 9, streamID: 1, ts: 0x70000000, payload: payload(60, 4)},
		// One more Type 3 new message: same field pattern, same delta
		// re-applied again.
		{csid: 2, typeID: 9, streamID: 1, ts: 0x90000000, payload: payload(60, 5)},
	}
	var b bytes.Buffer
	for _, in := range inputs {
		b.Write(joinChunks(enc.Encode(in)))
	}
	got := decodeAll(t, bytes.NewReader(b.Bytes()))
	for i, want := range inputs {
		assertMessage(t, got[i], want)
	}
}

// Timestamp delta accumulation wraps at 32 bits.
func TestTimestampWraparound(t *testing.T) {
	enc := newChunkEncoder(128)
	inputs := []encInput{
		{csid: 2, typeID: 9, streamID: 1, ts: 0xFFFFFFF0, payload: payload(10, 1)},
		{csid: 2, typeID: 9, streamID: 1, ts: 0x00000010, payload: payload(10, 2)},
		{csid: 2, typeID: 9, streamID: 1, ts: 0x00000060, payload: payload(10, 3)},
	}
	var b bytes.Buffer
	for _, in := range inputs {
		b.Write(joinChunks(enc.Encode(in)))
	}
	got := decodeAll(t, bytes.NewReader(b.Bytes()))
	for i, want := range inputs {
		assertMessage(t, got[i], want)
	}

	// Exact 24-bit-marker delta boundary on a Type 1 header.
	enc2 := newChunkEncoder(128)
	edge := []encInput{
		{csid: 5, typeID: 9, streamID: 1, ts: 100, payload: payload(10, 1)},
		{csid: 5, typeID: 9, streamID: 1, ts: 100 + extTimestampMarker, payload: payload(10, 2)},
	}
	var b2 bytes.Buffer
	for _, in := range edge {
		b2.Write(joinChunks(enc2.Encode(in)))
	}
	got2 := decodeAll(t, bytes.NewReader(b2.Bytes()))
	for i, want := range edge {
		assertMessage(t, got2[i], want)
	}
}

// A Type 3 new message following a Type 0 header reuses the absolute
// timestamp (no delta is added), including when the timestamp is extended.
func TestType3AfterType0ReusesAbsoluteTimestamp(t *testing.T) {
	enc := newChunkEncoder(128)
	inputs := []encInput{
		// Type 0 with extended absolute timestamp.
		{csid: 7, typeID: 9, streamID: 1, ts: 0x01020304, payload: payload(40, 1)},
		// Type 1 to change the length with a small delta.
		{csid: 7, typeID: 9, streamID: 1, ts: 0x01020384, payload: payload(55, 2)},
		// A new csid: its Type 0 carries an extended timestamp, then a
		// Type 3 new message on that csid reuses the absolute value.
		{csid: 8, typeID: 9, streamID: 2, ts: 0x0A0B0C0D, payload: payload(30, 3)},
		{csid: 8, typeID: 9, streamID: 2, ts: 0x0A0B0C0D, payload: payload(30, 4)},
	}
	var b bytes.Buffer
	for _, in := range inputs {
		b.Write(joinChunks(enc.Encode(in)))
	}
	got := decodeAll(t, bytes.NewReader(b.Bytes()))
	for i, want := range inputs {
		assertMessage(t, got[i], want)
	}
}

// Enumerate every TCP split position (1 byte per read up to the whole stream
// per read) and require identical reassembly each time.
func TestAllTCPSplitPositions(t *testing.T) {
	stream, inputs := buildMixedStream(t)
	for size := 1; size <= len(stream); size++ {
		r := &splitReader{data: stream, size: size}
		got := decodeAll(t, r)
		if len(got) != len(inputs) {
			t.Fatalf("size %d: messages = %d, want %d",
				size, len(got), len(inputs))
		}
		for i, want := range inputs {
			assertMessage(t, got[i], want)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	// A non-Type-0 header on a never-seen csid is illegal.
	d := NewDemuxer(bytes.NewReader([]byte{0xC2}))
	if _, err := d.ReadMessage(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}

	// A truncated stream surfaces io.ErrUnexpectedEOF.
	enc := newChunkEncoder(128)
	full := joinChunks(enc.Encode(encInput{
		csid: 2, typeID: 9, streamID: 1, ts: 10, payload: payload(200, 1),
	}))
	d = NewDemuxer(bytes.NewReader(full[:len(full)-5]))
	if _, err := d.ReadMessage(); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}
