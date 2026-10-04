package framestream

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

func encode(frames ...[]byte) []byte {
	var buf bytes.Buffer
	var hdr [headerLen]byte
	for _, f := range frames {
		binary.BigEndian.PutUint32(hdr[:], uint32(len(f)))
		buf.Write(hdr[:])
		buf.Write(f)
	}
	return buf.Bytes()
}

func collect(e **Endpoint) *[]string {
	got := &[]string{}
	*e = NewEndpoint(func(p []byte) {
		*got = append(*got, string(p))
	})
	return got
}

func TestSingleFrame(t *testing.T) {
	var e *Endpoint
	got := collect(&e)
	if err := e.Send([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	wire := append([]byte(nil), e.Pending()...)
	e.Consume(len(wire))
	if len(e.Pending()) != 0 {
		t.Fatalf("pending not drained: %d", len(e.Pending()))
	}
	if err := e.Input(wire); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, []string{"hello"}) {
		t.Fatalf("got %v", *got)
	}
}

// TestByteAtATime feeds the wire stream one byte per Input call: every
// possible read boundary is exercised.
func TestByteAtATime(t *testing.T) {
	frames := [][]byte{[]byte("alpha"), {}, []byte("beta gamma"), []byte("x")}
	wire := encode(frames...)
	var e *Endpoint
	got := collect(&e)
	for i := 0; i < len(wire); i++ {
		if err := e.Input(wire[i : i+1]); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
	}
	want := []string{"alpha", "", "beta gamma", "x"}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("got %q want %q", *got, want)
	}
}

// TestAllSplitPoints splits the wire stream at every position and feeds
// the two halves separately; the result must be identical each time.
func TestAllSplitPoints(t *testing.T) {
	frames := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	wire := encode(frames...)
	want := []string{"one", "two", "three"}
	for cut := 0; cut <= len(wire); cut++ {
		var e *Endpoint
		got := collect(&e)
		if err := e.Input(wire[:cut]); err != nil {
			t.Fatalf("cut %d first half: %v", cut, err)
		}
		if err := e.Input(wire[cut:]); err != nil {
			t.Fatalf("cut %d second half: %v", cut, err)
		}
		if !reflect.DeepEqual(*got, want) {
			t.Fatalf("cut %d: got %q want %q", cut, *got, want)
		}
	}
}

// TestAllTripleSplits feeds the stream in three chunks at every pair of
// split points, covering frames straddling multiple reads.
func TestAllTripleSplits(t *testing.T) {
	wire := encode([]byte("abcdef"), []byte("gh"))
	want := []string{"abcdef", "gh"}
	for i := 0; i <= len(wire); i++ {
		for j := i; j <= len(wire); j++ {
			var e *Endpoint
			got := collect(&e)
			for _, chunk := range [][]byte{wire[:i], wire[i:j], wire[j:]} {
				if err := e.Input(chunk); err != nil {
					t.Fatalf("split (%d,%d): %v", i, j, err)
				}
			}
			if !reflect.DeepEqual(*got, want) {
				t.Fatalf("split (%d,%d): got %q want %q", i, j, *got, want)
			}
		}
	}
}

