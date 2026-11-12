package netsec

import (
	"errors"
	"strings"
	"testing"

	"example.org/netsec/q0122/hpack"
)

func TestFrameErrorTextAndCode(t *testing.T) {
	fe := protocolError(42, ErrInterleave)
	if fe.Code != ProtocolError || fe.StreamID != 42 {
		t.Fatalf("bad FrameError: %+v", fe)
	}
	if !strings.Contains(fe.Error(), "stream 42") || !strings.Contains(fe.Error(), "interleaved") {
		t.Fatalf("unexpected error text: %q", fe.Error())
	}
	if !errors.Is(fe, ErrInterleave) {
		t.Fatal("Unwrap chain broken")
	}

	se := streamError(7, ErrBlockTooLarge)
	if se.Code != StreamError || !errors.Is(se, ErrBlockTooLarge) {
		t.Fatalf("bad stream error: %+v", se)
	}
}

func TestDecoderDynamicTableManagement(t *testing.T) {
	dec := hpack.NewDecoder(64, 128)
	if got := dec.DynamicTableSize(); got != 0 {
		t.Fatalf("initial size = %d, want 0", got)
	}

	enc := newTestEncoder()
	enc.writeFields([]hpack.Field{
		{Name: "a", Value: "1"},
		{Name: "b", Value: "22"},
		{Name: "c", Value: "333"},
	})
	fields, err := dec.DecodeFull(enc.bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("fields = %d", len(fields))
	}
	if dec.DynamicTableSize() == 0 {
		t.Fatal("dynamic table should be occupied")
	}

	// Shrinking via SETTINGS evicts entries.
	dec.SetMaxSize(0)
	if dec.DynamicTableSize() != 0 {
		t.Fatalf("after SetMaxSize(0) size = %d", dec.DynamicTableSize())
	}

	// Allowed maximum clamps an oversized SETTINGS value.
	small := hpack.NewDecoder(10, 10)
	small.SetMaxSize(1000)
	if got := small.DynamicTableSize(); got != 0 {
		t.Fatalf("clamped resize left size %d", got)
	}

	// NewDecoder normalizes an inverted limit pair.
	inv := hpack.NewDecoder(100, 10)
	enc2 := newTestEncoder()
	enc2.writeField(hpack.Field{Name: "k", Value: "v"})
	if _, err := inv.DecodeFull(enc2.bytes()); err != nil {
		t.Fatalf("decoder with normalized limits failed: %v", err)
	}
}

func TestDynamicTableEvictionWhenEntryTooLarge(t *testing.T) {
	dec := hpack.NewDecoder(40, 40)
	enc := newTestEncoder()
	// First entry fits; second entry alone exceeds the table and must empty it.
	enc.writeField(hpack.Field{Name: "k", Value: "v"})
	if _, err := dec.DecodeFull(enc.bytes()); err != nil {
		t.Fatal(err)
	}
	if dec.DynamicTableSize() == 0 {
		t.Fatal("first entry should occupy the table")
	}

	big := newTestEncoder()
	big.writeField(hpack.Field{Name: "a-much-too-long-name", Value: "an-even-longer-value-here"})
	if _, err := dec.DecodeFull(big.bytes()); err != nil {
		t.Fatal(err)
	}
	if dec.DynamicTableSize() != 0 {
		t.Fatalf("oversized entry did not empty table: %d", dec.DynamicTableSize())
	}
}

func TestResetFrameOnOpenBlockReleasesAndReuses(t *testing.T) {
	block := encodeBlock(t, false, wantFields(2)...)
	a := newTestAssembler(t, NewDefaultLimits())
	if _, err := a.HandleFrame(Frame{
		Type: FrameHeaders, StreamID: 9, Payload: block[:3],
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleFrame(Frame{Type: FrameReset, StreamID: 9}); err == nil {
		t.Fatal("reset of open stream must return a stream error")
	}
	if a.HasOpenBlock() || a.OpenStreamID() != 0 {
		t.Fatal("open block survived RST_STREAM")
	}
	// Fresh block on the same id is assembled from scratch.
	result := feedFragments(t, a, 9, [][]byte{block})
	if result.BlockBytes != len(block) {
		t.Fatalf("block after reset = %d bytes, want %d", result.BlockBytes, len(block))
	}
}

func TestDefaultLimitsValues(t *testing.T) {
	l := NewDefaultLimits()
	if l.MaxBlockBytes != defaultMaxBlockBytes || l.MaxFields != defaultMaxFields {
		t.Fatalf("defaults changed: %+v", l)
	}
}

// TestConnectionCloseIsIdempotent ensures repeated Close and post-close
// frames all report the closed error without publishing.
func TestConnectionCloseIsIdempotent(t *testing.T) {
	a := newTestAssembler(t, NewDefaultLimits())
	a.Close()
	a.Close()
	_, err := a.HandleFrame(Frame{Type: FrameContinuation, StreamID: 1})
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Code != ClosedError || !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("got %v, want ClosedError/ErrConnectionClosed", err)
	}
}
