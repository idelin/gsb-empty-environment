package adaptivehuffman

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// encodeAll encodes data with a fresh encoder and returns the raw bit stream.
func encodeAll(t *testing.T, data []byte) []byte {
	t.Helper()
	enc := NewEncoder()
	buf := new(bytes.Buffer)
	bw := NewBitWriter(buf)
	for _, b := range data {
		if err := enc.EncodeSymbol(bw, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestStateRestoreEncoder verifies that exporting the encoder state at any
// symbol boundary, restoring it into a fresh encoder, and continuing
// produces a bit stream identical to an uninterrupted run.
func TestStateRestoreEncoder(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	random64 := make([]byte, 64)
	rng.Read(random64)
	all256 := make([]byte, 256)
	for i := range all256 {
		all256[i] = byte(i)
	}
	inputs := [][]byte{
		{},
		{'a'},
		[]byte("abracadabra"),
		bytes.Repeat([]byte{7}, 100),
		random64,
		all256,
	}
	for ti, data := range inputs {
		ref := encodeAll(t, data)
		// Cut at every boundary for short inputs, sample for long ones.
		step := 1
		if len(data) > 70 {
			step = 17
		}
		for k := 0; k <= len(data); k += step {
			enc1 := NewEncoder()
			buf := new(bytes.Buffer)
			bw := NewBitWriter(buf) // shared bit writer: bit alignment carries over
			for _, b := range data[:k] {
				if err := enc1.EncodeSymbol(bw, b); err != nil {
					t.Fatal(err)
				}
			}
			enc2, err := RestoreEncoder(enc1.ExportState())
			if err != nil {
				t.Fatalf("input %d cut %d: %v", ti, k, err)
			}
			checkModelInvariants(t, enc2.m)
			for _, b := range data[k:] {
				if err := enc2.EncodeSymbol(bw, b); err != nil {
					t.Fatal(err)
				}
			}
			if err := bw.Flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), ref) {
				t.Fatalf("input %d cut %d: resumed encoding differs from uninterrupted", ti, k)
			}
		}
	}
}

// TestStateRestoreDecoder verifies that a decoder restored from an exported
// state continues decoding identically to the original decoder.
func TestStateRestoreDecoder(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(rng.Intn(40))
	}
	stream := encodeAll(t, data)

	for _, k := range []int{0, 1, 2, 7, 50, 128, 299, 300} {
		dec1 := NewDecoder()
		br := NewBitReader(bytes.NewReader(stream)) // shared bit reader
		out := make([]byte, 0, len(data))
		for i := 0; i < k; i++ {
			b, err := dec1.DecodeSymbol(br)
			if err != nil {
				t.Fatalf("cut %d symbol %d: %v", k, i, err)
			}
			out = append(out, b)
		}
		dec2, err := RestoreDecoder(dec1.ExportState())
		if err != nil {
			t.Fatalf("cut %d: %v", k, err)
		}
		checkModelInvariants(t, dec2.m)
		for i := k; i < len(data); i++ {
			b, err := dec2.DecodeSymbol(br)
			if err != nil {
				t.Fatalf("cut %d symbol %d: %v", k, i, err)
			}
			out = append(out, b)
		}
		if !bytes.Equal(out, data) {
			t.Fatalf("cut %d: resumed decoding mismatch", k)
		}
	}
}

// TestStateRoundTrip checks that a restored model exports byte-identical
// state again (idempotent export).
func TestStateRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	enc := NewEncoderWithMaxWeight(128) // exercise rescale paths too
	bw := NewBitWriter(io.Discard)
	for i := 0; i < 3000; i++ {
		if err := enc.EncodeSymbol(bw, byte(rng.Intn(60))); err != nil {
			t.Fatal(err)
		}
	}
	st := enc.ExportState()
	enc2, err := RestoreEncoder(st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc2.ExportState(), st) {
		t.Fatal("re-exported state differs from imported state")
	}
}

// TestCorruptState ensures truncated, corrupted or random state blobs are
// rejected (or at least never cause a panic).
func TestCorruptState(t *testing.T) {
	enc := NewEncoder()
	bw := NewBitWriter(io.Discard)
	for _, b := range []byte("state corruption test input") {
		if err := enc.EncodeSymbol(bw, b); err != nil {
			t.Fatal(err)
		}
	}
	st := enc.ExportState()

	for cut := 0; cut < len(st); cut++ {
		if _, err := RestoreEncoder(st[:cut]); err == nil {
			t.Fatalf("truncated state of %d bytes accepted", cut)
		}
	}
	// Bit flips must never panic; most should be rejected.
	for i := 0; i < len(st); i++ {
		bad := append([]byte(nil), st...)
		bad[i] ^= 0xFF
		_, _ = RestoreEncoder(bad)
	}
	// Random blobs must never panic.
	rng := rand.New(rand.NewSource(13))
	for i := 0; i < 200; i++ {
		blob := make([]byte, rng.Intn(400))
		rng.Read(blob)
		_, _ = RestoreEncoder(blob)
	}
}

// TestFrameGarbage ensures random garbage fed to DecodeFrame never panics.
func TestFrameGarbage(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 500; i++ {
		blob := make([]byte, rng.Intn(200))
		rng.Read(blob)
		_, _ = DecodeFrame(bytes.NewReader(blob))
	}
}