// TestHalfClose: A closes its send direction but keeps receiving B's
// remaining frames.
func TestHalfClose(t *testing.T) {
	var a, b *Endpoint
	gotA := collect(&a)
	gotB := collect(&b)

	// A sends one frame, then half-closes.
	if err := a.Send([]byte("a1")); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if a.SendState() != SendHalfClosed {
		t.Fatalf("send state %v", a.SendState())
	}
	if err := a.Send([]byte("a2")); err != ErrSendClosed {
		t.Fatalf("send after half-close: %v", err)
	}

	// B still sends two more frames; A must receive them.
	if err := b.Send([]byte("b1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Send([]byte("b2")); err != nil {
		t.Fatal(err)
	}

	// Move bytes A->B and B->A, byte by byte on the B->A path.
	wireAB := append([]byte(nil), a.Pending()...)
	wireBA := append([]byte(nil), b.Pending()...)
	if err := b.Input(wireAB); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(wireBA); i++ {
		if err := a.Input(wireBA[i : i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(*gotB, []string{"a1"}) {
		t.Fatalf("B got %q", *gotB)
	}
	if !reflect.DeepEqual(*gotA, []string{"b1", "b2"}) {
		t.Fatalf("A got %q", *gotA)
	}

	// A's receive direction is still open until B's transport EOF arrives.
	if a.RecvState() != RecvOpen {
		t.Fatalf("recv state %v", a.RecvState())
	}
	if err := b.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if err := a.InputEOF(); err != nil {
		t.Fatalf("clean EOF: %v", err)
	}
	if a.RecvState() != RecvClosed {
		t.Fatalf("recv state %v", a.RecvState())
	}
}

// TestCleanEOF: stream ends exactly on a frame boundary -> normal end.
func TestCleanEOF(t *testing.T) {
	var e *Endpoint
	got := collect(&e)
	if err := e.Input(encode([]byte("fin"))); err != nil {
		t.Fatal(err)
	}
	if err := e.InputEOF(); err != nil {
		t.Fatalf("want clean EOF, got %v", err)
	}
	if e.RecvState() != RecvClosed {
		t.Fatalf("recv state %v", e.RecvState())
	}
	if !reflect.DeepEqual(*got, []string{"fin"}) {
		t.Fatalf("got %q", *got)
	}
	// No further input is accepted after close.
	if err := e.Input([]byte{0}); err != ErrRecvClosed {
		t.Fatalf("input after close: %v", err)
	}
	if err := e.InputEOF(); err != ErrDoubleInputEOF {
		t.Fatalf("double EOF: %v", err)
	}
}

// TestTruncatedEOF: the peer goes away mid-frame -> truncation, at every
// possible cut inside a frame (header and payload alike).
func TestTruncatedEOF(t *testing.T) {
	wire := encode([]byte("complete"), []byte("unfinished"))
	frameLen := headerLen + len("unfinished")
	for have := 1; have < frameLen; have++ {
		var e *Endpoint
		got := collect(&e)
		prefix := encode([]byte("complete"))
		cut := append(prefix, wire[len(prefix):len(prefix)+have]...)
		if err := e.Input(cut); err != nil {
			t.Fatalf("have %d: %v", have, err)
		}
		err := e.InputEOF()
		te, ok := err.(*TruncatedError)
		if !ok {
			t.Fatalf("have %d: want TruncatedError, got %v", have, err)
		}
		if te.Have != have {
			t.Fatalf("have %d: te.Have=%d", have, te.Have)
		}
		if have < headerLen && te.Want != 0 {
			t.Fatalf("have %d: te.Want=%d, want 0 (header incomplete)", have, te.Want)
		}
		if have >= headerLen && te.Want != frameLen {
			t.Fatalf("have %d: te.Want=%d, want %d", have, te.Want, frameLen)
		}
		// The complete frame before the truncation must still be delivered.
		if !reflect.DeepEqual(*got, []string{"complete"}) {
			t.Fatalf("have %d: got %q", have, *got)
		}
	}
}

// TestReset kills both directions immediately.
func TestReset(t *testing.T) {
	var e *Endpoint
	collect(&e)
	if err := e.Send([]byte("x")); err != nil {
		t.Fatal(err)
	}
	e.Reset()
	if e.SendState() != SendReset || e.RecvState() != RecvReset {
		t.Fatalf("states %v/%v", e.SendState(), e.RecvState())
	}
	if len(e.Pending()) != 0 {
		t.Fatal("pending not discarded")
	}
	if err := e.Send([]byte("y")); err != ErrReset {
		t.Fatalf("send: %v", err)
	}
	if err := e.CloseSend(); err != ErrReset {
		t.Fatalf("close send: %v", err)
	}
	if err := e.Input(encode([]byte("z"))); err != ErrReset {
		t.Fatalf("input: %v", err)
	}
	if err := e.InputEOF(); err != ErrReset {
		t.Fatalf("eof: %v", err)
	}
}

func TestFrameTooLarge(t *testing.T) {
	var e *Endpoint
	collect(&e)
	var hdr [headerLen]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameSize+1)
	if err := e.Input(hdr[:]); err != ErrFrameTooLarge {
		t.Fatalf("input: %v", err)
	}
	if err := e.Send(make([]byte, MaxFrameSize+1)); err != ErrFrameTooLarge {
		t.Fatalf("send: %v", err)
	}
}

func TestEmptyFrame(t *testing.T) {
	var e *Endpoint
	got := collect(&e)
	if err := e.Input(encode([]byte{})); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, []string{""}) {
		t.Fatalf("got %q", *got)
	}
}
