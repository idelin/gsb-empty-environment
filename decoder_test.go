package prefixcode

import (
	"bytes"
	"math/rand"
	"testing"
)

func buildTestTable(t *testing.T, msg []byte, maxLen int) ([]int, []Code, *Table) {
	t.Helper()
	var freqs [256]uint64
	for _, b := range msg {
		freqs[b]++
	}
	lengths, err := BuildLengths(freqs[:], maxLen)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := NewTable(lengths)
	if err != nil {
		t.Fatal(err)
	}
	return lengths, codes, tbl
}

// decodeAll decodes stream in chunks of chunk bytes with a small output
// buffer, exercising resumable state across calls.
func decodeAll(t *testing.T, tbl *Table, stream []byte, numSym, chunk int) ([]byte, error) {
	t.Helper()
	dec := NewDecoder(tbl, WithSymbolCount(int64(numSym)), WithQuota(-1))
	var out []byte
	buf := make([]byte, 5)
	for off := 0; off < len(stream); {
		j := off + chunk
		if j > len(stream) {
			j = len(stream)
		}
		for off < j {
			c, n, err := dec.Decode(stream[off:j], buf, false)
			out = append(out, buf[:n]...)
			off += c
			if err == ErrShortBuffer {
				continue
			}
			if err != nil {
				return out, err
			}
			if c == 0 && n == 0 {
				t.Fatalf("decoder made no progress at offset %d", off)
			}
		}
	}
	for {
		_, n, err := dec.Decode(nil, buf, true)
		out = append(out, buf[:n]...)
		if err == ErrShortBuffer {
			continue
		}
		if err != nil {
			return out, err
		}
		if n == 0 {
			return out, nil
		}
	}
}

func TestRoundTrip(t *testing.T) {
	msg := []byte("the quick brown fox jumps over the lazy dog, the quick brown fox")
	_, codes, tbl := buildTestTable(t, msg, 16)
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeAll(t, tbl, stream, len(msg), len(stream))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatalf("got %q want %q", out, msg)
	}
}

func TestArbitraryChunking(t *testing.T) {
	var msg []byte
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 500; i++ {
		msg = append(msg, byte(rng.Intn(20)))
	}
	_, codes, tbl := buildTestTable(t, msg, 16)
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []int{1, 2, 3, 5, 7, 8, 13, 64, len(stream)} {
		out, err := decodeAll(t, tbl, stream, len(msg), chunk)
		if err != nil {
			t.Fatalf("chunk %d: %v", chunk, err)
		}
		if !bytes.Equal(out, msg) {
			t.Fatalf("chunk %d: output mismatch", chunk)
		}
	}
	// Random chunking must not change the decoded output either.
	for trial := 0; trial < 50; trial++ {
		dec := NewDecoder(tbl, WithSymbolCount(int64(len(msg))), WithQuota(-1))
		var out []byte
		buf := make([]byte, 4)
		off := 0
		for off < len(stream) {
			j := off + 1 + rng.Intn(9)
			if j > len(stream) {
				j = len(stream)
			}
			for off < j {
				c, n, err := dec.Decode(stream[off:j], buf, false)
				out = append(out, buf[:n]...)
				off += c
				if err != nil && err != ErrShortBuffer {
					t.Fatal(err)
				}
			}
		}
		for {
			_, n, err := dec.Decode(nil, buf, true)
			out = append(out, buf[:n]...)
			if err != nil && err != ErrShortBuffer {
				t.Fatal(err)
			}
			if n == 0 && err == nil {
				break
			}
		}
		if !bytes.Equal(out, msg) {
			t.Fatalf("random chunking trial %d: output mismatch", trial)
		}
	}
}

func TestTruncatedTailRejected(t *testing.T) {
	msg := []byte("aaaabbbccddddddeeeefffffgggg")
	_, codes, tbl := buildTestTable(t, msg, 16)
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	for _, cut := range []int{1, 2, 3} {
		if len(stream) <= cut {
			break
		}
		_, err := decodeAll(t, tbl, stream[:len(stream)-cut], len(msg), 1)
		if err != ErrTruncated {
			t.Fatalf("cut %d bytes: want ErrTruncated, got %v", cut, err)
		}
	}
	// Declaring far more symbols than the stream contains is also
	// truncation: the zero padding can yield at most a few spurious
	// symbols, then the stream runs dry.
	_, err = decodeAll(t, tbl, stream, len(msg)+10, len(stream))
	if err != ErrTruncated {
		t.Fatalf("inflated symbol count: want ErrTruncated, got %v", err)
	}
}

