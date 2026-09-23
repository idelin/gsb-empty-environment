package layerpack

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func encodeToBytes(t *testing.T, input []byte, codecs []Codec, lim Limits) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, bytes.NewReader(input), codecs, lim); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

func decodeToBytes(t *testing.T, container []byte, lim Limits) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	err := Decode(&out, bytes.NewReader(container), lim)
	return out.Bytes(), err
}

func TestRoundTripMultiLayer(t *testing.T) {
	input := pseudoRandom(1 << 18)
	container := encodeToBytes(t, input, []Codec{Flate, Gzip, Store}, DefaultLimits())
	out, err := decodeToBytes(t, container, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatal("round trip mismatch")
	}
}

func TestRoundTripEmpty(t *testing.T) {
	container := encodeToBytes(t, nil, []Codec{Flate}, DefaultLimits())
	out, err := decodeToBytes(t, container, DefaultLimits())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(out))
	}
}

// Each layer's output is individually under MaxOutputBytes, but the
// cumulative consumption across layers exceeds it and must be rejected.
func TestCumulativeOutputBudgetAcrossLayers(t *testing.T) {
	input := bytes.Repeat([]byte("x"), 60)
	container := encodeToBytes(t, input, []Codec{Store, Store, Store}, DefaultLimits())

	lim := DefaultLimits()
	lim.MaxOutputBytes = 100 // 60 per layer fits; 60+60 does not
	if _, err := decodeToBytes(t, container, lim); !errors.Is(err, ErrBudgetOutput) {
		t.Fatalf("want ErrBudgetOutput, got %v", err)
	}

	lim.MaxOutputBytes = 180 // 3 * 60 exactly fits
	if _, err := decodeToBytes(t, container, lim); err != nil {
		t.Fatalf("decode with sufficient cumulative budget: %v", err)
	}
}

// The output budget is checked before the layer buffer is allocated or any
// decompression is attempted: a container claiming a huge rawLen must fail
// with ErrBudgetOutput even when its payload is undecodable garbage.
func TestOutputBudgetCheckedBeforeAllocation(t *testing.T) {
	container := encodeToBytes(t, []byte("0123456789"), []Codec{Store}, DefaultLimits())
	// rawLen sits at header(6) + codec(1) + reserved(1) + compLen(8) = 16.
	const rawLenOff = 16
	huge := uint64(1) << 40
	for i := 0; i < 8; i++ {
		container[rawLenOff+i] = byte(huge >> (56 - 8*i))
	}
	// Corrupt the payload so any decompression attempt would fail too.
	container[len(container)-1] ^= 0xFF

	lim := DefaultLimits()
	lim.MaxOutputBytes = 1 << 20
	_, err := decodeToBytes(t, container, lim)
	if !errors.Is(err, ErrBudgetOutput) {
		t.Fatalf("want ErrBudgetOutput before allocation, got %v", err)
	}
}

func TestEncodeOutputBudgetCumulative(t *testing.T) {
	input := bytes.Repeat([]byte("y"), 50)
	lim := DefaultLimits()
	lim.MaxOutputBytes = 120 // 50+50 fits, 50+50+50 does not
	var buf bytes.Buffer
	err := Encode(&buf, bytes.NewReader(input), []Codec{Store, Store, Store}, lim)
	if !errors.Is(err, ErrBudgetOutput) {
		t.Fatalf("want ErrBudgetOutput, got %v", err)
	}
}

func TestHighRatioRejectedByDefault(t *testing.T) {
	input := make([]byte, 1<<20) // 1 MiB of zeros, flate ratio >> 100
	container := encodeToBytes(t, input, []Codec{Flate}, DefaultLimits())
	if _, err := decodeToBytes(t, container, DefaultLimits()); !errors.Is(err, ErrBudgetRatio) {
		t.Fatalf("want ErrBudgetRatio, got %v", err)
	}
}

