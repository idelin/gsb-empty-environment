package rangecoder

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

func roundTrip(t *testing.T, src []byte) {
	t.Helper()
	enc := Encode(src)
	dec, err := Decode(enc, len(src)+1)
	if err != nil {
		t.Fatalf("Decode(%d bytes src): %v", len(src), err)
	}
	if !bytes.Equal(dec, src) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", len(dec), len(src))
	}
}

func TestRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	random := make([]byte, 100000)
	rng.Read(random)

	skewed := make([]byte, 100000)
	for i := range skewed {
		// Heavily skewed distribution to exercise model adaptation.
		skewed[i] = byte(rng.Intn(4))
	}

	allBytes := make([]byte, 256*40)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}

	cases := map[string][]byte{
		"empty":      {},
		"one byte":   {'x'},
		"hello":      []byte("hello, range coder"),
		"text":       bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 500),
		"all bytes":  allBytes,
		"random":     random,
		"skewed":     skewed,
		"zero run":   make([]byte, 50000),
		"0xff run":   bytes.Repeat([]byte{0xff}, 50000),
		"edge bytes": {0x00, 0xff, 0xfe, 0x01, 0x00, 0xff},
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, src) })
	}
}

// A long run of a single symbol must not overflow the 32/33-bit coder
// state and must compress to a tiny fraction of the input.
func TestLongSingleSymbolStream(t *testing.T) {
	src := bytes.Repeat([]byte{'A'}, 3000000)
	enc := Encode(src)
	if limit := len(src) / 100; len(enc) > limit {
		t.Fatalf("compressed size %d, want < %d (1%% of source)", len(enc), limit)
	}
	dec, err := Decode(enc, len(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(dec, src) {
		t.Fatal("round trip mismatch on long single-symbol stream")
	}
	t.Logf("3,000,000 identical bytes -> %d bytes", len(enc))
}

// After rescaling, every known symbol must keep a positive frequency and
// the total must drop back below the scaling threshold.
func TestRescaleKeepsPositiveFrequencies(t *testing.T) {
	m := newModel()
	// Drive the model far past several rescaling points.
	for i := 0; i < 200000; i++ {
		m.update(uint32(i % 37))
		if m.total >= maxTotal {
			t.Fatalf("total %d not below threshold after update", m.total)
		}
	}
	var sum uint32
	for i, f := range m.freq {
		if f == 0 {
			t.Fatalf("symbol %d has zero frequency after rescaling", i)
		}
		sum += f
	}
	if sum != m.total {
		t.Fatalf("total %d != sum of frequencies %d", m.total, sum)
	}
}

// Encoder and decoder must walk identical model states; verify by decoding
// a mixed stream and comparing against the source at every stage.
func TestEncoderDecoderStateSync(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	src := make([]byte, 200000)
	for i := range src {
		switch rng.Intn(3) {
		case 0:
			src[i] = byte(rng.Intn(256))
		case 1:
			src[i] = 'a' + byte(rng.Intn(26))
		default:
			src[i] = 0
		}
	}
	roundTrip(t, src)
}

func TestHeaderFields(t *testing.T) {
	src := []byte("format header check")
	enc := Encode(src)
	if !bytes.Equal(enc[:3], []byte("RAC")) {
		t.Fatalf("bad magic %q", enc[:3])
	}
	if enc[3] != formatVersion {
		t.Fatalf("version = %d, want %d", enc[3], formatVersion)
	}
	if got := binary.BigEndian.Uint64(enc[4:12]); got != uint64(len(src)) {
		t.Fatalf("length field = %d, want %d", got, len(src))
	}
	if got := binary.BigEndian.Uint32(enc[12:16]); got != crc32.ChecksumIEEE(src) {
		t.Fatalf("crc field = %#x, want %#x", got, crc32.ChecksumIEEE(src))
	}
}

func TestBadMagicAndVersion(t *testing.T) {
	src := []byte("some payload")
	enc := Encode(src)

	bad := append([]byte(nil), enc...)
	bad[0] = 'X'
	if _, err := Decode(bad, 100); err != ErrBadMagic {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}

	bad = append([]byte(nil), enc...)
	bad[3] = 99
	if _, err := Decode(bad, 100); err != ErrBadVersion {
		t.Fatalf("got %v, want ErrBadVersion", err)
	}

	if _, err := Decode(enc[:headerSize-1], 100); err != ErrTruncated {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestCorruptPayloadDetected(t *testing.T) {
	src := bytes.Repeat([]byte("corrupt me! "), 2000)
	enc := Encode(src)
	// Corruption of any byte that participates in decoding is detected.
	// (Trailing bytes past the EOF terminator are ignored by design.)
	for _, pos := range []int{headerSize, headerSize + 5, len(enc) / 2, len(enc) - 6} {
		bad := append([]byte(nil), enc...)
		bad[pos] ^= 0x5a
		if _, err := Decode(bad, len(src)+1); err == nil {
			t.Fatalf("corruption at offset %d went undetected", pos)
		}
	}
}

// A truncated stream must terminate with an error, never decode forever.
func TestTruncatedStreamBounded(t *testing.T) {
	src := bytes.Repeat([]byte("truncate me "), 1000)
	enc := Encode(src)
	for _, cut := range []int{headerSize, headerSize + 1, len(enc) / 2, len(enc) - 2} {
		if _, err := Decode(enc[:cut], len(src)+1); err == nil {
			t.Fatalf("truncation to %d bytes went undetected", cut)
		}
	}
}

// Garbage with a valid-looking header must stop at the output budget.
func TestGarbageStreamBoundedByBudget(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var hdr [headerSize]byte
	copy(hdr[:3], magic)
	hdr[3] = formatVersion
	binary.BigEndian.PutUint64(hdr[4:12], 1<<40) // absurd claimed length
	binary.BigEndian.PutUint32(hdr[12:16], 0)

	garbage := make([]byte, 4096)
	rng.Read(garbage)
	data := append(hdr[:], garbage...)

	const budget = 10000
	if _, err := Decode(data, budget); err != ErrBudget {
		t.Fatalf("got %v, want ErrBudget", err)
	}
}

// A declared length beyond the budget is rejected before decoding starts.
func TestDeclaredLengthExceedsBudget(t *testing.T) {
	src := []byte("budget check")
	enc := Encode(src)
	if _, err := Decode(enc, len(src)-1); err != ErrBudget {
		t.Fatalf("got %v, want ErrBudget", err)
	}
}

// The EOF terminator, not the length field alone, ends decoding: extra
// trailing bytes after a valid stream must be ignored.
func TestTerminatorEndsStream(t *testing.T) {
	src := []byte("terminated")
	enc := Encode(src)
	padded := append(enc, 0xde, 0xad, 0xbe, 0xef)
	dec, err := Decode(padded, 100)
	if err != nil {
		t.Fatalf("Decode with trailing garbage: %v", err)
	}
	if !bytes.Equal(dec, src) {
		t.Fatal("output mismatch with trailing garbage")
	}
}
