package termfilter

import (
	"bytes"
	"testing"
)

// FuzzSplitEquivalence feeds random byte streams through the filter in one
// shot and again split at every possible cut, requiring identical output.
func FuzzSplitEquivalence(f *testing.F) {
	seeds := [][]byte{
		[]byte("plain \x1b[31mred\x1b[0m 中文🙂"),
		[]byte("\x1b]52;c;Zm9v\x07\x1bP1$u\x1b\\"),
		[]byte("\x9b31m\x9b0m\x9dosc\x9c"),
		{0x1b, '[', '?', '2', '5', 'h', 0xC0, 0x80, 0xF0, 0x9F},
		{0x1b, 0x1b, '[', 'm', 0x98, 0x9F, 0x90},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in []byte) {
		want := Filter(in)

		// Every single-cut split.
		for cut := 0; cut <= len(in); cut++ {
			var out bytes.Buffer
			w := NewWriter(&out)
			if _, err := w.Write(in[:cut]); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(in[cut:]); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("cut=%d: streamed %q, want %q (in % x)", cut, out.Bytes(), want, in)
			}
		}

		// Byte-at-a-time delivery.
		var out bytes.Buffer
		w := NewWriter(&out)
		for _, b := range in {
			if _, err := w.Write([]byte{b}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("byte-wise %q != %q (in % x)", out.Bytes(), want, in)
		}
	})
}

// FuzzLimitEquivalence applies a small sequence limit and again requires the
// one-shot and split deliveries to agree.
func FuzzLimitEquivalence(f *testing.F) {
	f.Add([]byte("\x1b[31mX\x1b]0;t\x07Y"))

	f.Fuzz(func(t *testing.T, in []byte) {
		opt := MaxSequenceBytes(7)
		want := Filter(in, opt)

		var out bytes.Buffer
		w := NewWriter(&out, opt)
		for i := 0; i < len(in); i++ {
			if _, err := w.Write(in[i : i+1]); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), want) {
			t.Fatalf("limit: %q != %q (in % x)", out.Bytes(), want, in)
		}
	})
}
