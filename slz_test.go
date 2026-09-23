package slz

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"strings"
	"testing"
)

// bruteForceBits is an independent exponential-free DP over every possible
// parse (no hash index, no chain limit), used to validate the encoder's
// minimum-bit parser on short inputs.
func bruteForceBits(s []byte) int {
	n := len(s)
	cost := make([]int, n+1)
	for i := n - 1; i >= 0; i-- {
		best := LiteralCost + cost[i+1]
		start := i - WindowSize
		if start < 0 {
			start = 0
		}
		for j := start; j < i; j++ {
			l := 0
			for i+l < n && l < MaxMatch && s[j+l] == s[i+l] {
				l++
			}
			for ll := MinMatch; ll <= l; ll++ {
				if c := MatchCost + cost[i+ll]; c < best {
					best = c
				}
			}
		}
		cost[i] = best
	}
	return cost[0]
}

// TestOptimalParseExhaustive enumerates every string over {a,b,c} up to
// length 9 and checks the encoder's parse cost against brute force, plus
// a full encode/decode round trip.
func TestOptimalParseExhaustive(t *testing.T) {
	alpha := []byte("abc")
	maxLen := 9
	total := 0
	var walk func(s []byte)
	walk = func(s []byte) {
		total++
		if got, want := OptimalBits(s), bruteForceBits(s); got != want {
			t.Fatalf("OptimalBits(%q) = %d, brute force = %d", s, got, want)
		}
		dec, err := Decompress(Compress(s), 1<<20)
		if err != nil {
			t.Fatalf("Decompress(Compress(%q)): %v", s, err)
		}
		if !bytes.Equal(dec, s) {
			t.Fatalf("round trip of %q gave %q", s, dec)
		}
		if len(s) < maxLen {
			for _, c := range alpha {
				walk(append(append([]byte{}, s...), c))
			}
		}
	}
	walk(nil)
	t.Logf("checked %d strings", total)
}

// TestGreedyCounterexample is a concrete string where the greedy
// longest-match parser emits strictly more bits than the optimal parse.
//
// "abcxbcdefgabcdefg": at offset 10 greedy takes match("abc",len3)=19 bits
// then match("defg",len4)=19 bits; optimal emits literal 'a' (9 bits) and
// then match("bcdefg",len6)=19 bits.
func TestGreedyCounterexample(t *testing.T) {
	s := []byte("abcxbcdefgabcdefg")
	greedy := GreedyBits(s)
	optimal := OptimalBits(s)
	brute := bruteForceBits(s)
	if optimal != brute {
		t.Fatalf("optimal parse = %d bits, brute force = %d", optimal, brute)
	}
	if greedy <= optimal {
		t.Fatalf("expected greedy to lose: greedy=%d optimal=%d", greedy, optimal)
	}
	t.Logf("greedy=%d bits, optimal=%d bits (saved %d)", greedy, optimal, greedy-optimal)
}

func TestDistanceOneRepeat(t *testing.T) {
	for _, n := range []int{1, 2, 3, 10, 100, 5000} {
		data := []byte(strings.Repeat("a", n))
		dec, err := Decompress(Compress(data), n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(dec, data) {
			t.Fatalf("n=%d: round trip mismatch", n)
		}
	}
	// A 5000-byte run of 'a' must compress to far less than the input.
	data := []byte(strings.Repeat("a", 5000))
	if enc := Compress(data); len(enc) > 2000 {
		t.Fatalf("run of 5000 'a' encoded to %d bytes", len(enc))
	}
}

func TestEmpty(t *testing.T) {
	dec, err := Decompress(Compress(nil), 0)
	if err != nil || len(dec) != 0 {
		t.Fatalf("empty round trip: %v %q", err, dec)
	}
}

func TestRandomChunkedRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	words := [][]byte{[]byte("hello"), []byte("world"), []byte("lz77"),
		[]byte("window"), []byte("match"), []byte("abc")}
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(3000)
		data := make([]byte, 0, n)
		for len(data) < n {
			switch rng.Intn(4) {
			case 0:
				data = append(data, byte(rng.Intn(256)))
			case 1:
				data = append(data, words[rng.Intn(len(words))]...)
			default:
				if len(data) == 0 {
					continue
				}
				start := rng.Intn(len(data))
				l := 1 + rng.Intn(80)
				if start+l > len(data) {
					l = len(data) - start
				}
				data = append(data, data[start:start+l]...)
			}
		}
		var sizes []int
		for rem := len(data); rem > 0; {
			s := 1 + rng.Intn(rem)
			sizes = append(sizes, s)
			rem -= s
		}
		for _, resetEach := range []bool{true, false} {
			enc := CompressBlocks(data, sizes, resetEach)
			dec, err := Decompress(enc, len(data))
			if err != nil {
				t.Fatalf("trial %d resetEach=%v: %v", trial, resetEach, err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("trial %d resetEach=%v: mismatch", trial, resetEach)
			}
		}
	}
}

