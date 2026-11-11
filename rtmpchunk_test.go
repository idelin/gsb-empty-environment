package rtmpchunk

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

// splitReader emulates arbitrary TCP segmentation: every Read returns at most
// size bytes. Varying size enumerates every chunk-boundary split point.
type splitReader struct {
	data []byte
	pos  int
	size int
}

func (s *splitReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	n := s.size
	if n > len(p) {
		n = len(p)
	}
	if n > len(s.data)-s.pos {
		n = len(s.data) - s.pos
	}
	copy(p, s.data[s.pos:s.pos+n])
	s.pos += n
	return n, nil
}

func buildMixedStream(t *testing.T) ([]byte, []encInput) {
	t.Helper()
	enc := newChunkEncoder(128)
	inputs := []encInput{
		// Type 0 (first message), multi-chunk -> Type 3 continuation.
		{csid: 2, typeID: 9, streamID: 1, ts: 100, payload: payload(300, 1)},
		{csid: 4, typeID: 8, streamID: 7, ts: 140, payload: payload(260, 10)},
		// Type 1 on csid 2: length changes (300 -> 50), delta differs.
		{csid: 2, typeID: 9, streamID: 1, ts: 200, payload: payload(50, 40)},
		// Type 2: same length (50) and type, only delta changes.
		{csid: 2, typeID: 9, streamID: 1, ts: 260, payload: payload(50, 70)},
		// Type 3 new message: identical length/type and same delta (60).
		{csid: 2, typeID: 9, streamID: 1, ts: 320, payload: payload(50, 90)},
		// Type 1 again with a large multi-chunk payload.
		{csid: 2, typeID: 9, streamID: 1, ts: 400, payload: payload(330, 110)},
		{csid: 100, typeID: 20, streamID: 3, ts: 500, payload: payload(200, 5)},
		{csid: 500, typeID: 21, streamID: 4, ts: 550, payload: payload(256, 6)},
	}
	var b bytes.Buffer
	for _, in := range inputs {
		b.Write(joinChunks(enc.Encode(in)))
	}
	return b.Bytes(), inputs
}

func decodeAll(t *testing.T, r io.Reader) []*Message {
	t.Helper()
	d := NewDemuxer(r)
	var got []*Message
	for {
		msg, err := d.ReadMessage()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		got = append(got, msg)
	}
}

// All four header formats, all three basic-header length forms, including
// multi-chunk messages with Type 3 continuation headers.
func TestFourHeaderFormatsAndReassembly(t *testing.T) {
	stream, inputs := buildMixedStream(t)
	got := decodeAll(t, bytes.NewReader(stream))
	if len(got) != len(inputs) {
		t.Fatalf("messages = %d, want %d", len(got), len(inputs))
	}
	for i, want := range inputs {
		assertMessage(t, got[i], want)
	}
}

// Multiple chunk streams interleaved at chunk granularity reassemble in
// per-message completion order.
func TestInterleavedChunkStreams(t *testing.T) {
	enc := newChunkEncoder(64)
	// The peer must negotiate the smaller chunk size first; the demuxer
	// otherwise keeps the protocol default of 128.
	scs := encInput{csid: 2, typeID: typeSetChunkSize, streamID: 0, ts: 0,
		payload: binary.BigEndian.AppendUint32(nil, 64)}
	a := encInput{csid: 2, typeID: 9, streamID: 1, ts: 1000, payload: payload(200, 1)}
	b := encInput{csid: 3, typeID: 8, streamID: 2, ts: 2000, payload: payload(150, 2)}
	c := encInput{csid: 320, typeID: 18, streamID: 9, ts: 3000, payload: payload(90, 3)}

	var stream bytes.Buffer
	// SCS is emitted while the encoder still uses the default 128 size.
	stream.Write(joinChunks(enc.Encode(scs)))

	ca, cb, cc := enc.Encode(a), enc.Encode(b), enc.Encode(c)
	maxLen := len(ca)
	if len(cb) > maxLen {
		maxLen = len(cb)
	}
	if len(cc) > maxLen {
		maxLen = len(cc)
	}
	for i := 0; i < maxLen; i++ {
		if i < len(ca) {
			stream.Write(ca[i])
		}
		if i < len(cb) {
			stream.Write(cb[i])
		}
		if i < len(cc) {
			stream.Write(cc[i])
		}
	}

	// c finishes in 2 chunks, b in 3, a in 4.
	d := NewDemuxer(bytes.NewReader(stream.Bytes()))
	if msg, err := d.ReadMessage(); err != nil {
		t.Fatalf("scs: %v", err)
	} else {
		assertMessage(t, msg, scs)
	}
	if d.ChunkSize() != 64 {
		t.Fatalf("chunk size = %d, want 64", d.ChunkSize())
	}
	for _, want := range []encInput{c, b, a} {
		msg, err := d.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		assertMessage(t, msg, want)
	}
}

// Set Chunk Size mid-stream: the control message uses the old size; later
// chunks use the negotiated size.
func TestSetChunkSizeMidStream(t *testing.T) {
	enc := newChunkEncoder(128)
	before := encInput{csid: 4, typeID: 9, streamID: 1, ts: 100, payload: payload(300, 1)}
	setSize := uint32(64)
	scs := encInput{csid: 2, typeID: typeSetChunkSize, streamID: 0, ts: 0,
		payload: binary.BigEndian.AppendUint32(nil, setSize)}
	after := encInput{csid: 4, typeID: 9, streamID: 1, ts: 200, payload: payload(200, 2)}

	var stream bytes.Buffer
	beforeChunks := enc.Encode(before)
	if len(beforeChunks) != 3 { // 128 + 128 + 44
		t.Fatalf("before message produced %d chunks, want 3", len(beforeChunks))
	}
	stream.Write(joinChunks(beforeChunks, enc.Encode(scs)))

	d := NewDemuxer(&stream)
	got, err := d.ReadMessage()
	if err != nil {
		t.Fatalf("before: %v", err)
	}
	assertMessage(t, got, before)
	if d.ChunkSize() != 128 {
		t.Fatalf("chunk size before SCS = %d", d.ChunkSize())
	}

	got, err = d.ReadMessage()
	if err != nil {
		t.Fatalf("scs: %v", err)
	}
	assertMessage(t, got, scs)
	if d.ChunkSize() != setSize {
		t.Fatalf("chunk size after SCS = %d, want %d", d.ChunkSize(), setSize)
	}

	enc.chunkSize = setSize
	afterChunks := enc.Encode(after)
	if len(afterChunks) != 4 { // 64*3 + 8
		t.Fatalf("after message produced %d chunks, want 4", len(afterChunks))
	}
	stream.Write(joinChunks(afterChunks))

	got, err = d.ReadMessage()
	if err != nil {
		t.Fatalf("after: %v", err)
	}
	assertMessage(t, got, after)
}
