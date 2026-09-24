package llex

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

// exactBlockCost is the independent test-side block cost: optimal table
// (SolveLengths) + header + count field + data bytes.
func exactBlockCost(alphabet []string, toks []uint32, L int) (int, bool) {
	freq := map[string]uint64{}
	for _, t := range toks {
		freq[alphabet[t]]++
	}
	syms := make([]Symbol, 0, len(freq))
	for id, f := range freq {
		syms = append(syms, Symbol{ID: id, Freq: f})
	}
	tab, err := SolveLengths(syms, L)
	if err != nil {
		return 0, false
	}
	hdr, err := MarshalTable(tab)
	if err != nil {
		return 0, false
	}
	var bits uint64
	for _, t := range toks {
		bits += uint64(tab.Lengths[indexOf(tab.IDs, alphabet[t])])
	}
	return len(hdr) + 4 + int((bits+7)/8), true
}

// bruteChunk enumerates every legal partition of the token sequence and
// returns the minimum total file size, index overhead included.
func bruteChunk(alphabet []string, tokens []uint32, L, maxBlocks, maxTokens int) int {
	best := -1
	var rec func(pos, blocks, acc int)
	rec = func(pos, blocks, acc int) {
		if pos == len(tokens) {
			if best < 0 || acc < best {
				best = acc
			}
			return
		}
		if blocks == maxBlocks {
			return
		}
		for j := pos + 1; j <= len(tokens) && j-pos <= maxTokens; j++ {
			c, ok := exactBlockCost(alphabet, tokens[pos:j], L)
			if !ok {
				continue
			}
			rec(j, blocks+1, acc+indexEntryLen+c)
		}
	}
	rec(0, 0, 0)
	if best < 0 {
		return -1
	}
	return fileFixedLen + best
}

func decodeAll(t *testing.T, file []byte) []string {
	t.Helper()
	out, err := DecodeFile(file, 1<<20, -1)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	return out
}

func tokensToIDs(alphabet []string, tokens []uint32) []string {
	out := make([]string, len(tokens))
	for i, tok := range tokens {
		out[i] = alphabet[tok]
	}
	return out
}

// Second verification layer: for short sequences the DP layout must match
// exhaustive enumeration of all candidate partitions, file overhead
// (index + headers + data + alignment) included.
func TestChunkedAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	alphabet := []string{"a", "bb", "ccc"}
	for trial := 0; trial < 150; trial++ {
		n := 1 + rng.Intn(9) // 1..9 tokens
		L := 2 + rng.Intn(2) // 2..3
		M := 2 + rng.Intn(3) // 2..4 tokens per block
		K := 2 + rng.Intn(3) // 2..4 blocks
		if K*M < n {
			continue
		}
		tokens := make([]uint32, n)
		for i := range tokens {
			tokens[i] = uint32(rng.Intn(len(alphabet)))
		}
		res, err := EncodeChunked(alphabet, tokens, ChunkConfig{L: L, MaxBlocks: K, MaxBlockTokens: M})
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if !res.Optimal {
			t.Fatalf("trial %d: unlimited budget must prove optimality", trial)
		}
		want := bruteChunk(alphabet, tokens, L, K, M)
		if want < 0 {
			t.Fatalf("trial %d: brute force found no layout", trial)
		}
		if res.Bytes != want {
			t.Fatalf("trial %d: n=%d L=%d M=%d K=%d tokens=%v: DP=%d bytes, brute force=%d",
				trial, n, L, M, K, tokens, res.Bytes, want)
		}
		got := decodeAll(t, res.File)
		wantIDs := tokensToIDs(alphabet, tokens)
		if len(got) != len(wantIDs) {
			t.Fatalf("trial %d: decoded %d symbols, want %d", trial, len(got), len(wantIDs))
		}
		for i := range got {
			if got[i] != wantIDs[i] {
				t.Fatalf("trial %d: position %d: got %q want %q", trial, i, got[i], wantIDs[i])
			}
		}
	}
}

// greedyChunkCost picks the locally cheapest next block at every step.
func greedyChunkCost(alphabet []string, tokens []uint32, L, M int) int {
	total := fileFixedLen
	for pos := 0; pos < len(tokens); {
		bestJ, bestC := -1, 0
		for j := pos + 1; j <= len(tokens) && j-pos <= M; j++ {
			c, ok := exactBlockCost(alphabet, tokens[pos:j], L)
			if !ok {
				continue
			}
			if bestJ < 0 || c < bestC {
				bestJ, bestC = j, c
			}
		}
		if bestJ < 0 {
			return -1
		}
		total += indexEntryLen + bestC
		pos = bestJ
	}
	return total
}

