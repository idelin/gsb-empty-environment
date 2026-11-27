package h2guard

import (
	"bytes"
	"encoding/binary"
	"time"
)

// encodeInteger writes value in the RFC 7541 section 5.1 representation with
// the given prefix bits, reusing the already-set prefix byte prefixByte.
func encodeInteger(prefixByte byte, prefixBits uint, value int) []byte {
	mask := byte((1 << prefixBits) - 1)
	if value < int(mask) {
		return []byte{prefixByte | byte(value)}
	}
	out := []byte{prefixByte | mask}
	remaining := value - int(mask)
	for remaining >= 128 {
		out = append(out, byte(remaining%128+128))
		remaining /= 128
	}
	out = append(out, byte(remaining))
	return out
}

// encodeString writes an H=0 string literal (RFC 7541 5.2).
func encodeString(s string) []byte {
	out := encodeInteger(0, 7, len(s))
	out = append(out, []byte(s)...)
	return out
}

// hpackBlock encodes header fields with a deliberately simple policy: static
// table hits use indexed representation (6.1), everything else is a literal
// without indexing (6.2.2) carrying an inline H=0 name and value. It produces
// blocks the decoder must accept while covering both representations.
func hpackBlock(fields []HeaderField) []byte {
	var block []byte
	for _, f := range fields {
		if idx, ok := staticIndex(f.Name, f.Value); ok {
			block = append(block, encodeInteger(0x80, 7, idx)...)
			continue
		}
		if idx, ok := staticNameIndex(f.Name); ok {
			block = append(block, encodeInteger(0x00, 4, idx)...)
		} else {
			block = append(block, 0x00)
			block = append(block, encodeString(f.Name)...)
		}
		block = append(block, encodeString(f.Value)...)
	}
	return block
}

func staticIndex(name, value string) (int, bool) {
	for i, e := range hpackStaticTable {
		if e.name == name && e.value == value {
			return i + 1, true
		}
	}
	return 0, false
}

func staticNameIndex(name string) (int, bool) {
	for i, e := range hpackStaticTable {
		if e.name == name {
			return i + 1, true
		}
	}
	return 0, false
}

func requestHeaders(extra ...HeaderField) []HeaderField {
	fields := []HeaderField{
		{Name: ":method", Value: "POST"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/api"},
		{Name: ":authority", Value: "example.com"},
	}
	return append(fields, extra...)
}

func headersFrame(streamID uint32, endHeaders, endStream bool, block []byte) *Frame {
	var flags byte
	if endHeaders {
		flags |= FlagEndHeaders
	}
	if endStream {
		flags |= FlagEndStream
	}
	return &Frame{Type: FrameHeaders, Flags: flags, StreamID: streamID, Payload: block}
}

func continuationFrame(streamID uint32, endHeaders bool, fragment []byte) *Frame {
	var flags byte
	if endHeaders {
		flags |= FlagEndHeaders
	}
	return &Frame{Type: FrameContinuation, Flags: flags, StreamID: streamID, Payload: fragment}
}

func dataFrame(streamID uint32, endStream bool, payload []byte) *Frame {
	var flags byte
	if endStream {
		flags |= FlagEndStream
	}
	return &Frame{Type: FrameData, Flags: flags, StreamID: streamID, Payload: payload}
}

func rstFrame(streamID uint32, code uint32) *Frame {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, code)
	return &Frame{Type: FrameRSTStream, StreamID: streamID, Payload: payload}
}

// splitBlock fragments an encoded block across one HEADERS plus CONTINUATIONs.
func splitBlock(streamID uint32, block []byte, chunk int, endStream bool) []*Frame {
	var frames []*Frame
	for off := 0; off < len(block); off += chunk {
		end := off + chunk
		if end > len(block) {
			end = len(block)
		}
		if off == 0 {
			frames = append(frames, headersFrame(streamID, end == len(block), endStream && end == len(block), block[off:end]))
		} else {
			frames = append(frames, continuationFrame(streamID, end == len(block), block[off:end]))
		}
	}
	if len(block) == 0 {
		frames = append(frames, headersFrame(streamID, true, endStream, nil))
	}
	return frames
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func testConfig(clock *fakeClock) *Config {
	cfg := DefaultConfig()
	cfg.Now = clock.now
	return &cfg
}

func mustHandle(t testTB, p *Parser, f *Frame) *Event {
	t.Helper()
	ev, err := p.Handle(f)
	if err != nil {
		t.Fatalf("unexpected error on frame type=0x%x stream=%d: %v", f.Type, f.StreamID, err)
	}
	return ev
}

type testTB interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
}

func parseOne(t testTB, data []byte) *Frame {
	t.Helper()
	f, err := ReadFrame(bytes.NewReader(data), DefaultMaxFrameSize)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	return f
}

func u32(b []byte) uint32 {
	return binary.BigEndian.Uint32(b)
}
