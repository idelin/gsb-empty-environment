package seqwin

import (
	"bytes"
	"errors"
	"testing"
)

func mustFrame(t *testing.T, typ byte, payload []byte) []byte {
	t.Helper()
	b, err := EncodeFrame(typ, 0, payload, 1024)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	return b
}

func TestChunkedFeedByteByByte(t *testing.T) {
	wire := append(mustFrame(t, 1, []byte("hello")), mustFrame(t, 2, []byte("world"))...)
	p := NewParser(1024)
	var got []Frame
	for i := 0; i < len(wire); i++ {
		frames, err := p.Feed(wire[i : i+1 : i+1])
		if err != nil {
			t.Fatalf("byte %d: unexpected error %v", i, err)
		}
		got = append(got, frames...)
	}
	if len(got) != 2 {
		t.Fatalf("decoded %d frames, want 2", len(got))
	}
	if got[0].Type != 1 || string(got[0].Payload) != "hello" {
		t.Errorf("frame 0 = %+v", got[0])
	}
	if got[1].Type != 2 || string(got[1].Payload) != "world" {
		t.Errorf("frame 1 = %+v", got[1])
	}
}

func TestRandomChunkBoundaries(t *testing.T) {
	wire := append(mustFrame(t, 1, []byte("aaa")), mustFrame(t, 2, []byte("bbbb"))...)
	for _, cut := range []int{1, 3, 5, 7, 9, len(wire) - 1} {
		p := NewParser(1024)
		f1, err := p.Feed(wire[:cut])
		if err != nil {
			t.Fatalf("cut %d first feed: %v", cut, err)
		}
		f2, err := p.Feed(wire[cut:])
		if err != nil {
			t.Fatalf("cut %d second feed: %v", cut, err)
		}
		if n := len(f1) + len(f2); n != 2 {
			t.Errorf("cut %d: decoded %d frames, want 2", cut, n)
		}
	}
}

func TestIncompleteVsCorrupt(t *testing.T) {
	wire := mustFrame(t, 1, []byte("payload"))
	p := NewParser(1024)
	// A strict prefix must be reported as incomplete, never corrupt.
	for n := 1; n < len(wire); n++ {
		_, _, err := p.parseOne(wire[:n])
		if !errors.Is(err, ErrIncomplete) {
			t.Fatalf("prefix %d: err = %v, want ErrIncomplete", n, err)
		}
		if IsCorrupt(err) {
			t.Fatalf("prefix %d: misclassified as corrupt", n)
		}
	}
	// A flipped payload byte must be corrupt, never incomplete.
	bad := bytes.Clone(wire)
	bad[HeaderLen] ^= 0xFF
	_, err := p.Feed(bad)
	if !IsCorrupt(err) {
		t.Fatalf("corrupted frame: err = %v, want CorruptError", err)
	}
	if errors.Is(err, ErrIncomplete) {
		t.Fatal("corrupted frame misclassified as incomplete")
	}
}

func TestBadMagicIsCorrupt(t *testing.T) {
	p := NewParser(1024)
	_, err := p.Feed([]byte{0x00, 0x00, 1, 0, 0, 0, 0, 0})
	if !IsCorrupt(err) {
		t.Fatalf("err = %v, want CorruptError", err)
	}
}

func TestQuotaCheckedBeforeAllocation(t *testing.T) {
	// Declare a 65535-byte payload against a 16-byte quota. Only the
	// header is fed; the parser must reject on the quota instead of
	// waiting for (or allocating) 64 KiB.
	p := NewParser(16)
	hdr := []byte{magic[0], magic[1], 1, 0, 0xFF, 0xFF}
	_, err := p.Feed(hdr)
	if !IsCorrupt(err) {
		t.Fatalf("err = %v, want CorruptError for over-quota length", err)
	}
	// Same header within quota must instead report incomplete.
	p2 := NewParser(0xFFFF)
	if _, err := p2.Feed(hdr); err != nil {
		t.Fatalf("within-quota header: err = %v, want nil (waiting for more)", err)
	}
}

func TestEncodeFrameHonorsQuota(t *testing.T) {
	if _, err := EncodeFrame(1, 0, make([]byte, 100), 16); err == nil {
		t.Fatal("EncodeFrame with over-quota payload succeeded, want error")
	}
}

func TestEmptyPayloadRoundTrip(t *testing.T) {
	p := NewParser(0) // quota 0: only empty payloads allowed
	frames, err := p.Feed(mustFrame(t, 9, nil))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(frames) != 1 || frames[0].Type != 9 || len(frames[0].Payload) != 0 {
		t.Errorf("frames = %+v", frames)
	}
}
