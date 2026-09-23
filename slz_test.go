package slz

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"strings"
	"testing"
)

func roundtrip(t *testing.T, data []byte, chunkSize int) {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, data, chunkSize); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(&buf, len(data)+1)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("roundtrip mismatch: got %d bytes, want %d", len(got), len(data))
	}
}

func TestRoundtripBasic(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	cases := map[string][]byte{
		"empty":        {},
		"one":          {'x'},
		"two":          {'a', 'b'},
		"hello":        []byte("hello world"),
		"run":          []byte(strings.Repeat("a", 1000)),
		"phrase":       []byte(strings.Repeat("the quick brown fox. ", 200)),
		"all-bytes":    allBytes,
		"window-cross": append(bytes.Repeat([]byte("0123456789"), 500), bytes.Repeat([]byte("0123456789"), 500)...),
	}
	for name, data := range cases {
		for _, cs := range []int{1, 7, 64, 4096} {
			t.Run(name, func(t *testing.T) { roundtrip(t, data, cs) })
		}
	}
}

func TestDistanceOneRepeatExtends(t *testing.T) {
	data := []byte(strings.Repeat("z", 500))
	var buf bytes.Buffer
	if err := Encode(&buf, data, 4096); err != nil {
		t.Fatal(err)
	}
	toks := optimalParse(data)
	hasDist1 := false
	for _, tk := range toks {
		if tk.isMatch && tk.dist == 1 {
			hasDist1 = true
			if tk.length > 500 {
				t.Fatalf("bogus match length %d", tk.length)
			}
		}
	}
	if !hasDist1 {
		t.Fatal("expected a distance-1 run-extension match")
	}
	got, err := Decode(&buf, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("distance-1 overlap copy expanded incorrectly")
	}
}

// bruteForceMinCost enumerates every valid parse of data (any literal /
// any in-window match of any length) and returns the minimum bit cost.
func bruteForceMinCost(data []byte) int {
	n := len(data)
	memo := make([]int, n+1)
	for i := range memo {
		memo[i] = -1
	}
	var f func(i int) int
	f = func(i int) int {
		if i == n {
			return 0
		}
		if memo[i] >= 0 {
			return memo[i]
		}
		best := literalCost + f(i+1)
		for d := 1; d <= i && d <= WindowSize; d++ {
			lim := n - i
			if lim > MaxMatch {
				lim = MaxMatch
			}
			maxL := 0
			for maxL < lim && data[i-d+maxL] == data[i+maxL] {
				maxL++
			}
			for l := MinMatch; l <= maxL; l++ {
				if c := matchCost(l, d) + f(i+l); c < best {
					best = c
				}
			}
		}
		memo[i] = best
		return best
	}
	return f(0)
}

// TestExhaustiveShortStrings proves the DP parse attains the true minimum
// bit cost for every short string over small alphabets, and never loses
// to greedy.
func TestExhaustiveShortStrings(t *testing.T) {
	check := func(data []byte) {
		opt := parseCost(optimalParse(data))
		if bf := bruteForceMinCost(data); opt != bf {
			t.Fatalf("optimal parse cost %d != brute-force minimum %d for %q", opt, bf, data)
		}
		if g := parseCost(greedyParse(data)); opt > g {
			t.Fatalf("optimal parse cost %d > greedy cost %d for %q", opt, g, data)
		}
	}
	var enum func(prefix []byte, depth int, alphabet []byte)
	enum = func(prefix []byte, depth int, alphabet []byte) {
		if depth == 0 {
			check(prefix)
			return
		}
		for _, b := range alphabet {
			enum(append(append([]byte{}, prefix...), b), depth-1, alphabet)
		}
	}
	for l := 0; l <= 12; l++ {
		enum(nil, l, []byte("ab"))
	}
	for l := 0; l <= 8; l++ {
		enum(nil, l, []byte("abc"))
	}
}

// TestGreedyCounterexample is a concrete case where longest-match greedy
// is strictly worse than the minimum-bit parse: greedy grabs a length-5
// match at a huge distance (expensive gamma-coded distance) while the
// optimal parse takes a length-4 match at distance 4 plus one literal.
func TestGreedyCounterexample(t *testing.T) {
	data := []byte("abcde")
	filler := make([]byte, 2000)
	for i := range filler {
		filler[i] = byte(0x80 + (i*7)%0x40) // no 'a'..'e' bytes
	}
	data = append(data, filler...)
	data = append(data, []byte("abcdabcde")...)

	opt := parseCost(optimalParse(data))
	gr := parseCost(greedyParse(data))
	t.Logf("optimal=%d bits, greedy=%d bits", opt, gr)
	if opt >= gr {
		t.Fatalf("expected optimal < greedy, got optimal=%d greedy=%d", opt, gr)
	}
}

func TestRandomChunkedRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 60; iter++ {
		n := rng.Intn(6000)
		var data []byte
		for len(data) < n {
			if len(data) > 100 && rng.Intn(2) == 0 {
				l := 1 + rng.Intn(200)
				d := 1 + rng.Intn(len(data))
				for k := 0; k < l && len(data) < n; k++ {
					data = append(data, data[len(data)-d])
				}
			} else {
				data = append(data, byte(rng.Intn(256)))
			}
		}
		cs := []int{1, 3, 17, 256, 4096}[rng.Intn(5)]
		roundtrip(t, data, cs)
		for _, tk := range optimalParse(data) {
			if tk.isMatch && (tk.dist < 1 || tk.dist > WindowSize) {
				t.Fatalf("encoder emitted out-of-window distance %d", tk.dist)
			}
		}
	}
}

