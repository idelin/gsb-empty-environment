package reassemble

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
	"time"
)

func TestParserArbitraryChunking(t *testing.T) {
	var stream []byte
	stream = AppendFrame(stream, 1, 20, 0, []byte("hello"))
	stream = AppendFrame(stream, 1, 20, 5, []byte(" worl"))
	stream = AppendFrame(stream, 2, 3, 0, []byte("abc"))

	// Reference: one big write.
	p := NewParser(1 << 20)
	ref, err := p.Write(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != 3 {
		t.Fatalf("got %d frames", len(ref))
	}

	// Every split point, and 1-byte trickle, must give identical frames.
	feed := func(chunks [][]byte) []Frame {
		p := NewParser(1 << 20)
		var all []Frame
		for _, c := range chunks {
			frames, err := p.Write(c)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, frames...)
		}
		if p.Pending() != 0 {
			t.Fatalf("leftover pending bytes: %d", p.Pending())
		}
		return all
	}
	for split := 0; split <= len(stream); split++ {
		got := feed([][]byte{stream[:split], stream[split:]})
		if len(got) != 3 {
			t.Fatalf("split %d: %d frames", split, len(got))
		}
		for i := range ref {
			if got[i].ID != ref[i].ID || got[i].TotalLen != ref[i].TotalLen ||
				got[i].Offset != ref[i].Offset || !bytes.Equal(got[i].Payload, ref[i].Payload) {
				t.Fatalf("split %d: frame %d differs", split, i)
			}
		}
	}
	var trickle [][]byte
	for _, b := range stream {
		trickle = append(trickle, []byte{b})
	}
	if got := feed(trickle); len(got) != 3 {
		t.Fatalf("trickle: %d frames", len(got))
	}
}

func TestParserNeedMoreDataIsNotAnError(t *testing.T) {
	stream := AppendFrame(nil, 1, 10, 0, []byte("hello"))
	p := NewParser(1 << 20)
	// Partial header: silently buffered.
	frames, err := p.Write(stream[:HeaderSize-1])
	if err != nil || len(frames) != 0 {
		t.Fatalf("partial header: frames=%d err=%v", len(frames), err)
	}
	// Full header, partial payload: still buffered.
	frames, err = p.Write(stream[HeaderSize-1 : HeaderSize+2])
	if err != nil || len(frames) != 0 {
		t.Fatalf("partial payload: frames=%d err=%v", len(frames), err)
	}
	if p.Pending() != len(stream)-3 {
		t.Fatalf("pending=%d", p.Pending())
	}
	frames, err = p.Write(stream[HeaderSize+2:])
	if err != nil || len(frames) != 1 || string(frames[0].Payload) != "hello" {
		t.Fatalf("completion: frames=%d err=%v", len(frames), err)
	}
}

func TestParserBadData(t *testing.T) {
	good := AppendFrame(nil, 1, 5, 0, []byte("hello"))

	corrupt := func(mutate func([]byte)) []byte {
		b := append([]byte(nil), good...)
		mutate(b)
		return b
	}
	cases := map[string][]byte{
		"bad magic":    corrupt(func(b []byte) { b[0] = 'X' }),
		"bad version":  corrupt(func(b []byte) { b[2] = 9 }),
		"bad checksum": corrupt(func(b []byte) { b[27] ^= 0xff }),
		"bad payload":  corrupt(func(b []byte) { b[HeaderSize] ^= 0xff }), // header CRC now stale? no: payload not covered
		"past total": corrupt(func(b []byte) {
			binary.BigEndian.PutUint32(b[15:19], 4)
			binary.BigEndian.PutUint32(b[27:31], crc32.ChecksumIEEE(b[:27]))
		}),
		"payload limit": corrupt(func(b []byte) {
			binary.BigEndian.PutUint32(b[11:15], 100)
			binary.BigEndian.PutUint32(b[19:23], 50)
			binary.BigEndian.PutUint32(b[27:31], crc32.ChecksumIEEE(b[:27]))
		}),
	}
	for name, stream := range cases {
		p := NewParser(10)
		_, err := p.Write(stream)
		if err == nil || !IsBadData(err) {
			t.Errorf("%s: got %v, want BadDataError", name, err)
		}
	}
}

func TestParserFramesBeforeErrorReturned(t *testing.T) {
	stream := AppendFrame(nil, 1, 3, 0, []byte("abc"))
	stream = append(stream, make([]byte, HeaderSize)...) // garbage trailing bytes
	p := NewParser(10)
	frames, err := p.Write(stream)
	if len(frames) != 1 {
		t.Fatalf("valid frame before garbage lost: %d frames", len(frames))
	}
	if err == nil || !IsBadData(err) {
		t.Fatalf("got %v, want BadDataError", err)
	}
}

func TestHandlerEndToEnd(t *testing.T) {
	c := &clock{t: time.Now()}
	h := NewHandler(testConfig(), c.now)
	msg := []byte("fragmented streams reassembled")
	var stream []byte
	// Deliberately out of order, with a consistent overlap.
	stream = AppendFrame(stream, 42, uint32(len(msg)), 10, msg[10:20])
	stream = AppendFrame(stream, 42, uint32(len(msg)), 0, msg[0:15])
	stream = AppendFrame(stream, 42, uint32(len(msg)), 15, msg[15:])
	// Feed in awkward 7-byte chunks.
	var done [][]byte
	for i := 0; i < len(stream); i += 7 {
		end := i + 7
		if end > len(stream) {
			end = len(stream)
		}
		d, err := h.Write(stream[i:end])
		if err != nil {
			t.Fatal(err)
		}
		done = append(done, d...)
	}
	if len(done) != 1 || !bytes.Equal(done[0], msg) {
		t.Fatalf("done=%d %q", len(done), done)
	}
}
