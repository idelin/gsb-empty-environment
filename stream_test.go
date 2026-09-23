package huffcodec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// encodeSymbols renders symbols with the given codes into a padded byte
// stream, mirroring what the format encoder does.
func encodeSymbols(symbols []byte, codes []Code) []byte {
	bw := bitWriter{}
	for _, s := range symbols {
		bw.writeBits(codes[s].Bits, uint(codes[s].Len))
	}
	bw.flush()
	return bw.buf
}

// drainAll feeds payload to dec in chunks of chunkSize with a small output
// buffer, collecting all output. The shared budget doubles as the symbol
// count, so padding bits at the tail can never be misread as data.
func drainAll(t *testing.T, dec *StreamDecoder, payload []byte, chunkSize int) []byte {
	t.Helper()
	var got []byte
	buf := make([]byte, 3)
	for off := 0; off < len(payload); {
		n := chunkSize
		if off+n > len(payload) {
			n = len(payload) - off
		}
		c, p, err := dec.Decode(payload[off:off+n], buf)
		if err != nil && !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("chunk %d: %v", off, err)
		}
		got = append(got, buf[:p]...)
		off += c
	}
	for {
		_, p, err := dec.Decode(nil, buf)
		if err != nil && !errors.Is(err, ErrQuotaExceeded) {
			t.Fatalf("drain: %v", err)
		}
		if p == 0 {
			break
		}
		got = append(got, buf[:p]...)
	}
	return got
}

func TestStreamRoundTripArbitraryChunks(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var freqs [256]uint64
	for i := 0; i < 40; i++ {
		freqs[rng.Intn(256)] = uint64(1 + rng.Intn(500))
	}
	lengths, err := CodeLengths(freqs[:], 16)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	var alphabet []byte
	for s, l := range lengths {
		if l > 0 {
			alphabet = append(alphabet, byte(s))
		}
	}
	data := make([]byte, 500)
	for i := range data {
		data[i] = alphabet[rng.Intn(len(alphabet))]
	}
	payload := encodeSymbols(data, codes)

	for _, chunk := range []int{1, 2, 3, 5, 7, 8, 13, 64, 1000} {
		dec, err := NewStreamDecoder(lengths, int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		got := drainAll(t, dec, payload, chunk)
		if !bytes.Equal(got, data) {
			t.Fatalf("chunk size %d: decoded %d bytes, want %d", chunk, len(got), len(data))
		}
	}
}

func TestLayeredTableLongCodes(t *testing.T) {
	// 13 symbols with lengths 1..12,12: Kraft sum is exactly 1 and the
	// 12-bit codes force sub-tables below the 8-bit root level.
	lengths := make([]uint8, 256)
	syms := "abcdefghijklm"
	for i := 0; i < 12; i++ {
		lengths[syms[i]] = uint8(i + 1)
	}
	lengths['m'] = 12
	if k := KraftScaled(lengths); k != 1<<MaxCodeLength {
		t.Fatalf("test table not complete: %d", k)
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	tab, err := buildDecodeTable(codes)
	if err != nil {
		t.Fatal(err)
	}
	// The longest code must live in a sub-table, not the root level.
	longest := codes['m']
	rootIdx := longest.Bits >> (longest.Len - levelBits)
	if tab.entries[rootIdx].sub == nil {
		t.Fatalf("12-bit code not routed through a sub-table")
	}

	data := []byte("mlkjiihgfedcbaam")
	payload := encodeSymbols(data, codes)
	dec, err := NewStreamDecoder(lengths, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got := drainAll(t, dec, payload, 1); !bytes.Equal(got, data) {
		t.Fatalf("long-code round trip failed: %q", got)
	}
}

func TestOutputBudgetSharedAcrossCalls(t *testing.T) {
	lengths := make([]uint8, 256)
	lengths['a'] = 1 // code 0
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	payload := encodeSymbols([]byte("aaaaaa"), codes) // 6 one-bit symbols

	dec, err := NewStreamDecoder(lengths, 4)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 2)
	total := 0
	var sawQuota error
	for i := 0; i < 10 && total < 6; i++ {
		var src []byte
		if i == 0 {
			src = payload
		}
		_, p, err := dec.Decode(src, out)
		total += p
		if err != nil {
			sawQuota = err
			break
		}
	}
	if !errors.Is(sawQuota, ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", sawQuota)
	}
	if dec.Produced != 4 || total != 4 {
		t.Fatalf("budget not shared across calls: produced %d, want 4", dec.Produced)
	}
}

func TestFinishDistinguishesTruncatedFromInvalid(t *testing.T) {
	lengths := make([]uint8, 256)
	lengths['a'] = 1 // code 0
	lengths['b'] = 2 // code 10
	lengths['c'] = 2 // code 11 (complete table)

	// Clean end: a full codeword, no leftover bits.
	dec, err := NewStreamDecoder(lengths, -1)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 4)
	if n, err := dec.DecodeBits(0b10, 2, out); err != nil || n != 1 || out[0] != 'b' {
		t.Fatalf("decode b: n=%d err=%v", n, err)
	}
	if err := dec.Finish(); err != nil {
		t.Fatalf("clean finish: %v", err)
	}

	// Truncated tail: "1" is a proper prefix of codeword "10".
	dec, err = NewStreamDecoder(lengths, -1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := dec.DecodeBits(0b1, 1, out); err != nil || n != 0 {
		t.Fatalf("partial feed: n=%d err=%v", n, err)
	}
	if err := dec.Finish(); !errors.Is(err, ErrIncompleteCodeword) {
		t.Fatalf("want ErrIncompleteCodeword, got %v", err)
	}

	// Invalid at finish on an incomplete (single-symbol) table: bit 1
	// can never start the only codeword (0).
	single := make([]uint8, 256)
	single['a'] = 1
	dec, err = NewStreamDecoder(single, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.DecodeBits(0b1, 1, out); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("single-symbol invalid bit: got %v", err)
	}
}

func TestStreamRejectsBadTables(t *testing.T) {
	over := []uint8{1, 1, 1}
	if _, err := NewStreamDecoder(over, -1); !errors.Is(err, ErrOversubscribed) {
		t.Fatalf("oversubscribed: got %v", err)
	}
	incomplete := []uint8{2, 2}
	if _, err := NewStreamDecoder(incomplete, -1); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete: got %v", err)
	}
	empty := make([]uint8, 4)
	if _, err := NewStreamDecoder(empty, -1); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("empty: got %v", err)
	}
}