// A concrete case where locally optimal blocks are globally wrong: every
// single-symbol block is cheap, but seven of them plus headers cost far
// more than one shared table.
func TestGreedyChunkingFails(t *testing.T) {
	alphabet := []string{"A", "B"}
	tokens := []uint32{0, 0, 0, 0, 0, 0, 0, 1}
	cfg := ChunkConfig{L: 4, MaxBlocks: 8, MaxBlockTokens: 8}
	res, err := EncodeChunked(alphabet, tokens, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Optimal {
		t.Fatal("layout must be proven optimal")
	}
	greedy := greedyChunkCost(alphabet, tokens, cfg.L, cfg.MaxBlockTokens)
	if greedy <= res.Bytes {
		t.Fatalf("expected greedy (%d) to lose to DP (%d)", greedy, res.Bytes)
	}
	if res.Blocks != 1 {
		t.Fatalf("optimal layout is a single block, got %d", res.Blocks)
	}
	if want := bruteChunk(alphabet, tokens, cfg.L, cfg.MaxBlocks, cfg.MaxBlockTokens); res.Bytes != want {
		t.Fatalf("DP=%d, brute force=%d", res.Bytes, want)
	}
}

// With a tiny evaluation budget the encoder must keep a feasible file and
// a reliable lower bound, and must not claim optimality.
func TestBudgetKeepsFeasibleAndBound(t *testing.T) {
	alphabet := []string{"A", "B"}
	tokens := []uint32{0, 0, 0, 0, 0, 0, 0, 1}
	cfg := ChunkConfig{L: 4, MaxBlocks: 8, MaxBlockTokens: 8, EvalBudget: 1}
	res, err := EncodeChunked(alphabet, tokens, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Optimal {
		t.Fatal("budget 1 must not claim optimality")
	}
	optimum := bruteChunk(alphabet, tokens, cfg.L, cfg.MaxBlocks, cfg.MaxBlockTokens)
	if res.Feasible < optimum {
		t.Fatalf("feasible %d below true optimum %d: impossible", res.Feasible, optimum)
	}
	if res.LowerBound > optimum {
		t.Fatalf("lower bound %d exceeds true optimum %d: bound is unsound", res.LowerBound, optimum)
	}
	if res.LowerBound > res.Feasible {
		t.Fatalf("lower bound %d exceeds feasible %d", res.LowerBound, res.Feasible)
	}
	got := decodeAll(t, res.File)
	if len(got) != len(tokens) {
		t.Fatalf("feasible file decodes %d symbols, want %d", len(got), len(tokens))
	}
	for i, g := range got {
		if g != alphabet[tokens[i]] {
			t.Fatalf("position %d: got %q", i, g)
		}
	}
}

func TestRandomBlockAccess(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []string{"x", "y", "z", "w"}
	tokens := make([]uint32, 500)
	for i := range tokens {
		tokens[i] = uint32(rng.Intn(4))
	}
	res, err := EncodeChunked(alphabet, tokens, ChunkConfig{L: 4, MaxBlocks: 20, MaxBlockTokens: 37})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := ParseFile(res.File, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var joined []string
	for b := range entries {
		syms, err := DecodeFileBlock(res.File, entries, b, -1)
		if err != nil {
			t.Fatalf("block %d: %v", b, err)
		}
		joined = append(joined, syms...)
	}
	whole := decodeAll(t, res.File)
	if len(joined) != len(whole) {
		t.Fatalf("random access gave %d symbols, whole-file gave %d", len(joined), len(whole))
	}
	for i := range joined {
		if joined[i] != whole[i] {
			t.Fatalf("position %d differs: %q vs %q", i, joined[i], whole[i])
		}
	}
}

func TestEmptyInput(t *testing.T) {
	res, err := EncodeChunked([]string{"a"}, nil, ChunkConfig{L: 4, MaxBlocks: 4, MaxBlockTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Optimal || res.Blocks != 0 {
		t.Fatalf("empty input: %+v", res)
	}
	if out := decodeAll(t, res.File); len(out) != 0 {
		t.Fatalf("empty input must decode to empty, got %v", out)
	}
}

func TestInfeasibleChunkConfig(t *testing.T) {
	_, err := EncodeChunked([]string{"a"}, make([]uint32, 10), ChunkConfig{L: 4, MaxBlocks: 2, MaxBlockTokens: 3})
	if err == nil {
		t.Fatal("10 tokens cannot fit in 2 blocks of 3")
	}
}

// fixCRC recomputes the file index checksum after tampering, so the
// structural validators (not the checksum) are what rejects the file.
func fixCRC(data []byte) {
	nb := int(binary.LittleEndian.Uint32(data[6:10]))
	end := 10 + indexEntryLen*nb
	binary.LittleEndian.PutUint32(data[end:end+4], crc32.ChecksumIEEE(data[:end]))
}

func TestTamperedIndexRejected(t *testing.T) {
	alphabet := []string{"a", "b"}
	tokens := []uint32{0, 1, 0, 1, 0, 1, 0, 1}
	res, err := EncodeChunked(alphabet, tokens, ChunkConfig{L: 2, MaxBlocks: 2, MaxBlockTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocks < 2 {
		t.Fatalf("need >= 2 blocks for this test, got %d", res.Blocks)
	}
	nb := res.Blocks

	corrupt := func(mutate func(d []byte), fixChecksum bool) []byte {
		d := append([]byte(nil), res.File...)
		mutate(d)
		if fixChecksum {
			fixCRC(d)
		}
		return d
	}
	mustFail := func(name string, d []byte) {
		t.Helper()
		if _, err := ParseFile(d, 1<<20); err == nil {
			t.Fatalf("%s: tampered file accepted", name)
		}
		if _, err := DecodeFile(d, 1<<20, -1); err == nil {
			t.Fatalf("%s: tampered file decoded", name)
		}
	}

	entry1 := 10 + indexEntryLen // second index entry

	mustFail("checksum", corrupt(func(d []byte) { d[entry1] ^= 0x01 }, false))
	mustFail("offset beyond file", corrupt(func(d []byte) {
		binary.LittleEndian.PutUint64(d[entry1:entry1+8], 1<<40)
	}, true))
	mustFail("overlapping offset", corrupt(func(d []byte) {
		binary.LittleEndian.PutUint64(d[entry1:entry1+8], 5)
	}, true))
	mustFail("zero block length", corrupt(func(d []byte) {
		binary.LittleEndian.PutUint32(d[entry1+8:entry1+12], 0)
	}, true))
	mustFail("huge symbol count", corrupt(func(d []byte) {
		binary.LittleEndian.PutUint32(d[entry1+12:entry1+16], 0xffffffff)
	}, true))
	mustFail("huge block count", corrupt(func(d []byte) {
		binary.LittleEndian.PutUint32(d[6:10], 0x00ffffff)
	}, false))
	mustFail("truncated file", res.File[:len(res.File)-3])
	_ = nb
}

// The streaming decoder must produce identical output no matter how the
// input is sliced or how often the consumer pauses it.
func TestStreamDecoderSlicingAndBackpressure(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	alphabet := []string{"a", "b", "c", "d"}
	tokens := make([]uint32, 300)
	for i := range tokens {
		tokens[i] = uint32(rng.Intn(4))
	}
	res, err := EncodeChunked(alphabet, tokens, ChunkConfig{L: 4, MaxBlocks: 16, MaxBlockTokens: 29})
	if err != nil {
		t.Fatal(err)
	}
	want := tokensToIDs(alphabet, tokens)

	for _, seed := range []int64{1, 2, 3} {
		r := rand.New(rand.NewSource(seed))
		var got []string
		sd := NewStreamDecoder(1<<20, len(tokens))
		emit := func(s string) bool {
			if r.Intn(7) == 0 {
				return false // backpressure: do not consume
			}
			got = append(got, s)
			return true
		}
		for pos := 0; pos < len(res.File); {
			step := 1 + r.Intn(11)
			if pos+step > len(res.File) {
				step = len(res.File) - pos
			}
			if err := sd.Write(res.File[pos:pos+step], emit); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			pos += step
		}
		for spins := 0; len(got) < len(want) && spins < 1<<20; spins++ {
			if err := sd.Write(nil, emit); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("seed %d: got %d symbols, want %d", seed, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("seed %d: position %d: got %q want %q", seed, i, got[i], want[i])
			}
		}
	}
}

func TestStreamDecoderRejectsTrailingGarbage(t *testing.T) {
	res, err := EncodeChunked([]string{"a"}, []uint32{0, 0}, ChunkConfig{L: 2, MaxBlocks: 1, MaxBlockTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	sd := NewStreamDecoder(1<<20, -1)
	bad := append(append([]byte(nil), res.File...), 0x00)
	if err := sd.Write(bad, func(string) bool { return true }); err == nil {
		t.Fatal("trailing garbage must be rejected")
	}
}
