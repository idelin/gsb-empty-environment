package chain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func framesRaw(t *testing.T, payloads ...string) ([]byte, []Frame) {
	t.Helper()
	b := NewBuilder()
	var fs []Frame
	for _, pl := range payloads {
		fs = append(fs, b.Add([]byte(pl)))
	}
	return b.Bytes(), fs
}

func TestMissingMiddleFrameIsError(t *testing.T) {
	b := NewBuilder()
	f0 := b.Add([]byte("zero"))
	_ = b.Add([]byte("one"))
	f2 := b.Add([]byte("two"))

	p := NewParser()
	p.Append(f0.Encode())
	mustNext(t, p) // frame0
	p.Append(f2.Encode())
	_, err := p.Next()
	var ce *ChainError
	if !errors.As(err, &ce) || !errors.Is(ce.Kind, ErrSequenceGap) {
		t.Fatalf("err=%v want ErrSequenceGap", err)
	}
	if ce.Got != 2 || ce.Want != 1 {
		t.Fatalf("gap got/want = %d/%d", ce.Got, ce.Want)
	}
	// Parser must not skip the gap.
	if p.Checkpoint().Seq != 1 {
		t.Fatalf("parser advanced past gap to %d", p.Checkpoint().Seq)
	}
}

func TestSwappedAdjacentFramesBreakChain(t *testing.T) {
	b := NewBuilder()
	f0 := b.Add([]byte("zero"))
	f1 := b.Add([]byte("one"))

	// Swap two adjacent, individually-valid frames on the wire.
	raw := append(f1.Encode(), f0.Encode()...)

	p := NewParser()
	p.Append(raw)
	_, err := p.Next()
	var ce *ChainError
	// Frame1 cannot follow genesis: seq is ahead (gap) and its link points
	// at frame0. Either way the swapped, CRC-valid frames break the chain.
	if !errors.As(err, &ce) {
		t.Fatalf("err=%v want a ChainError", err)
	}
	if !errors.Is(ce.Kind, ErrSequenceGap) && !errors.Is(ce.Kind, ErrBrokenLink) {
		t.Fatalf("kind=%v want sequence gap or broken link", ce.Kind)
	}
	if ce.Offset != 0 {
		t.Fatalf("offset=%d want 0", ce.Offset)
	}
	// The lagging frame0 must not be accepted afterwards either.
	p.Append(f0.Encode())
	if _, err := p.Next(); err == nil {
		t.Fatal("lagging frame after break was accepted")
	}
}

func TestValidCRCButWrongPrevDigest(t *testing.T) {
	// Two well-formed frames whose link pointers disagree: CRC only covers
	// each frame, so both CRCs pass but the chain must reject.
	b := NewBuilder()
	f0 := b.Add([]byte("zero"))

	// Hand-make a frame seq1 linking to a bogus digest.
	bad := Frame{Seq: 1, PrevDigest: bytes.Repeat([]byte{0x01}, digestSize), Payload: []byte("one")}
	stream := append(f0.Encode(), bad.Encode()...)

	p := NewParser()
	p.Append(stream)
	mustNext(t, p)
	_, err := p.Next()
	var ce *ChainError
	if !errors.As(err, &ce) || !errors.Is(ce.Kind, ErrBrokenLink) {
		t.Fatalf("err=%v want ErrBrokenLink", err)
	}
}

func TestDuplicateFrameNotCountedAsNew(t *testing.T) {
	b := NewBuilder()
	f0 := b.Add([]byte("zero"))
	b.Add([]byte("one"))

	p := NewParser()
	p.Append(f0.Encode())
	if ev := mustNext(t, p); ev.Kind != EventFrame {
		t.Fatalf("kind=%v want EventFrame", ev.Kind)
	}
	// Resend identical frame0.
	p.Append(f0.Encode())
	ev, err := p.Next()
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if ev.Kind != EventDuplicate {
		t.Fatalf("kind=%v want EventDuplicate", ev.Kind)
	}
	if p.Checkpoint().Seq != 1 {
		t.Fatalf("duplicate advanced seq to %d", p.Checkpoint().Seq)
	}
}

func TestSameSeqDifferentContentConflicts(t *testing.T) {
	b := NewBuilder()
	f0 := b.Add([]byte("zero"))

	// Another frame with seq0 but different payload (its own CRC valid).
	evil := Frame{Seq: 0, PrevDigest: ZeroDigest(), Payload: []byte("EVIL")}
	if bytes.Equal(evil.Encode(), f0.Encode()) {
		t.Fatal("test setup produced identical frames")
	}

	p := NewParser()
	p.Append(f0.Encode())
	mustNext(t, p)
	p.Append(evil.Encode())
	_, err := p.Next()
	var ce *ChainError
	if !errors.As(err, &ce) || !errors.Is(ce.Kind, ErrConflictingDuplicate) {
		t.Fatalf("err=%v want ErrConflictingDuplicate", err)
	}
}

func TestReorderedOldFrame(t *testing.T) {
	b := NewBuilder()
	old := b.Add([]byte("zero"))
	b.Add([]byte("one"))
	b.Add([]byte("two"))

	p := NewParser()
	// Accept 0,1,2 then receive 0 again (more than one behind).
	p.Append(b.Bytes())
	for i := 0; i < 3; i++ {
		mustNext(t, p)
	}
	p.Append(old.Encode())
	_, err := p.Next()
	var ce *ChainError
	if !errors.As(err, &ce) || !errors.Is(ce.Kind, ErrReordered) {
		t.Fatalf("err=%v want ErrReordered", err)
	}
}

