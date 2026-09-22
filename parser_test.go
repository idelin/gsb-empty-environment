package correlator

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func sampleMessages() []Message {
	return []Message{
		{Kind: KindRequest, Epoch: 1, Seq: 1, Payload: []byte("get /a")},
		{Kind: KindRequest, Epoch: 1, Seq: 2, Payload: nil},
		{Kind: KindResponse, Epoch: 1, Seq: 1, Payload: []byte("200 OK")},
		{Kind: KindResponse, Epoch: 2, Seq: 1, Payload: bytes.Repeat([]byte("x"), 300)},
	}
}

func encodeAll(msgs []Message) []byte {
	var stream []byte
	for _, m := range msgs {
		stream = append(stream, m.Marshal()...)
	}
	return stream
}

func drain(t *testing.T, p *Parser) []Message {
	t.Helper()
	var got []Message
	for {
		m, err := p.Next()
		if errors.Is(err, ErrIncomplete) {
			return got
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, m)
	}
}

func TestParserArbitraryChunking(t *testing.T) {
	msgs := sampleMessages()
	stream := encodeAll(msgs)

	// Whole stream at once.
	whole := drain(t, func() *Parser { p := NewParser(1024, 1<<20); p.Feed(stream); return p }())

	// Byte-by-byte.
	p := NewParser(1024, 1<<20)
	var bytewise []Message
	for i := 0; i < len(stream); i++ {
		if err := p.Feed(stream[i : i+1]); err != nil {
			t.Fatalf("Feed byte %d: %v", i, err)
		}
		bytewise = append(bytewise, drain(t, p)...)
	}

	// Random chunk sizes, deterministic seed.
	p = NewParser(1024, 1<<20)
	rng := rand.New(rand.NewSource(42))
	var random []Message
	for off := 0; off < len(stream); {
		n := 1 + rng.Intn(17)
		if off+n > len(stream) {
			n = len(stream) - off
		}
		if err := p.Feed(stream[off : off+n]); err != nil {
			t.Fatalf("Feed chunk: %v", err)
		}
		random = append(random, drain(t, p)...)
		off += n
	}

	for name, got := range map[string][]Message{"whole": whole, "bytewise": bytewise, "random": random} {
		if len(got) != len(msgs) {
			t.Fatalf("%s: got %d messages, want %d", name, len(got), len(msgs))
		}
		for i, m := range got {
			want := msgs[i]
			if m.Kind != want.Kind || m.Epoch != want.Epoch || m.Seq != want.Seq ||
				!bytes.Equal(m.Payload, want.Payload) {
				t.Errorf("%s msg %d: got %+v, want %+v", name, i, m, want)
			}
		}
	}
}

func TestParserIncompleteVsInvalid(t *testing.T) {
	m := Message{Kind: KindRequest, Epoch: 7, Seq: 9, Payload: []byte("hello")}
	frame := m.Marshal()

	p := NewParser(1024, 1<<20)
	// Feed everything except the last byte: must be "need more", never "bad".
	if err := p.Feed(frame[:len(frame)-1]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Next(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("truncated frame: got %v, want ErrIncomplete", err)
	}
	if err := p.Feed(frame[len(frame)-1:]); err != nil {
		t.Fatal(err)
	}
	got, err := p.Next()
	if err != nil {
		t.Fatalf("completed frame: %v", err)
	}
	if got.Epoch != 7 || got.Seq != 9 || string(got.Payload) != "hello" {
		t.Fatalf("decoded %+v", got)
	}
}

func TestParserBadMagic(t *testing.T) {
	frame := Message{Kind: KindRequest, Epoch: 1, Seq: 1}.Marshal()
	frame[0] = 0x00
	p := NewParser(1024, 1<<20)
	p.Feed(frame)
	if _, err := p.Next(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad magic: got %v, want ErrInvalid", err)
	}
}

func TestParserBadKind(t *testing.T) {
	frame := Message{Kind: KindRequest, Epoch: 1, Seq: 1}.Marshal()
	frame[2] = 99
	p := NewParser(1024, 1<<20)
	p.Feed(frame)
	if _, err := p.Next(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad kind: got %v, want ErrInvalid", err)
	}
}

func TestParserLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	// Header alone declares a payload far over quota. The parser must
	// reject it immediately — without waiting for (or allocating) the
	// claimed payload bytes.
	frame := Message{Kind: KindRequest, Epoch: 1, Seq: 1, Payload: make([]byte, 4096)}.Marshal()
	headerOnly := frame[:headerSize]

	p := NewParser(128, 1<<20) // quota: 128 bytes
	if err := p.Feed(headerOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Next(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized declared length: got %v, want ErrInvalid", err)
	}

	// Same declared length but within quota: must be ErrIncomplete, i.e.
	// the parser waits for the payload instead of failing.
	p = NewParser(8192, 1<<20)
	if err := p.Feed(headerOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Next(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("in-quota declared length: got %v, want ErrIncomplete", err)
	}
}

func TestParserBufferBound(t *testing.T) {
	p := NewParser(8, headerSize+8)
	// One in-quota frame exactly fills the buffer.
	frame := Message{Kind: KindRequest, Epoch: 1, Seq: 1, Payload: make([]byte, 8)}.Marshal()
	if err := p.Feed(frame); err != nil {
		t.Fatalf("full frame within bound: %v", err)
	}
	// One more byte before draining exceeds the bound.
	if err := p.Feed([]byte{0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over bound: got %v, want ErrInvalid", err)
	}
}
