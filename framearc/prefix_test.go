package framearc

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestCanonicalCodesErrors(t *testing.T) {
	cases := []struct {
		name  string
		table [][2]uint8
	}{
		{"empty", nil},
		{"zero length", [][2]uint8{{'a', 0}}},
		{"overlong", [][2]uint8{{'a', maxCodeLen + 1}}},
		{"duplicate symbol", [][2]uint8{{'a', 1}, {'a', 2}}},
		{"oversubscribed", [][2]uint8{{'a', 1}, {'b', 1}, {'c', 1}}},
	}
	for _, c := range cases {
		if _, err := canonicalCodes(c.table); err == nil {
			t.Errorf("%s: want error", c.name)
		}
	}
	// Incomplete codes are legal: unused bit patterns simply never occur.
	if _, err := canonicalCodes([][2]uint8{{'a', 3}}); err != nil {
		t.Errorf("incomplete code should be valid: %v", err)
	}
}

func TestHuffmanRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	datasets := [][]byte{
		[]byte("a"),
		[]byte("ab"),
		[]byte("aaaaaaaaaaaaaaaaaaaaa"),
		[]byte("the quick brown fox jumps over the lazy dog"),
		bytes.Repeat([]byte("banana "), 500),
	}
	for i := 0; i < 5; i++ {
		b := make([]byte, 1+rng.Intn(3000))
		rng.Read(b)
		datasets = append(datasets, b)
	}
	for i, data := range datasets {
		arc, err := AppendFrame(nil, uint32(i), data)
		if err != nil {
			t.Fatalf("dataset %d: %v", i, err)
		}
		h, err := parseHeader(arc)
		if err != nil {
			t.Fatalf("dataset %d: %v", i, err)
		}
		if len(arc) != h.frameLen() {
			t.Fatalf("dataset %d: frameLen %d != %d", i, h.frameLen(), len(arc))
		}
		got, err := h.decode(arc)
		if err != nil {
			t.Fatalf("dataset %d: %v", i, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("dataset %d: round trip mismatch", i)
		}
	}
}

func TestSingleBitFlipAlwaysDetected(t *testing.T) {
	data := bytes.Repeat([]byte("detect me if you can "), 40)
	arc, err := AppendFrame(nil, 0, data)
	if err != nil {
		t.Fatal(err)
	}
	for off := 0; off < len(arc); off++ {
		corrupted := FlipBit(arc, off, uint(off%8))
		r := &Reader{}
		events := r.Feed(corrupted)
		events = append(events, r.Close()...)
		for _, ev := range events {
			if ev.Kind == EventFrame {
				t.Fatalf("flip at byte %d went undetected", off)
			}
		}
	}
}
