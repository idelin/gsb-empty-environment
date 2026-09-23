package codec

import (
	"bytes"
	"testing"

	"sfm/freq"
)

func buildOrder0(data []byte, alpha int) *freq.Model {
	m := freq.NewModel(alpha)
	for _, s := range data {
		m.Add(s)
	}
	return m
}

func buildOrder1(data []byte, alpha int) *freq.CondModel {
	cm := freq.NewCondModel(alpha)
	prev := byte(InitContext)
	for _, s := range data {
		cm.Add(prev, s)
		prev = s
	}
	return cm
}

func TestRoundTripOrder0(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog. the dog is lazy.")
	m := buildOrder0(data, 256)
	blob, err := EncodeOrder0(m, data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(blob, 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestRoundTripOrder1WithMerge(t *testing.T) {
	data := []byte("abababababab cdcdcdcdcd abab cdcd efefefef")
	cm := buildOrder1(data, 256)
	// Merge two contexts; encoding must still round trip, and the
	// merged contexts share one code.
	cm.MergeContexts('a', 'c')
	blob, err := EncodeConditional(cm, data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(blob, 1<<20)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("round trip mismatch: got %q", got)
	}
}

func TestRoundTripEmpty(t *testing.T) {
	m := freq.NewModel(256)
	blob, err := EncodeOrder0(m, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(blob, 10)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d bytes, want 0", len(got))
	}
}

func TestChecksumDetectsCorruption(t *testing.T) {
	data := []byte("integrity matters")
	m := buildOrder0(data, 256)
	blob, _ := EncodeOrder0(m, data)
	// Corrupt the group context byte inside the checksummed region
	// (offset 9: after magic+version+flags and the alpha/count/groups
	// varints). It stays a well-formed varint, so only the checksum
	// can catch it.
	blob[9] ^= 0x01
	if _, err := Decode(blob, 1<<20); err != ErrChecksum {
		t.Fatalf("corrupted header: err = %v, want ErrChecksum", err)
	}
	// Corrupt a payload byte: caught by checksum? No - payload is not
	// checksummed; it is caught as a decode-level mismatch instead.
	// (The format checksums the header/model, which is what selects
	// the code.) Flip a payload bit and expect a decode error or
	// wrong output, never a silent success with the right length.
	blob2, _ := EncodeOrder0(m, data)
	blob2[len(blob2)-1] ^= 0x40
	out, err := Decode(blob2, 1<<20)
	if err == nil && bytes.Equal(out, data) {
		t.Fatalf("payload corruption silently accepted")
	}
}

func TestBadVersionAndMagic(t *testing.T) {
	data := []byte("abcabc")
	m := buildOrder0(data, 256)
	blob, _ := EncodeOrder0(m, data)

	bad := append([]byte(nil), blob...)
	bad[3] = 0x7F
	if _, err := Decode(bad, 1<<20); err != ErrBadVersion {
		t.Fatalf("version byte: err = %v, want ErrBadVersion", err)
	}

	bad = append([]byte(nil), blob...)
	bad[0] = 'X'
	if _, err := Decode(bad, 1<<20); err != ErrBadMagic {
		t.Fatalf("magic: err = %v, want ErrBadMagic", err)
	}
}

func TestBoundedDecodeOutput(t *testing.T) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 10)
	}
	m := buildOrder0(data, 256)
	blob, _ := EncodeOrder0(m, data)
	if _, err := Decode(blob, 999); err != ErrOutputTooLarge {
		t.Fatalf("decode with limit below declared length: err = %v, want ErrOutputTooLarge", err)
	}
	if got, err := Decode(blob, 1000); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("decode at exact limit: err = %v", err)
	}
}

func TestUnknownSymbolRejected(t *testing.T) {
	m := freq.NewModel(4) // alphabet {0,1,2,3}
	m.Add(0)
	m.Add(1)
	if _, err := EncodeOrder0(m, []byte{0, 1, 7}); err != ErrUnknownSymbol {
		t.Fatalf("symbol outside alphabet: err = %v, want ErrUnknownSymbol", err)
	}
	// Zero-frequency but in-alphabet symbols are encodable (explicit
	// escape strategy): symbol 3 was never added yet must round trip.
	blob, err := EncodeOrder0(m, []byte{0, 3, 1})
	if err != nil {
		t.Fatalf("zero-frequency in-alphabet symbol must be encodable: %v", err)
	}
	got, err := Decode(blob, 10)
	if err != nil || !bytes.Equal(got, []byte{0, 3, 1}) {
		t.Fatalf("round trip of unseen symbol: %v %v", got, err)
	}
}

func TestTruncatedAndTrailing(t *testing.T) {
	data := []byte("some payload bytes for truncation tests")
	m := buildOrder0(data, 256)
	blob, _ := EncodeOrder0(m, data)
	if _, err := Decode(blob[:len(blob)-5], 1<<20); err == nil {
		t.Fatalf("truncated stream must fail")
	}
	if _, err := Decode(append(append([]byte(nil), blob...), 0xAA), 1<<20); err != ErrTrailingData {
		t.Fatalf("trailing garbage: err = %v, want ErrTrailingData", err)
	}
}

// TestEntropyIsEstimateNotByteLength demonstrates the package contract:
// the floating point entropy is an estimate; the real byte length comes
// from the encoder. A deterministic stream has entropy exactly 0 but a
// nonzero real length (header + 1 bit/symbol).
func TestEntropyIsEstimateNotByteLength(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, 128)
	m := buildOrder0(data, 256)
	if got := m.Entropy(); got != 0 {
		t.Fatalf("entropy of deterministic stream = %v, want 0", got)
	}
	blob, err := EncodeOrder0(m, data)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(blob) == 0 {
		t.Fatalf("real byte length is 0; entropy 0 must not be used as a length")
	}
	got, err := Decode(blob, 1<<20)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("round trip: %v", err)
	}
	// For non-trivial data the real payload length stays close to the
	// entropy estimate (within the model header plus Huffman overhead).
	words := []byte("the quick brown fox jumps over the lazy dog")
	m2 := buildOrder0(words, 256)
	blob2, _ := EncodeOrder0(m2, words)
	est := m2.Entropy() * float64(len(words)) / 8
	real := float64(len(blob2))
	if real < est {
		t.Fatalf("real length %v bytes < entropy estimate %v bytes: impossible for a real code", real, est)
	}
}
