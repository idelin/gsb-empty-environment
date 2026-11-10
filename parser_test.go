package wsprobe

import (
	"encoding/binary"
	"strings"
	"testing"
)

var testMask = [4]byte{0x37, 0xfa, 0x21, 0x3d}

// frame builds a masked client frame as wire bytes.
func frame(fin bool, opcode byte, payload []byte) []byte {
	b := []byte{0}
	if fin {
		b[0] |= 0x80
	}
	b[0] |= opcode
	maskBit := byte(0x80)
	switch n := len(payload); {
	case n < 126:
		b = append(b, maskBit|byte(n))
	case n <= 0xffff:
		b = append(b, maskBit|126, byte(n>>8), byte(n))
	default:
		b = append(b, maskBit|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		b = append(b, ext[:]...)
	}
	b = append(b, testMask[:]...)
	for i, c := range payload {
		b = append(b, c^testMask[i%4])
	}
	return b
}

func textFrame(payload string) []byte { return frame(true, OpText, []byte(payload)) }

type collector struct {
	frames []Frame
	hits   []Hit
}

func (c *collector) events() Events {
	return Events{
		OnFrame: func(f Frame) { c.frames = append(c.frames, f) },
		OnHit:   func(h Hit) { c.hits = append(c.hits, h) },
	}
}

// feed pushes stream into p in chunks of size n (n <= 0 feeds it whole).
func feed(p *Parser, stream []byte, n int) {
	if n <= 0 {
		p.Feed(stream)
		return
	}
	for len(stream) > 0 {
		m := min(n, len(stream))
		p.Feed(stream[:m])
		stream = stream[m:]
	}
}

func TestSingleFrameHit(t *testing.T) {
	var c collector
	p := NewParser("world", c.events())
	feed(p, textFrame("hello world"), 0)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v, want ok", r.Outcome)
	}
	if len(c.hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(c.hits))
	}
	h := c.hits[0]
	if h.StartFrame != 0 || h.StartOffset != 6 || h.EndFrame != 0 || h.EndOffset != 11 {
		t.Fatalf("hit = %v", h)
	}
}

func TestNeedleAcrossFragments(t *testing.T) {
	var c collector
	p := NewParser("hello world", c.events())
	stream := append([]byte{},
		frame(false, OpText, []byte("hel"))...,
	)
	stream = append(stream, frame(false, OpContinuation, []byte("lo wor"))...)
	stream = append(stream, frame(true, OpContinuation, []byte("ld!"))...)
	feed(p, stream, 1) // byte-by-byte delivery
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v, want ok", r.Outcome)
	}
	if len(c.hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(c.hits))
	}
	h := c.hits[0]
	if h.StartFrame != 0 || h.StartOffset != 0 || h.EndFrame != 2 || h.EndOffset != 2 {
		t.Fatalf("hit = %v, want frame 0:0 .. frame 2:2", h)
	}
}

func TestInterleavedControlFrames(t *testing.T) {
	var c collector
	p := NewParser("secret", c.events())
	stream := append([]byte{}, frame(false, OpText, []byte("sec"))...)
	stream = append(stream, frame(true, OpPing, []byte("ping!"))...)
	stream = append(stream, frame(true, OpPong, nil)...)
	stream = append(stream, frame(true, OpContinuation, []byte("ret"))...)
	feed(p, stream, 3)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v, want ok", r.Outcome)
	}
	if len(c.frames) != 4 {
		t.Fatalf("got %d frames, want 4", len(c.frames))
	}
	if len(c.hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(c.hits))
	}
	h := c.hits[0]
	if h.StartFrame != 0 || h.StartOffset != 0 || h.EndFrame != 3 || h.EndOffset != 3 {
		t.Fatalf("hit = %v, want frame 0:0 .. frame 3:3", h)
	}
}

func TestUTF8RuneSplitAcrossFragments(t *testing.T) {
	var c collector
	p := NewParser("好", c.events())
	// "你" = E4 BD A0, "好" = E5 A5 BD; split both runes across fragments.
	stream := append([]byte{}, frame(false, OpText, []byte{0xe4, 0xbd})...)
	stream = append(stream, frame(false, OpContinuation, []byte{0xa0, 0xe5})...)
	stream = append(stream, frame(true, OpContinuation, []byte{0xa5, 0xbd})...)
	feed(p, stream, 2)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v (%v), want ok", r.Outcome, r.Err)
	}
	if len(c.hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(c.hits))
	}
	h := c.hits[0]
	if h.StartFrame != 1 || h.StartOffset != 1 || h.EndFrame != 2 || h.EndOffset != 2 {
		t.Fatalf("hit = %v, want frame 1:1 .. frame 2:2", h)
	}
}

func TestInvalidUTF8AcrossFragments(t *testing.T) {
	var c collector
	p := NewParser("", c.events())
	stream := append([]byte{}, frame(false, OpText, []byte{0xe4, 0xbd})...)
	stream = append(stream, frame(true, OpContinuation, []byte{0xff, 0xa0})...)
	feed(p, stream, 0)
	r := p.Finish()
	if r.Outcome != OutcomeProtocolError {
		t.Fatalf("outcome = %v, want protocol-error", r.Outcome)
	}
	if !strings.Contains(r.Err.Error(), "invalid UTF-8") {
		t.Fatalf("err = %v", r.Err)
	}
}