// buildStream assembles a raw stream from hand-crafted chunks.
func buildStream(totalLen int, crc uint32, chunks [][]token, checkpoints []uint32) []byte {
	var buf bytes.Buffer
	hdr := append([]byte{}, magic...)
	hdr = append(hdr, Version)
	hdr = binary.AppendUvarint(hdr, uint64(totalLen))
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc)
	hdr = append(hdr, c[:]...)
	buf.Write(hdr)
	for i, toks := range chunks {
		binary.BigEndian.PutUint32(c[:], checkpoints[i])
		buf.Write(c[:])
		buf.Write(binary.AppendUvarint(nil, uint64(len(toks))))
		bw := &bitWriter{}
		for _, tk := range toks {
			if tk.isMatch {
				bw.writeBit(1)
				bw.writeGamma(tk.length - MinMatch + 1)
				bw.writeGamma(tk.dist)
			} else {
				bw.writeBit(0)
				bw.writeBits(uint32(tk.lit), 8)
			}
		}
		bw.flush()
		buf.Write(bw.buf)
	}
	return buf.Bytes()
}

func expectDecodeErr(t *testing.T, stream []byte, maxOut int, want string) {
	t.Helper()
	_, err := Decode(bytes.NewReader(stream), maxOut)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestRejectNotYetProduced(t *testing.T) {
	// First token is a match: distance 1 with zero bytes produced.

	s := buildStream(10, 0,
		[][]token{{{isMatch: true, length: 3, dist: 1}}},
		[]uint32{crc32.ChecksumIEEE(nil)})
	expectDecodeErr(t, s, 100, "not-yet-produced")

	// Ten literals produced, then a match at distance 100.
	toks := make([]token, 0, 11)
	for i := 0; i < 10; i++ {
		toks = append(toks, token{lit: 'a'})
	}
	toks = append(toks, token{isMatch: true, length: 3, dist: 100})
	s = buildStream(13, 0, [][]token{toks}, []uint32{crc32.ChecksumIEEE(nil)})
	expectDecodeErr(t, s, 100, "not-yet-produced")
}

func TestRejectEvictedWindowDistance(t *testing.T) {
	// Produce WindowSize literals, then reference distance WindowSize+1.
	toks := make([]token, 0, WindowSize+1)
	for i := 0; i < WindowSize; i++ {
		toks = append(toks, token{lit: byte(i)})
	}
	toks = append(toks, token{isMatch: true, length: 3, dist: WindowSize + 1})
	s := buildStream(WindowSize+3, 0, [][]token{toks}, []uint32{crc32.ChecksumIEEE(nil)})
	expectDecodeErr(t, s, 2*WindowSize, "evicted window")
}

func TestRejectBadCheckpoint(t *testing.T) {
	// Chunk 2's checkpoint does not match the dictionary after chunk 1.
	s := buildStream(2, crc32.ChecksumIEEE([]byte("AB")),
		[][]token{{{lit: 'A'}}, {{lit: 'B'}}},
		[]uint32{crc32.ChecksumIEEE(nil), 0xdeadbeef})
	expectDecodeErr(t, s, 100, "checkpoint mismatch")
}

func TestRejectOverrunDeclaredLength(t *testing.T) {
	s := buildStream(1, 0,
		[][]token{{{lit: 'A'}, {lit: 'B'}}},
		[]uint32{crc32.ChecksumIEEE(nil)})
	expectDecodeErr(t, s, 100, "overruns declared length")
}

func TestRejectBadVersionAndMagic(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, []byte("hello"), 64); err != nil {
		t.Fatal(err)
	}
	bad := buf.Bytes()
	bad[3] = 99
	expectDecodeErr(t, bad, 100, "unsupported version")
	bad2 := buf.Bytes()
	bad2[0] = 'X'
	expectDecodeErr(t, bad2, 100, "bad magic")
}

func TestChecksumMismatch(t *testing.T) {
	data := []byte(strings.Repeat("some payload bytes ", 50))
	var buf bytes.Buffer
	if err := Encode(&buf, data, 1<<20); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	raw[len(raw)-1] ^= 0x40 // corrupt a literal in the final chunk
	expectDecodeErr(t, raw, len(data)+1, "checksum mismatch")
}

func TestOutputLimit(t *testing.T) {
	data := []byte(strings.Repeat("abcabcabc", 100))
	var buf bytes.Buffer
	if err := Encode(&buf, data, 4096); err != nil {
		t.Fatal(err)
	}
	expectDecodeErr(t, buf.Bytes(), len(data)-1, "exceeds output limit")
	if _, err := Decode(&buf, len(data)); err != nil {
		t.Fatalf("decode at exact limit failed: %v", err)
	}
}

func TestGammaRoundtrip(t *testing.T) {
	bw := &bitWriter{}
	for v := 1; v <= 100000; v = v*3 + 1 {
		bw.writeGamma(v)
	}
	bw.flush()
	br := &bitReader{data: bw.buf}
	for v := 1; v <= 100000; v = v*3 + 1 {
		got, err := br.readGamma()
		if err != nil || got != v {
			t.Fatalf("gamma roundtrip: got %d, %v; want %d", got, err, v)
		}
	}
}
