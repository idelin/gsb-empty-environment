package adaptivehuffman

import (
	"bytes"
	"io"
	"math/rand"
	"sort"
	"testing"
)

// skewedText returns a deterministic long text with a heavily skewed
// byte distribution (a few symbols dominate, a long tail is rare).
func skewedText(n int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	const alphabet = "etaoinshrdlucmfwypvbgkqjxzETAOINSHRDLU0123456789 .,;:!?-\n"
	weights := make([]int, len(alphabet))
	total := 0
	for i := range weights {
		weights[i] = 1 << max(0, 12-i/3) // geometric decay
		total += weights[i]
	}
	out := make([]byte, n)
	for i := range out {
		x := r.Intn(total)
		for j, w := range weights {
			if x < w {
				out[i] = alphabet[j]
				break
			}
			x -= w
		}
	}
	return out
}

func encodeAll(t *testing.T, data []byte, chunk int) (*Encoder, []byte) {
	t.Helper()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		if _, err := enc.Write(data[i:end]); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return enc, buf.Bytes()
}

func decodeAll(t *testing.T, compressed []byte, chunk int) (*Decoder, []byte) {
	t.Helper()
	dec := NewDecoder(bytes.NewReader(compressed))
	var out []byte
	p := make([]byte, chunk)
	for {
		n, err := dec.Read(p)
		out = append(out, p[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	return dec, out
}

func roundTrip(t *testing.T, data []byte, wChunk, rChunk int) (*Encoder, *Decoder, []byte, []byte) {
	t.Helper()
	enc, compressed := encodeAll(t, data, wChunk)
	dec, plain := decodeAll(t, compressed, rChunk)
	if !bytes.Equal(plain, data) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(plain), len(data))
	}
	return enc, dec, compressed, plain
}

// checkSiblingProperty verifies the FGK invariants on a tree:
// nodes enumerated by decreasing order number have non-increasing
// weights, siblings occupy adjacent order numbers, and every internal
// weight equals the sum of its children.
func checkSiblingProperty(t *testing.T, tr *tree) {
	t.Helper()
	nodes := make([]*node, len(tr.all))
	copy(nodes, tr.all)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].order > nodes[j].order })
	for i := 1; i < len(nodes); i++ {
		if nodes[i].weight > nodes[i-1].weight {
			t.Fatalf("sibling property violated: order %d (w=%d) before order %d (w=%d)",
				nodes[i-1].order, nodes[i-1].weight, nodes[i].order, nodes[i].weight)
		}
	}
	for _, n := range tr.all {
		if n.isLeaf() {
			continue
		}
		if n.right.order != n.left.order+1 {
			t.Fatalf("siblings not adjacent: left order %d, right order %d",
				n.left.order, n.right.order)
		}
		if n.weight != n.left.weight+n.right.weight {
			t.Fatalf("weight mismatch: node %d has %d, children sum to %d",
				n.order, n.weight, n.left.weight+n.right.weight)
		}
	}
}

func TestRoundTripSkewedLongText(t *testing.T) {
	data := skewedText(1<<20, 42) // 1 MiB, heavily skewed
	enc, _, compressed, _ := roundTrip(t, data, 4096, 4096)
	ratio := float64(len(compressed)) / float64(len(data))
	t.Logf("input=%d bytes compressed=%d bytes ratio=%.3f treeNodes=%d",
		len(data), len(compressed), ratio, enc.TreeSize())
	if ratio >= 0.65 {
		t.Fatalf("poor compression on skewed input: ratio %.3f", ratio)
	}
}

func TestStreamingChunks(t *testing.T) {
	data := skewedText(100000, 7)
	// Odd chunk sizes on both sides stress the bit-level streaming.
	roundTrip(t, data, 1, 1)
	roundTrip(t, data, 7, 13)
	roundTrip(t, data, 65536, 3)
}

func TestSiblingPropertyMaintained(t *testing.T) {
	data := skewedText(20000, 99)
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	for i, b := range data {
		if _, err := enc.Write([]byte{b}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		checkSiblingProperty(t, enc.t)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestDecoderTreeMirrorsEncoder(t *testing.T) {
	data := skewedText(50000, 1234)
	enc, dec, _, _ := roundTrip(t, data, 1024, 1024)
	ser := func(tr *tree) [][2]uint64 {
		nodes := make([]*node, len(tr.all))
		copy(nodes, tr.all)
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].order > nodes[j].order })
		out := make([][2]uint64, len(nodes))
		for i, n := range nodes {
			out[i] = [2]uint64{uint64(int64(n.symbol) + 2), n.weight}
		}
		return out
	}
	es, ds := ser(enc.t), ser(dec.t)
	if len(es) != len(ds) {
		t.Fatalf("tree sizes differ: encoder %d, decoder %d", len(es), len(ds))
	}
	for i := range es {
		if es[i] != ds[i] {
			t.Fatalf("trees diverge at node %d: enc=%v dec=%v", i, es[i], ds[i])
		}
	}
}

func TestConstantMemory(t *testing.T) {
	// Feed every possible byte value many times; the tree must stay
	// bounded by 2*256-1 nodes no matter how long the input is.
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i * 31)
	}
	enc, _ := encodeAll(t, data, 8192)
	if got, max := enc.TreeSize(), 2*256+1; got > max {
		t.Fatalf("tree grew to %d nodes, want <= %d", got, max)
	}
}

func TestEmptyInput(t *testing.T) {
	enc, compressed := encodeAll(t, nil, 1)
	if len(compressed) != 0 {
		t.Fatalf("empty input produced %d bytes", len(compressed))
	}
	if enc.TreeSize() != 1 {
		t.Fatalf("tree should contain only the NYT root, has %d nodes", enc.TreeSize())
	}
	_, plain := decodeAll(t, compressed, 4)
	if len(plain) != 0 {
		t.Fatalf("decoded %d bytes from empty stream", len(plain))
	}
}

func TestSingleSymbolRepeated(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, 100000)
	_, _, compressed, _ := roundTrip(t, data, 4096, 4096)
	// One 8-bit raw symbol plus ~1 bit per occurrence.
	if want := 100000/8 + 64; len(compressed) > want {
		t.Fatalf("repeated symbol: got %d bytes, want <= %d", len(compressed), want)
	}
}

func TestAllByteValues(t *testing.T) {
	data := make([]byte, 0, 256*300)
	for i := 0; i < 300; i++ {
		for b := 0; b < 256; b++ {
			data = append(data, byte(b))
		}
	}
	roundTrip(t, data, 333, 77)
}

func TestAdaptiveCodewordShrink(t *testing.T) {
	// As 'z' becomes frequent, its codeword must shorten: the tree
	// adapts to the observed distribution within a single pass.
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	seed := append(skewedText(64, 5), 'z')
	if _, err := enc.Write(seed); err != nil {
		t.Fatal(err)
	}
	before := len(pathBits(enc.t.leaves['z']))
	if _, err := enc.Write(bytes.Repeat([]byte{'z'}, 5000)); err != nil {
		t.Fatal(err)
	}
	after := len(pathBits(enc.t.leaves['z']))
	t.Logf("codeword length for 'z': %d -> %d bits", before, after)
	if after >= before {
		t.Fatalf("codeword did not shrink: before=%d after=%d", before, after)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadReturnsEOFAfterDrain(t *testing.T) {
	data := []byte("hello adaptive world")
	_, compressed := encodeAll(t, data, 64)
	dec := NewDecoder(bytes.NewReader(compressed))
	out, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("got %q, want %q", out, data)
	}
	if n, err := dec.Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Fatalf("after drain: got n=%d err=%v, want 0, EOF", n, err)
	}
}