func TestIncompleteUTF8AtMessageEnd(t *testing.T) {
	var c collector
	p := NewParser("", c.events())
	feed(p, frame(true, OpText, []byte("abc\xe4\xbd")), 0)
	r := p.Finish()
	if r.Outcome != OutcomeProtocolError {
		t.Fatalf("outcome = %v, want protocol-error", r.Outcome)
	}
	if !strings.Contains(r.Err.Error(), "incomplete UTF-8") {
		t.Fatalf("err = %v", r.Err)
	}
}

func TestUnmaskedFrameRejected(t *testing.T) {
	var c collector
	p := NewParser("", c.events())
	raw := []byte{0x81, 0x02, 'h', 'i'} // text, no mask bit
	feed(p, raw, 0)
	r := p.Finish()
	if r.Outcome != OutcomeProtocolError || !strings.Contains(r.Err.Error(), "unmasked") {
		t.Fatalf("result = %+v", r)
	}
}

func TestTruncatedMidFrame(t *testing.T) {
	var c collector
	p := NewParser("", c.events())
	full := textFrame("hello")
	feed(p, full[:len(full)-3], 0)
	r := p.Finish()
	if r.Outcome != OutcomeTruncated {
		t.Fatalf("outcome = %v, want truncated", r.Outcome)
	}
}

func TestTruncatedUnterminatedMessage(t *testing.T) {
	var c collector
	p := NewParser("", c.events())
	feed(p, frame(false, OpText, []byte("no end")), 0)
	r := p.Finish()
	if r.Outcome != OutcomeTruncated {
		t.Fatalf("outcome = %v, want truncated", r.Outcome)
	}
}

func TestProtocolViolations(t *testing.T) {
	cases := []struct {
		name   string
		stream []byte
		want   string
	}{
		{"continuation without message", frame(true, OpContinuation, []byte("x")), "without an open message"},
		{"new data during fragment", append(frame(false, OpText, []byte("a")), textFrame("b")...), "before final continuation"},
		{"fragmented control", frame(false, OpPing, []byte("x")), "fragmented control"},
		{"oversized control", frame(true, OpPing, make([]byte, 126)), "exceeds 125"},
		{"reserved opcode", frame(true, 0x3, []byte("x")), "reserved data opcode"},
		{"reserved control opcode", frame(true, 0xB, nil), "reserved control opcode"},
		{"close with 1-byte payload", frame(true, OpClose, []byte{0x03}), "1-byte payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c collector
			p := NewParser("", c.events())
			feed(p, tc.stream, 0)
			r := p.Finish()
			if r.Outcome != OutcomeProtocolError {
				t.Fatalf("outcome = %v, want protocol-error", r.Outcome)
			}
			if !strings.Contains(r.Err.Error(), tc.want) {
				t.Fatalf("err = %q, want substring %q", r.Err, tc.want)
			}
		})
	}
}

func TestExtendedLengthPayload(t *testing.T) {
	var c collector
	needle := "needle"
	payload := strings.Repeat("a", 300) + needle + strings.Repeat("b", 200)
	p := NewParser(needle, c.events())
	feed(p, textFrame(payload), 7)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v, want ok", r.Outcome)
	}
	if len(c.hits) != 1 || c.hits[0].StartOffset != 300 {
		t.Fatalf("hits = %v", c.hits)
	}
}

func TestOverlappingHits(t *testing.T) {
	var c collector
	p := NewParser("aa", c.events())
	feed(p, textFrame("aaaa"), 0)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v", r.Outcome)
	}
	if len(c.hits) != 3 {
		t.Fatalf("got %d hits, want 3 overlapping", len(c.hits))
	}
}

func TestArbitraryChunkingConsistency(t *testing.T) {
	stream := append([]byte{}, frame(false, OpText, []byte("prefix sec"))...)
	stream = append(stream, frame(true, OpPing, []byte("p"))...)
	stream = append(stream, frame(true, OpContinuation, []byte("ret suffix"))...)
	stream = append(stream, textFrame("second secret message")...)
	stream = append(stream, frame(true, OpClose, []byte{0x03, 0xe8})...)

	var want []Hit
	p := NewParser("secret", Events{OnHit: func(h Hit) { want = append(want, h) }})
	feed(p, stream, 0)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v", r.Outcome)
	}

	for _, size := range []int{1, 2, 3, 5, 8, 13, 64} {
		var got []Hit
		q := NewParser("secret", Events{OnHit: func(h Hit) { got = append(got, h) }})
		feed(q, stream, size)
		if r := q.Finish(); r.Outcome != OutcomeOK {
			t.Fatalf("chunk %d: outcome = %v", size, r.Outcome)
		}
		if len(got) != len(want) {
			t.Fatalf("chunk %d: got %d hits, want %d", size, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chunk %d: hit %d = %+v, want %+v", size, i, got[i], want[i])
			}
		}
	}
	if len(want) != 2 {
		t.Fatalf("got %d hits, want 2", len(want))
	}
}

func TestBinaryMessageSkipsChecks(t *testing.T) {
	var c collector
	p := NewParser("x", c.events())
	// Invalid UTF-8 in a binary message must not raise an error.
	feed(p, frame(true, OpBinary, []byte{0xff, 0xfe, 'x'}), 0)
	if r := p.Finish(); r.Outcome != OutcomeOK {
		t.Fatalf("outcome = %v (%v)", r.Outcome, r.Err)
	}
	if len(c.hits) != 0 {
		t.Fatalf("binary message must not be searched, hits = %v", c.hits)
	}
}