func TestCheckpointSuffixAccepted(t *testing.T) {
	prefix := NewBuilder()
	prefix.Add([]byte("a"))
	prefix.Add([]byte("b"))
	cp := prefix.Checkpoint() // last accepted frame is f1 (seq1)

	cont, err := NewBuilderAt(cp)
	if err != nil {
		t.Fatal(err)
	}
	cont.Add([]byte("c"))
	cont.Add([]byte("d"))

	p, err := NewParserAt(cp)
	if err != nil {
		t.Fatal(err)
	}
	p.Append(cont.Bytes())
	ev := mustNext(t, p)
	if ev.Frame.Seq != 2 {
		t.Fatalf("first suffix seq=%d want 2", ev.Frame.Seq)
	}
	ev = mustNext(t, p)
	if ev.Frame.Seq != 3 {
		t.Fatalf("second suffix seq=%d want 3", ev.Frame.Seq)
	}
}

func TestWrongCheckpointRejectsSuffix(t *testing.T) {
	prefix := NewBuilder()
	prefix.Add([]byte("a"))
	goodCP := prefix.Checkpoint()

	cont, err := NewBuilderAt(goodCP)
	if err != nil {
		t.Fatal(err)
	}
	cont.Add([]byte("c"))

	// Wrong but well-formed checkpoint.
	wrong := Checkpoint{Seq: goodCP.Seq, LastDigest: bytes.Repeat([]byte{0x7F}, digestSize)}
	p, err := NewParserAt(wrong)
	if err != nil {
		t.Fatalf("well-formed wrong checkpoint should construct: %v", err)
	}
	p.Append(cont.Bytes())
	if _, err := p.Next(); !errors.Is(err, ErrBrokenLink) {
		t.Fatalf("err=%v want ErrBrokenLink", err)
	}

	// Malformed checkpoint rejected at construction.
	if _, err := NewParserAt(Checkpoint{Seq: 1, LastDigest: []byte("short")}); !errors.Is(err, ErrBadCheckpoint) {
		t.Fatalf("err=%v want ErrBadCheckpoint", err)
	}
}

func TestOffsetLocatesBrokenFrame(t *testing.T) {
	b := NewBuilder()
	f0 := b.Add([]byte("zero"))
	f1 := b.Add([]byte("one"))
	bad := Frame{Seq: 2, PrevDigest: bytes.Repeat([]byte{9}, digestSize), Payload: []byte("x")}
	stream := append(b.Bytes(), bad.Encode()...)

	p := NewParser()
	p.Append(stream)
	mustNext(t, p)
	mustNext(t, p)
	_, err := p.Next()
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("err=%v", err)
	}
	want := len(f0.Encode()) + len(f1.Encode())
	if ce.Offset != want {
		t.Fatalf("offset=%d want %d", ce.Offset, want)
	}
}

func TestBadMagicIsBadData(t *testing.T) {
	raw, _ := framesRaw(t, "x")
	raw[0] = 0x00
	p := NewParser()
	p.Append(raw)
	if _, err := p.Next(); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("err=%v want ErrBadMagic", err)
	}
}

func TestCRCErrorIsBadData(t *testing.T) {
	raw, _ := framesRaw(t, "payload")
	raw[HeaderLen] ^= 0xFF // flip a payload byte
	p := NewParser()
	p.Append(raw)
	if _, err := p.Next(); !errors.Is(err, ErrCRC) {
		t.Fatalf("err=%v want ErrCRC", err)
	}
}

func TestLengthQuotaCheckedBeforeAllocation(t *testing.T) {
	// Declare a huge length but send only the header: oversize must be
	// reported as bad data even though the frame is also incomplete.
	hdr := make([]byte, HeaderLen)
	hdr[0], hdr[1] = Magic0, Magic1
	binary.BigEndian.PutUint64(hdr[magicSize:], 0)
	binary.BigEndian.PutUint32(hdr[magicSize+seqSize+digestSize:], MaxPayload+1)

	p := NewParser()
	p.Append(hdr)
	if _, err := p.Next(); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err=%v want ErrPayloadTooLarge", err)
	}
}

func TestZeroLengthBoundary(t *testing.T) {
	hdr := make([]byte, HeaderLen)
	hdr[0], hdr[1] = Magic0, Magic1
	payloadLen := uint32(10)
	binary.BigEndian.PutUint32(hdr[magicSize+seqSize+digestSize:], payloadLen)

	p := NewParser(WithMaxPayload(9))
	p.Append(hdr)
	if _, err := p.Next(); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err=%v want ErrPayloadTooLarge", err)
	}
}

func TestIncompleteVsBadDataDistinguished(t *testing.T) {
	raw, _ := framesRaw(t, "abc")

	// Byte-by-byte: never bad data until completion; always NeedMoreData.
	p := NewParser()
	for i := 0; i < len(raw)-1; i++ {
		p.Append(raw[i : i+1])
		if _, err := p.Next(); !errors.Is(err, ErrNeedMoreData) {
			t.Fatalf("after %d bytes err=%v want NeedMoreData", i+1, err)
		}
	}
	p.Append(raw[len(raw)-1:])
	ev := mustNext(t, p)
	if string(ev.Frame.Payload) != "abc" {
		t.Fatalf("payload=%q", ev.Frame.Payload)
	}
}
