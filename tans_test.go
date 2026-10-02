package tans

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// zipfSample draws n symbols from a highly skewed Zipf-like distribution
// over alphabet symbols (symbol i has probability ~ 1/(i+1)).
func zipfSample(rng *rand.Rand, n, alphabet int) []byte {
	cum := make([]float64, alphabet)
	sum := 0.0
	for i := 0; i < alphabet; i++ {
		sum += 1 / float64(i+1)
		cum[i] = sum
	}
	out := make([]byte, n)
	for i := range out {
		v := rng.Float64() * sum
		s := 0
		for s < alphabet-1 && cum[s] < v {
			s++
		}
		out[i] = byte(s)
	}
	return out
}

// skewedSample builds a two-symbol 99/1 sample.
func skewedSample(rng *rand.Rand, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		if rng.Intn(100) == 0 {
			out[i] = 1
		}
	}
	return out
}

func roundTrip(t *testing.T, name string, data []byte, tableLog uint) {
	t.Helper()
	table := BuildTable(CountSymbols(data), tableLog)
	enc := table.Encode(nil, data)
	dec, err := table.Decode(nil, enc)
	if err != nil {
		t.Fatalf("%s: decode error: %v", name, err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatalf("%s: round trip mismatch (got %d bytes, want %d)", name, len(dec), len(data))
	}
}

func TestRoundTripDistributions(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	random := make([]byte, 1<<16)
	rng.Read(random)

	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"single byte", []byte{7}},
		{"all same", bytes.Repeat([]byte{0xAB}, 10000)},
		{"two symbols 99/1", skewedSample(rng, 100000)},
		{"zipf 16", zipfSample(rng, 100000, 16)},
		{"zipf 256", zipfSample(rng, 200000, 256)},
		{"uniform random", random},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			roundTrip(t, c.name, c.data, 0)
		})
	}
}

func TestRoundTripAllTableLogs(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	data := zipfSample(rng, 50000, 64)
	for tableLog := uint(MinTableLog); tableLog <= MaxTableLog; tableLog++ {
		roundTrip(t, "tableLog", data, tableLog)
	}
}

func entropyBits(counts []uint64) float64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	h := 0.0
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	return h * float64(total)
}

// TestCompressionRatio verifies that on highly skewed input the tANS
// payload (excluding the table itself) lands within 1% of the
// zero-order entropy bound.
func TestCompressionRatio(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	data := zipfSample(rng, 1<<20, 32)
	counts := CountSymbols(data)
	table := BuildTable(counts, 0)

	enc := table.Encode(nil, data)
	payloadBits := float64(len(enc)-headerSize) * 8
	bound := entropyBits(counts)

	t.Logf("entropy bound: %.0f bits, tANS payload: %.0f bits (%.3f%% of bound)",
		bound, payloadBits, 100*payloadBits/bound)
	if payloadBits > bound*1.01 {
		t.Fatalf("payload exceeds entropy bound by more than 1%%: %.0f vs %.0f", payloadBits, bound)
	}
	if payloadBits < bound*0.99 {
		t.Fatalf("payload suspiciously below entropy bound: %.0f vs %.0f", payloadBits, bound)
	}
}

// TestCompressionRatioWithTable checks that even including the serialized
// table, a large skewed stream compresses to within 2% of the bound.
func TestCompressionRatioWithTable(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	data := zipfSample(rng, 8<<20, 64)
	counts := CountSymbols(data)
	table := BuildTable(counts, 0)

	tbl, err := table.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	enc := table.Encode(nil, data)
	totalBits := float64(len(enc)+len(tbl)) * 8
	bound := entropyBits(counts)

	t.Logf("entropy bound: %.0f bits, total with table: %.0f bits (%.3f%% of bound)",
		bound, totalBits, 100*totalBits/bound)
	if totalBits > bound*1.02 {
		t.Fatalf("total exceeds entropy bound by more than 2%%: %.0f vs %.0f", totalBits, bound)
	}
}

func TestTableSerialization(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := zipfSample(rng, 100000, 100)
	table := BuildTable(CountSymbols(data), 0)

	raw, err := table.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	table2, err := UnmarshalTable(raw)
	if err != nil {
		t.Fatal(err)
	}
	enc := table.Encode(nil, data)
	dec, err := table2.Decode(nil, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatal("round trip with deserialized table failed")
	}
}

func TestDecodeCorrupt(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	data := zipfSample(rng, 1000, 16)
	table := BuildTable(CountSymbols(data), 0)
	enc := table.Encode(nil, data)

	if _, err := table.Decode(nil, enc[:headerSize-1]); err != ErrInvalidStream {
		t.Fatalf("short header: got %v", err)
	}
	if _, err := table.Decode(nil, enc[:len(enc)-2]); err != ErrInvalidStream {
		t.Fatalf("truncated payload: got %v", err)
	}
	if _, err := UnmarshalTable(enc[:100]); err != ErrInvalidStream {
		t.Fatalf("bad table: got %v", err)
	}
}

// TestRangeCoderRoundTrip validates the arithmetic-coding baseline.
func TestRangeCoderRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	random := make([]byte, 1<<15)
	rng.Read(random)
	cases := [][]byte{
		nil,
		{42},
		bytes.Repeat([]byte{9}, 5000),
		skewedSample(rng, 50000),
		zipfSample(rng, 100000, 64),
		random,
	}
	for i, data := range cases {
		table := BuildTable(CountSymbols(data), 0)
		model := newRangeModel(table.norm)
		enc := rangeEncode(model, data)
		dec := rangeDecode(model, enc, len(data))
		if !bytes.Equal(dec, data) {
			t.Fatalf("case %d: range coder round trip mismatch", i)
		}
	}
}

// TestTANSvsRangeCoderRatio sanity-checks that tANS payload size is
// comparable to the range coder on the same skewed input.
func TestTANSvsRangeCoderRatio(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	data := zipfSample(rng, 1<<20, 32)
	table := BuildTable(CountSymbols(data), 0)
	model := newRangeModel(table.norm)

	tansSize := len(table.Encode(nil, data)) - headerSize
	rcSize := len(rangeEncode(model, data))
	t.Logf("tANS payload: %d bytes, range coder: %d bytes", tansSize, rcSize)
	if tansSize > rcSize+rcSize/100+64 {
		t.Fatalf("tANS payload significantly larger than range coder: %d vs %d", tansSize, rcSize)
	}
}

func ExampleTable_Encode() {
	data := []byte("aaaaabbbbcccdde") // highly skewed toward 'a'
	table := BuildTable(CountSymbols(data), 0)
	compressed := table.Encode(nil, data)
	restored, err := table.Decode(nil, compressed)
	if err != nil {
		panic(err)
	}
	fmt.Println("round trip ok:", bytes.Equal(restored, data))
	// Output:
	// round trip ok: true
}