func TestWindowEvictionLongRange(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	prefix := make([]byte, WindowSize+1000)
	for i := range prefix {
		prefix[i] = byte(rng.Intn(256))
	}
	// Repeat of the first 100 bytes at distance > WindowSize: the encoder
	// must not (and cannot) reference it; round trip must still hold.
	data := append(append([]byte{}, prefix...), prefix[:100]...)
	dec, err := Decompress(Compress(data), len(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatal("mismatch")
	}
}

// testBlock describes one hand-crafted block.
type testBlock struct {
	rawLen int
	reset  bool
	emit   func(w *bitWriter)
}

func buildStream(totalLen int, crc uint32, blocks []testBlock) []byte {
	out := make([]byte, 0, 64)
	out = append(out, Magic...)
	out = append(out, Version, 0)
	var u [8]byte
	binary.LittleEndian.PutUint64(u[:], uint64(totalLen))
	out = append(out, u[:]...)
	binary.LittleEndian.PutUint32(u[:4], crc)
	out = append(out, u[:4]...)
	for _, b := range blocks {
		w := &bitWriter{}
		b.emit(w)
		bitLen := w.bits()
		payload := w.finish()
		binary.LittleEndian.PutUint32(u[:4], uint32(b.rawLen))
		out = append(out, u[:4]...)
		binary.LittleEndian.PutUint32(u[:4], uint32(bitLen))
		out = append(out, u[:4]...)
		flags := byte(0)
		if b.reset {
			flags |= flagResetWindow
		}
		out = append(out, flags)
		out = append(out, payload...)
	}
	return out
}

func emitLiteral(w *bitWriter, b byte) {
	w.writeBits(0, 1)
	w.writeBits(uint32(b), 8)
}

func emitMatch(w *bitWriter, dist, length int) {
	w.writeBits(1, 1)
	w.writeBits(uint32(dist-1), WindowBits)
	w.writeBits(uint32(length-MinMatch), LenBits)
}

func TestRejectFutureReference(t *testing.T) {
	// After one literal, a match with dist=2 references a byte that has
	// not been produced yet.
	s := buildStream(4, 0, []testBlock{{
		rawLen: 4,
		reset:  true,
		emit: func(w *bitWriter) {
			emitLiteral(w, 'a')
			emitMatch(w, 2, 3)
		},
	}})
	if _, err := Decompress(s, 1<<20); err != errBadDistance {
		t.Fatalf("got %v, want errBadDistance", err)
	}
}

func TestRejectReferenceBeforeCheckpoint(t *testing.T) {
	// Block 2 resets the window, so its match may not reach into block 1.
	s := buildStream(7, 0, []testBlock{
		{rawLen: 4, reset: true, emit: func(w *bitWriter) {
			for _, c := range "abcd" {
				emitLiteral(w, byte(c))
			}
		}},
		{rawLen: 3, reset: true, emit: func(w *bitWriter) {
			emitMatch(w, 3, 3)
		}},
	})
	if _, err := Decompress(s, 1<<20); err != errBadDistance {
		t.Fatalf("got %v, want errBadDistance", err)
	}
}

func TestCrossBlockWindowContinues(t *testing.T) {
	// Without a reset flag, block 2 may reference block 1 within the window.
	want := []byte("abcdbcd")
	s := buildStream(len(want), crc32.ChecksumIEEE(want), []testBlock{
		{rawLen: 4, reset: true, emit: func(w *bitWriter) {
			for _, c := range "abcd" {
				emitLiteral(w, byte(c))
			}
		}},
		{rawLen: 3, reset: false, emit: func(w *bitWriter) {
			emitMatch(w, 3, 3)
		}},
	})
	dec, err := Decompress(s, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, want) {
		t.Fatalf("got %q, want %q", dec, want)
	}
}

func TestOverlapCopyExtends(t *testing.T) {
	// dist=1, len=5 after one literal must extend the run: "a" -> "aaaaaa".
	want := []byte("aaaaaa")
	s := buildStream(len(want), crc32.ChecksumIEEE(want), []testBlock{
		{rawLen: 6, reset: true, emit: func(w *bitWriter) {
			emitLiteral(w, 'a')
			emitMatch(w, 1, 5)
		}},
	})
	dec, err := Decompress(s, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, want) {
		t.Fatalf("got %q, want %q", dec, want)
	}
}

func TestOutputBudget(t *testing.T) {
	data := []byte(strings.Repeat("budget", 50))
	enc := Compress(data)
	if _, err := Decompress(enc, len(data)-1); err != errTooLarge {
		t.Fatalf("got %v, want errTooLarge", err)
	}
	if _, err := Decompress(enc, len(data)); err != nil {
		t.Fatalf("exact budget should succeed: %v", err)
	}
}

func TestCorruptions(t *testing.T) {
	data := []byte("hello hello hello hello")
	good := Compress(data)

	bad := append([]byte{}, good...)
	bad[0] = 'X'
	if _, err := Decompress(bad, 1<<20); err != errCorrupt {
		t.Fatalf("bad magic: %v", err)
	}

	bad = append([]byte{}, good...)
	bad[4] = Version + 1
	if _, err := Decompress(bad, 1<<20); err != errBadVersion {
		t.Fatalf("bad version: %v", err)
	}

	bad = append([]byte{}, good...)
	bad[14] ^= 0xff // crc field
	if _, err := Decompress(bad, 1<<20); err != errChecksum {
		t.Fatalf("bad crc: %v", err)
	}

	bad = append([]byte{}, good...)
	binary.LittleEndian.PutUint64(bad[6:14], uint64(len(data)+1))
	if _, err := Decompress(bad, 1<<20); err != errBadLength {
		t.Fatalf("bad length: %v", err)
	}

	for _, cut := range []int{0, 3, headerSize - 1, headerSize, len(good) - 1} {
		if _, err := Decompress(good[:cut], 1<<20); err == nil {
			t.Fatalf("truncated to %d bytes: expected error", cut)
		}
	}
}