// A legitimate large compression ratio is allowed by explicit configuration.
func TestHighRatioAllowedExplicitly(t *testing.T) {
	input := make([]byte, 1<<20)
	container := encodeToBytes(t, input, []Codec{Flate}, DefaultLimits())
	lim := DefaultLimits()
	lim.AllowHighRatio = true
	out, err := decodeToBytes(t, container, lim)
	if err != nil {
		t.Fatalf("Decode with AllowHighRatio: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatal("round trip mismatch")
	}
}

func TestLayerBudget(t *testing.T) {
	input := []byte("payload")
	lim := DefaultLimits()
	lim.MaxLayers = 2
	var buf bytes.Buffer
	if err := Encode(&buf, bytes.NewReader(input), []Codec{Store, Store, Store}, lim); !errors.Is(err, ErrBudgetLayers) {
		t.Fatalf("encode: want ErrBudgetLayers, got %v", err)
	}

	container := encodeToBytes(t, input, []Codec{Store, Store, Store}, DefaultLimits())
	if _, err := decodeToBytes(t, container, lim); !errors.Is(err, ErrBudgetLayers) {
		t.Fatalf("decode: want ErrBudgetLayers, got %v", err)
	}
}

func TestInputBudget(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxInputBytes = 10
	var buf bytes.Buffer
	err := Encode(&buf, bytes.NewReader(bytes.Repeat([]byte("z"), 11)), []Codec{Store}, lim)
	if !errors.Is(err, ErrBudgetInput) {
		t.Fatalf("encode: want ErrBudgetInput, got %v", err)
	}

	container := encodeToBytes(t, []byte("0123456789"), []Codec{Store}, DefaultLimits())
	lim.MaxInputBytes = int64(len(container)) - 1
	if _, err := decodeToBytes(t, container, lim); !errors.Is(err, ErrBudgetInput) {
		t.Fatalf("decode: want ErrBudgetInput, got %v", err)
	}
}

func TestChecksumMismatch(t *testing.T) {
	container := encodeToBytes(t, []byte("checksum me"), []Codec{Store}, DefaultLimits())
	container[len(container)-1] ^= 0xFF
	if _, err := decodeToBytes(t, container, DefaultLimits()); !errors.Is(err, ErrChecksum) {
		t.Fatalf("want ErrChecksum, got %v", err)
	}
}

func TestUnsupportedVersion(t *testing.T) {
	container := encodeToBytes(t, []byte("v"), []Codec{Store}, DefaultLimits())
	container[4] = 99
	if _, err := decodeToBytes(t, container, DefaultLimits()); !errors.Is(err, ErrVersion) {
		t.Fatalf("want ErrVersion, got %v", err)
	}
}

func TestMalformedContainers(t *testing.T) {
	valid := encodeToBytes(t, []byte("abc"), []Codec{Store}, DefaultLimits())
	cases := map[string][]byte{
		"empty":          {},
		"short header":   valid[:5],
		"bad magic":      append([]byte("XXXX"), valid[4:]...),
		"zero layers":    append([]byte{}, valid[:5]...),
		"truncated":      valid[:len(valid)-1],
		"trailing bytes": append(append([]byte{}, valid...), 0),
		"unknown codec":  append([]byte{valid[0], valid[1], valid[2], valid[3], valid[4], valid[5], 77}, valid[7:]...),
	}
	for name, c := range cases {
		if _, err := decodeToBytes(t, c, DefaultLimits()); err == nil || errors.Is(err, ErrChecksum) {
			t.Errorf("%s: want format error, got %v", name, err)
		}
	}
}

func TestEncodeFileCleansUpOnError(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.lyr")
	lim := DefaultLimits()
	lim.MaxInputBytes = 4 // force failure mid-encode
	err := EncodeFile(dst, bytes.NewReader(bytes.Repeat([]byte("q"), 100)), []Codec{Flate}, lim)
	if !errors.Is(err, ErrBudgetInput) {
		t.Fatalf("want ErrBudgetInput, got %v", err)
	}
	assertDirEmpty(t, dir)
}

func TestDecodeFileCleansUpOnError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.lyr")
	container := encodeToBytes(t, bytes.Repeat([]byte("s"), 60), []Codec{Store, Store}, DefaultLimits())
	if err := os.WriteFile(src, container, 0o644); err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits()
	lim.MaxOutputBytes = 100 // cumulative 120 > 100
	err := DecodeFile(filepath.Join(dir, "out.bin"), src, lim)
	if !errors.Is(err, ErrBudgetOutput) {
		t.Fatalf("want ErrBudgetOutput, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "in.lyr" {
		t.Fatalf("temp output not cleaned up, dir holds %v", entries)
	}
}

func TestFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "c.lyr")
	dst := filepath.Join(dir, "out.bin")
	input := pseudoRandom(1 << 16)
	if err := EncodeFile(src, bytes.NewReader(input), []Codec{Flate, Gzip}, DefaultLimits()); err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	if err := DecodeFile(dst, src, DefaultLimits()); err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, input) {
		t.Fatal("file round trip mismatch")
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary output not cleaned up, dir holds %v", entries)
	}
}

// pseudoRandom returns deterministic, poorly compressible data so tests
// stay under the default expansion-ratio budget.
func pseudoRandom(n int) []byte {
	b := make([]byte, n)
	x := uint32(0x12345678)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}