func TestIncompleteCodewordVsInvalidCode(t *testing.T) {
	// Incomplete table: a=0, b/c are 9-bit codes starting with 10000000x.
	lengths := make([]int, 256)
	lengths['a'] = 1
	lengths['b'] = 9
	lengths['c'] = 9
	if err := Validate(lengths, 9); err != ErrIncomplete {
		t.Fatalf("test table should be incomplete: %v", err)
	}
	tbl, err := NewTable(lengths)
	if err != nil {
		t.Fatal(err)
	}
	// Finish with only the 8-bit prefix of a 9-bit codeword: incomplete.
	dec := NewDecoder(tbl, WithSymbolCount(1), WithQuota(-1))
	if _, _, err := dec.Decode([]byte{0x80}, make([]byte, 4), true); err != ErrTruncated {
		t.Fatalf("partial codeword at finish: want ErrTruncated, got %v", err)
	}
	// Bits that match no codeword at all: invalid/overlong.
	dec = NewDecoder(tbl, WithSymbolCount(1), WithQuota(-1))
	if _, _, err := dec.Decode([]byte{0xC0}, make([]byte, 4), true); err != ErrInvalidCode {
		t.Fatalf("unknown prefix: want ErrInvalidCode, got %v", err)
	}
	// Same invalid pattern detected mid-stream, before finish.
	dec = NewDecoder(tbl, WithSymbolCount(2), WithQuota(-1))
	if _, _, err := dec.Decode([]byte{0xC0, 0x00}, make([]byte, 4), false); err != ErrInvalidCode {
		t.Fatalf("mid-stream invalid: want ErrInvalidCode, got %v", err)
	}
}

func TestQuotaSharedAcrossCalls(t *testing.T) {
	msg := []byte("abcdefghijklmnopqrst") // 20 symbols
	_, codes, tbl := buildTestTable(t, msg, 16)
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	dec := NewDecoder(tbl, WithSymbolCount(int64(len(msg))), WithQuota(7))
	out := make([]byte, len(msg))
	consumed, produced, err := dec.Decode(stream, out, true)
	if err != ErrQuota {
		t.Fatalf("want ErrQuota, got %v", err)
	}
	if produced != 7 {
		t.Fatalf("produced %d symbols, want exactly the 7-symbol quota", produced)
	}
	if dec.Quota() != 0 {
		t.Fatalf("quota left: %d", dec.Quota())
	}
	dec.AddQuota(13)
	c2, p2, err := dec.Decode(stream[consumed:], out[produced:], true)
	if err != nil {
		t.Fatal(err)
	}
	if produced+p2 != len(msg) || !bytes.Equal(out, msg) {
		t.Fatalf("after granting quota: consumed %d+%d, got %q", consumed, c2, out)
	}
}

func TestShortBufferResumable(t *testing.T) {
	msg := []byte("aaaaaaaaabbbbbccccc")
	_, codes, tbl := buildTestTable(t, msg, 16)
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	dec := NewDecoder(tbl, WithSymbolCount(int64(len(msg))), WithQuota(-1))
	small := make([]byte, 3)
	consumed, produced, err := dec.Decode(stream, small, true)
	if err != ErrShortBuffer {
		t.Fatalf("want ErrShortBuffer, got %v", err)
	}
	if produced != 3 {
		t.Fatalf("produced %d, want 3", produced)
	}
	rest := make([]byte, len(msg)-3)
	c2, p2, err := dec.Decode(stream[consumed:], rest, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = c2
	got := append(small, rest[:p2]...)
	if !bytes.Equal(got, msg) {
		t.Fatalf("got %q want %q", got, msg)
	}
}

func TestStrictModeExactBoundary(t *testing.T) {
	// Complete 2-bit table; 4 symbols pack into exactly 1 byte.
	lengths := make([]int, 256)
	for _, s := range []byte{'a', 'b', 'c', 'd'} {
		lengths[s] = 2
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := NewTable(lengths)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abcd")
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	if len(stream) != 1 {
		t.Fatalf("want 1 byte, got %d", len(stream))
	}
	dec := NewDecoder(tbl, WithQuota(-1)) // strict: no symbol count
	out := make([]byte, 8)
	_, n, err := dec.Decode(stream, out, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(out[:n]) != string(msg) {
		t.Fatalf("got %q want %q", out[:n], msg)
	}
}

func TestLongCodesAcrossLevels(t *testing.T) {
	// Force codes longer than 16 bits to exercise 3 table levels.
	freqs := []uint64{1, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	lengths, err := BuildLengths(freqs, 20)
	if err != nil {
		t.Fatal(err)
	}
	max := 0
	for _, l := range lengths {
		if l > max {
			max = l
		}
	}
	if max <= 16 {
		t.Fatalf("test requires codes longer than 16 bits, got %d", max)
	}
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := NewTable(lengths)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(5))
	var msg []byte
	for i := 0; i < 2000; i++ {
		msg = append(msg, byte(rng.Intn(len(freqs))))
	}
	stream, err := Encode(msg, codes)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeAll(t, tbl, stream, len(msg), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, msg) {
		t.Fatal("long-code round trip mismatch")
	}
}
