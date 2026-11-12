package netsec

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"testing"
)

// compressMessage produces the on-wire compressed payload for one message:
// a DEFLATE stream terminated by an empty stored block, with the RFC 7692
// 0x0000ffff trailer stripped. The writer keeps its dictionary so the second
// message may back-reference the first, mirroring a takeover-enabled peer.
func compressMessage(t *testing.T, w *flate.Writer, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w.Reset(&buf)
	if _, err := w.Write(plain); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flate flush: %v", err)
	}
	raw := buf.Bytes()
	if len(raw) < len(rfc7692Tail) || !bytes.Equal(raw[len(raw)-len(rfc7692Tail):], rfc7692Tail[:]) {
		t.Fatalf("sync flush did not end with 0x0000ffff: % x", raw)
	}
	return raw[:len(raw)-len(rfc7692Tail)]
}

func newCompressor(t *testing.T) *flate.Writer {
	t.Helper()
	w, err := flate.NewWriter(nil, flate.BestCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// TestFlateTakeoverReusesDictionary sends two messages where the second one
// compresses using history established by the first. Only a receiver that
// retains the dictionary can inflate it.
func TestFlateTakeoverReusesDictionary(t *testing.T) {
	w := newCompressor(t)
	first := bytes.Repeat([]byte("dictionary-prefix-"), 200)
	second := append(append([]byte(nil), first...), []byte("-suffix")...)

	wire1 := compressMessage(t, w, first)
	wire2 := compressMessage(t, w, second)
	if len(wire2) >= len(second) {
		t.Fatalf("second message should be small via back-references, got %d bytes", len(wire2))
	}

	d := NewFlateDecompressor(true)
	r := NewReceiver(ReceiverConfig{Decompressor: d, ContextTakeover: true})

	ev, err := r.HandleFrame(Frame{Fin: true, RSV1: true, Opcode: OpBinary, Payload: wire1})
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if !bytes.Equal(ev.Message.Payload, first) {
		t.Fatalf("first message payload mismatch")
	}

	// Interleaved control frame must not perturb the retained dictionary.
	if _, err := r.HandleFrame(controlFrame(OpPing, 1, 2, 3)); err != nil {
		t.Fatalf("ping: %v", err)
	}

	ev, err = r.HandleFrame(Frame{Fin: true, RSV1: true, Opcode: OpBinary, Payload: wire2})
	if err != nil {
		t.Fatalf("second message using shared dictionary: %v", err)
	}
	if !bytes.Equal(ev.Message.Payload, second) {
		t.Fatalf("second message payload mismatch")
	}
}

// TestFlateNoTakeoverClearsDictionary shows that with takeover disabled the
// receiver clears its state after each message; independently compressed
// messages still inflate correctly.
func TestFlateNoTakeoverClearsDictionary(t *testing.T) {
	d := NewFlateDecompressor(false)
	r := NewReceiver(ReceiverConfig{Decompressor: d, ContextTakeover: false})

	for _, plain := range [][]byte{[]byte("hello"), []byte("world"), {}} {
		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, flate.DefaultCompression)
		if err != nil {
			t.Fatalf("flate.NewWriter: %v", err)
		}
		_, _ = w.Write(plain)
		_ = w.Flush()
		_ = w.Close()
		raw := buf.Bytes()
		raw = raw[:len(raw)-len(rfc7692Tail)]

		ev, err := r.HandleFrame(Frame{Fin: true, RSV1: true, Opcode: OpText, Payload: raw})
		if err != nil {
			t.Fatalf("message %q: %v", plain, err)
		}
		if !bytes.Equal(ev.Message.Payload, plain) {
			t.Fatalf("payload mismatch: got %q want %q", ev.Message.Payload, plain)
		}
	}
}

// TestFlateCorruptStreamDiscardsMessage feeds garbage and asserts the whole
// message is dropped, the dictionary is reset, and the next message works.
func TestFlateCorruptStreamDiscardsMessage(t *testing.T) {
	d := NewFlateDecompressor(true)
	r := NewReceiver(ReceiverConfig{Decompressor: d, ContextTakeover: true})

	_, err := r.HandleFrame(Frame{
		Fin:     true,
		RSV1:    true,
		Opcode:  OpText,
		Payload: []byte{0xff, 0xff, 0xff, 0xff, 0x01},
	})
	if !errors.Is(err, ErrDecompression) {
		t.Fatalf("want ErrDecompression, got %v", err)
	}

	// A valid standalone compressed message still inflates after the reset.
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate.NewWriter: %v", err)
	}
	_, _ = io.WriteString(w, "recovered")
	_ = w.Flush()
	_ = w.Close()
	raw := buf.Bytes()
	raw = raw[:len(raw)-len(rfc7692Tail)]

	ev, err := r.HandleFrame(Frame{Fin: true, RSV1: true, Opcode: OpText, Payload: raw})
	if err != nil {
		t.Fatalf("recovery message: %v", err)
	}
	if string(ev.Message.Payload) != "recovered" {
		t.Fatalf("payload mismatch: %q", ev.Message.Payload)
	}
}

// TestFlateHistoryWindowTrimsTo32KiB keeps the retained dictionary bounded.
func TestFlateHistoryTrimsTo32KiB(t *testing.T) {
	fd := NewFlateDecompressor(true).(*flateDecompressor)
	fd.retainHistory(make([]byte, flateMaxWindow+10))
	if len(fd.history) != flateMaxWindow {
		t.Fatalf("history length %d, want %d", len(fd.history), flateMaxWindow)
	}
	fd.retainHistory(make([]byte, 5))
	if len(fd.history) != flateMaxWindow {
		t.Fatalf("history should stay at window size, got %d", len(fd.history))
	}
	fd.Reset()
	if len(fd.history) != 0 || fd.reader != nil {
		t.Fatalf("reset must clear history and reader")
	}
}

// TestFlateCloseReleasesReader exercises Close after at least one inflation,
// plus a second Close being harmless.
func TestFlateCloseReleasesReader(t *testing.T) {
	d := NewFlateDecompressor(true).(*flateDecompressor)
	r := NewReceiver(ReceiverConfig{Decompressor: d, ContextTakeover: true})

	ev, err := r.HandleFrame(Frame{Fin: true, RSV1: true, Opcode: OpText, Payload: nil})
	if err != nil {
		t.Fatalf("empty compressed message: %v", err)
	}
	if len(ev.Message.Payload) != 0 {
		t.Fatalf("empty payload expected, got %d bytes", len(ev.Message.Payload))
	}
	if d.reader == nil {
		t.Fatalf("reader should be lazily created")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if d.reader != nil {
		t.Fatalf("close must drop the reader")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second close must be a no-op: %v", err)
	}
}
