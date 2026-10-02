package huffman

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"testing"
)

// encode compresses data, writing it in chunks of chunkSize to exercise
// streaming behavior.
func encode(t *testing.T, data []byte, chunkSize int) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		if _, err := enc.Write(data[:n]); err != nil {
			t.Fatalf("encoder write: %v", err)
		}
		data = data[n:]
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("encoder close: %v", err)
	}
	return buf.Bytes()
}

// decode decompresses data, reading into a buffer of readSize bytes.
func decode(t *testing.T, data []byte, readSize int) []byte {
	t.Helper()
	dec := NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	if _, err := io.CopyBuffer(&out, dec, make([]byte, readSize)); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Bytes()
}

func roundTrip(t *testing.T, data []byte, writeChunk, readChunk int) []byte {
	t.Helper()
	compressed := encode(t, data, writeChunk)
	got := decode(t, compressed, readChunk)
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(got), len(data))
	}
	return compressed
}

// makeSkewedText builds a long text with a heavily skewed byte
// distribution, similar to natural language.
func makeSkewedText(rng *rand.Rand, n int) []byte {
	alphabet := []byte("etaoin shrdlu etaoin shrdlu etaoin shrdlu,.\nABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return out
}

func TestRoundTripSkewedLongText(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	data := makeSkewedText(rng, 1<<20) // 1 MiB
	compressed := roundTrip(t, data, 4096, 4096)
	ratio := float64(len(compressed)) / float64(len(data))
	t.Logf("skewed text: %d -> %d bytes (ratio %.3f)", len(data), len(compressed), ratio)
	if ratio >= 0.7 {
		t.Fatalf("expected meaningful compression on skewed input, got ratio %.3f", ratio)
	}
}

func TestRoundTripAllByteValues(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 1<<16)
	rng.Read(data) // uniform random: every byte value occurs
	roundTrip(t, data, 1000, 777)
}

func TestRoundTripEmpty(t *testing.T) {
	roundTrip(t, nil, 1, 1)
}

func TestRoundTripSingleByte(t *testing.T) {
	roundTrip(t, []byte("x"), 1, 1)
}

func TestRoundTripRepeatedByte(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 100000)
	compressed := roundTrip(t, data, 1<<16, 1<<16)
	// A single distinct byte needs only 1 bit per occurrence plus the
	// initial NYT+literal and the trailing EOF symbol.
	if limit := len(data)/8 + 64; len(compressed) > limit {
		t.Fatalf("repeated byte should compress to ~1 bit/byte, got %d -> %d (limit %d)", len(data), len(compressed), limit)
	}
}

func TestRoundTripByteAtATime(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	data := makeSkewedText(rng, 5000)
	roundTrip(t, data, 1, 1) // extreme streaming: one byte per Write/Read
}

func TestRoundTripEvolvingDistribution(t *testing.T) {
	// Distribution shifts mid-stream; the adaptive tree must track it.
	var data []byte
	for i := 0; i < 26; i++ {
		data = append(data, bytes.Repeat([]byte{'a' + byte(i)}, 5000*(i+1))...)
	}
	roundTrip(t, data, 8192, 8192)
}

// validateSiblingProperty checks the FGK invariants: nodes listed by
// decreasing order number have non-increasing weights, and the two
// children of every internal node hold adjacent order numbers.
func validateSiblingProperty(t *tree) error {
	sorted := make([]*node, len(t.nodes))
	copy(sorted, t.nodes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].order > sorted[j].order })
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].weight < sorted[i].weight {
			return fmt.Errorf("weight inversion at order %d (%d) vs %d (%d)",
				sorted[i-1].order, sorted[i-1].weight, sorted[i].order, sorted[i].weight)
		}
	}
	for _, n := range t.nodes {
		if !n.isLeaf() {
			if n.left.order != n.right.order+1 && n.right.order != n.left.order+1 {
				return fmt.Errorf("siblings not adjacent: orders %d and %d", n.left.order, n.right.order)
			}
		}
	}
	return nil
}

func TestSiblingPropertyMaintained(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	tr := newTree()
	for i := 0; i < 200000; i++ {
		var symbol uint16
		if rng.Intn(100) < 90 {
			symbol = uint16(rng.Intn(8)) // hot symbols
		} else {
			symbol = uint16(rng.Intn(256))
		}
		tr.update(symbol)
		if i%997 == 0 {
			if err := validateSiblingProperty(tr); err != nil {
				t.Fatalf("sibling property violated after %d updates: %v", i, err)
			}
		}
	}
	if err := validateSiblingProperty(tr); err != nil {
		t.Fatalf("sibling property violated at end: %v", err)
	}
}

func TestEncoderDecoderTreesStayInSync(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := makeSkewedText(rng, 50000)

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}

	dec := NewDecoder(&buf)
	if _, err := io.ReadAll(dec); err != nil {
		t.Fatal(err)
	}

	// Both trees must have identical shape, weights and orders.
	en, dn := enc.t.nodes, dec.t.nodes
	if len(en) != len(dn) {
		t.Fatalf("node count differs: encoder %d, decoder %d", len(en), len(dn))
	}
	for i := range en {
		if en[i].weight != dn[i].weight || en[i].order != dn[i].order || en[i].symbol != dn[i].symbol {
			t.Fatalf("tree mismatch at node %d: enc{%d,%d,%d} dec{%d,%d,%d}",
				i, en[i].weight, en[i].order, en[i].symbol, dn[i].weight, dn[i].order, dn[i].symbol)
		}
	}
}

func TestConstantMemoryBound(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	data := make([]byte, 1<<20) // 1 MiB covering the whole alphabet
	rng.Read(data)
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(enc.t.nodes); got > maxNodes {
		t.Fatalf("tree grew to %d nodes, exceeds constant bound %d", got, maxNodes)
	}
	t.Logf("tree nodes after 1 MiB of random input: %d (bound %d)", len(enc.t.nodes), maxNodes)
}

func TestCorruptStreamReturnsError(t *testing.T) {
	dec := NewDecoder(bytes.NewReader([]byte{0xFF}))
	if _, err := io.ReadAll(dec); err == nil {
		t.Fatal("expected error decoding truncated stream")
	}
}
