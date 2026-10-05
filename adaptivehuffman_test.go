package adaptivehuffman

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// roundtripSync encodes data symbol by symbol and decodes it back, checking
// after every checkEvery-th symbol that (a) the tree invariants hold on both
// sides and (b) the encoder's and decoder's exported states are byte
// identical, proving the two sides evolve deterministically in sync.
func roundtripSync(t *testing.T, data []byte, maxWeight uint64, checkEvery int) {
	t.Helper()
	enc := NewEncoderWithMaxWeight(maxWeight)
	dec := NewDecoderWithMaxWeight(maxWeight)
	buf := new(bytes.Buffer)
	bw := NewBitWriter(buf)
	encStates := make(map[int][]byte)
	for i, b := range data {
		if err := enc.EncodeSymbol(bw, b); err != nil {
			t.Fatalf("encode symbol %d: %v", i, err)
		}
		if i%checkEvery == 0 || i == len(data)-1 {
			checkModelInvariants(t, enc.m)
			encStates[i] = enc.ExportState()
		}
	}
	if err := bw.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	br := NewBitReader(bytes.NewReader(buf.Bytes()))
	for i := 0; i < len(data); i++ {
		got, err := dec.DecodeSymbol(br)
		if err != nil {
			t.Fatalf("decode symbol %d: %v", i, err)
		}
		if got != data[i] {
			t.Fatalf("symbol %d: got 0x%02x, want 0x%02x", i, got, data[i])
		}
		if i%checkEvery == 0 || i == len(data)-1 {
			checkModelInvariants(t, dec.m)
			if want := encStates[i]; !bytes.Equal(dec.ExportState(), want) {
				t.Fatalf("encoder/decoder state divergence after symbol %d", i)
			}
		}
	}
	checkPrefixFree(t, enc.m)
}

func TestBitIO(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	buf := new(bytes.Buffer)
	bw := NewBitWriter(buf)
	type op struct {
		v uint64
		n uint
	}
	var ops []op
	total := 0
	for total < 5000 {
		n := uint(rng.Intn(64) + 1)
		v := rng.Uint64()
		if n < 64 {
			v &= (1 << n) - 1
		}
		bw.WriteBits(v, n)
		ops = append(ops, op{v, n})
		total += int(n)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	br := NewBitReader(bytes.NewReader(buf.Bytes()))
	for i, o := range ops {
		got, err := br.ReadBits(o.n)
		if err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
		if got != o.v {
			t.Fatalf("op %d: got %x, want %x (n=%d)", i, got, o.v, o.n)
		}
	}
}

// TestExhaustiveSmallAlphabet exhaustively encodes every string up to a
// maximum length over alphabets of size 2, 3 and 4, verifying round-trip,
// tree invariants and encoder/decoder state synchronisation after every
// single symbol.
func TestExhaustiveSmallAlphabet(t *testing.T) {
	cases := []struct {
		alpha  int
		maxLen int
	}{
		{2, 8},
		{3, 6},
		{4, 5},
	}
	for _, tc := range cases {
		t.Run(string(rune('a'+tc.alpha-2)), func(t *testing.T) {
			var rec func(s []byte)
			rec = func(s []byte) {
				if len(s) > 0 {
					roundtripSync(t, s, DefaultMaxWeight, 1)
				}
				if len(s) == tc.maxLen {
					return
				}
				for i := 0; i < tc.alpha; i++ {
					rec(append(s, byte('a'+i)))
				}
			}
			rec(nil)
		})
	}
}

func TestAll256Bytes(t *testing.T) {
	data := make([]byte, 0, 3*256)
	for i := 0; i < 256; i++ {
		data = append(data, byte(i))
	}
	for i := 0; i < 256; i++ {
		data = append(data, byte(255-i))
	}
	for i := 0; i < 256; i++ {
		data = append(data, byte((i*37+11)&0xff))
	}
	roundtripSync(t, data, DefaultMaxWeight, 1)

	var buf bytes.Buffer
	if err := EncodeFrame(&buf, data); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("frame round-trip mismatch")
	}
}

func TestLongSingleByte(t *testing.T) {
	data := bytes.Repeat([]byte{0xAA}, 1<<20)
	roundtripSync(t, data, DefaultMaxWeight, 1<<16)
}

// TestRescale uses a tiny weight threshold to force frequent rescaling and
// verifies the stream still round-trips, invariants hold, and the root
// weight never exceeds the threshold.
func TestRescale(t *testing.T) {
	const maxW = 64
	rng := rand.New(rand.NewSource(42))
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte(rng.Intn(32))
	}
	roundtripSync(t, data, maxW, 1)

	enc := NewEncoderWithMaxWeight(maxW)
	bw := NewBitWriter(io.Discard)
	maxSeen := uint64(0)
	for _, b := range data {
		if err := enc.EncodeSymbol(bw, b); err != nil {
			t.Fatal(err)
		}
		if enc.m.root.weight > maxSeen {
			maxSeen = enc.m.root.weight
		}
	}
	if maxSeen > maxW {
		t.Fatalf("root weight %d exceeded threshold %d: rescale did not happen", maxSeen, maxW)
	}
	// 20000 symbols far exceed the threshold, so without rescaling the root
	// weight would have reached 20000; the check above proves rescaling
	// occurred. Additionally require the tree actually approached the
	// threshold at some point (sanity that the test is meaningful).
	if maxSeen < maxW/2 {
		t.Fatalf("root weight never approached the threshold (max %d)", maxSeen)
	}
}

func TestFrameRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{0, 1, 2, 3, 7, 8, 9, 15, 16, 17, 100, 1000, 4097} {
		data := make([]byte, n)
		rng.Read(data)
		var buf bytes.Buffer
		if err := EncodeFrame(&buf, data); err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		got, err := DecodeFrame(&buf)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d: payload mismatch", n)
		}
	}
}

// chunkReader returns at most chunk bytes per Read call.
type chunkReader struct {
	data  []byte
	chunk int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.chunk
	if n > len(c.data) {
		n = len(c.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

// TestChunkedInput feeds a frame through readers that return the data in
// small chunks, exercising the bit reader's buffering.
func TestChunkedInput(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 5000)
	rng.Read(data)
	var buf bytes.Buffer
	if err := EncodeFrame(&buf, data); err != nil {
		t.Fatal(err)
	}
	frame := buf.Bytes()
	for chunk := 1; chunk <= 9; chunk++ {
		got, err := DecodeFrame(&chunkReader{data: append([]byte(nil), frame...), chunk: chunk})
		if err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("chunk=%d: payload mismatch", chunk)
		}
	}
}
