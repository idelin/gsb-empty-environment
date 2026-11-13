package netsec

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"testing"
)

func TestContinueSHA256MatchesStandardLibraryOnKnownPrefixes(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(0x5eed))
	edgeLengths := []int{0, 1, 54, 55, 56, 57, 63, 64, 65, 119, 120, 127, 128, 129, 191, 192}
	suffixLengths := []int{0, 1, 54, 55, 56, 63, 64, 65, 127, 128, 129}

	for _, prefixLen := range edgeLengths {
		for _, suffixLen := range suffixLengths {
			prefix := randomBytes(rng, prefixLen)
			suffix := randomBytes(rng, suffixLen)
			assertExtensionMatchesLibrary(t, prefix, suffix, uint64(prefixLen))
		}
	}

	for i := 0; i < 200; i++ {
		prefixLen := rng.Intn(320)
		suffixLen := rng.Intn(260)
		prefix := randomBytes(rng, prefixLen)
		suffix := randomBytes(rng, suffixLen)
		assertExtensionMatchesLibrary(t, prefix, suffix, uint64(prefixLen))
	}
}

func TestImportSHA256StateRoundTrip(t *testing.T) {
	t.Parallel()

	tag := sha256.Sum256([]byte("known prefix"))
	state, err := ImportSHA256State(tag[:])
	if err != nil {
		t.Fatalf("ImportSHA256State() error = %v", err)
	}
	if got := state.Tag(); !bytes.Equal(got, tag[:]) {
		t.Fatalf("state.Tag() = %x, want %x", got, tag)
	}

	emptyState, err := ImportSHA256State(sha256EmptyTag(t))
	if err != nil {
		t.Fatalf("ImportSHA256State(empty) error = %v", err)
	}
	if want := [8]uint32{
		0xe3b0c442, 0x98fc1c14, 0x9afbf4c8, 0x996fb924,
		0x27ae41e4, 0x649b934c, 0xa495991b, 0x7852b855,
	}; emptyState != SHA256State(want) {
		t.Fatalf("empty state = %x, want %x", emptyState, want)
	}
}

func TestImportSHA256StateRejectsWrongSize(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 1, 31, 33, 64} {
		_, err := ImportSHA256State(make([]byte, size))
		if err == nil {
			t.Fatalf("ImportSHA256State(%d bytes) error = nil, want error", size)
		}
	}
}

func TestSHA256PaddingEdgeLengths(t *testing.T) {
	t.Parallel()

	for _, total := range []uint64{0, 1, 54, 55, 56, 57, 63, 64, 65, 119, 120, 127, 128, 1 << 32} {
		padding := SHA256Padding(total)
		wantLen := SHA256PaddedLength(total) - total
		if uint64(len(padding)) != wantLen {
			t.Fatalf("SHA256Padding(%d) length = %d, want %d", total, len(padding), wantLen)
		}
		if padding[0] != 0x80 {
			t.Fatalf("SHA256Padding(%d)[0] = %#x, want 0x80", total, padding[0])
		}
		if (total+uint64(len(padding)))%SHA256BlockSize != 0 {
			t.Fatalf("SHA256Padding(%d) does not produce a whole block", total)
		}

		wantBitLength := total << 3
		gotBitLength := bytesToUint64(padding[len(padding)-8:])
		if gotBitLength != wantBitLength {
			t.Fatalf("SHA256Padding(%d) bit length = %#x, want %#x", total, gotBitLength, wantBitLength)
		}
	}
}

func TestSHA256PaddingLengthWrapsModulo2To64(t *testing.T) {
	t.Parallel()

	total := ^uint64(0) // 2^64 - 1 bytes
	padding := SHA256Padding(total)
	got := bytesToUint64(padding[len(padding)-8:])
	want := total << 3 // multiplication wraps in uint64
	if got != want {
		t.Fatalf("encoded bit length = %#x, want %#x", got, want)
	}
}

func TestContinueSHA256EmptySuffixAddsFinalBlock(t *testing.T) {
	t.Parallel()

	prefix := []byte("secret-and-message")
	originalTag := sha256.Sum256(prefix)
	originalPadding := SHA256Padding(uint64(len(prefix)))

	gotTag, padding, err := ExtendSHA256(originalTag[:], nil, uint64(len(prefix)))
	if err != nil {
		t.Fatalf("ExtendSHA256() error = %v", err)
	}
	if !bytes.Equal(padding, originalPadding) {
		t.Fatalf("padding mismatch")
	}

	extended := append(append([]byte(nil), prefix...), originalPadding...)
	wantTag := sha256.Sum256(extended)
	if !bytes.Equal(gotTag, wantTag[:]) {
		t.Fatalf("empty-suffix tag = %x, want %x", gotTag, wantTag)
	}
}

func assertExtensionMatchesLibrary(t *testing.T, prefix, suffix []byte, originalLen uint64) {
	t.Helper()

	originalTag := sha256.Sum256(prefix)
	gotTag, padding, err := ExtendSHA256(originalTag[:], suffix, originalLen)
	if err != nil {
		t.Fatalf("ExtendSHA256(prefix=%d, suffix=%d) error = %v", len(prefix), len(suffix), err)
	}
	if want := SHA256Padding(originalLen); !bytes.Equal(padding, want) {
		t.Fatalf("ExtendSHA256(prefix=%d, suffix=%d) padding mismatch", len(prefix), len(suffix))
	}

	concatenated := make([]byte, 0, len(prefix)+len(padding)+len(suffix))
	concatenated = append(concatenated, prefix...)
	concatenated = append(concatenated, padding...)
	concatenated = append(concatenated, suffix...)
	wantTag := sha256.Sum256(concatenated)

	if !bytes.Equal(gotTag, wantTag[:]) {
		t.Fatalf("prefix=%d suffix=%d: continued tag = %x, standard-library tag = %x",
			len(prefix), len(suffix), gotTag, wantTag)
	}
}

func randomBytes(rng *rand.Rand, n int) []byte {
	data := make([]byte, n)
	_, _ = rng.Read(data)
	return data
}

func sha256EmptyTag(t *testing.T) []byte {
	t.Helper()
	tag := sha256.Sum256(nil)
	return tag[:]
}

func bytesToUint64(data []byte) uint64 {
	var value uint64
	for _, b := range data {
		value = value<<8 | uint64(b)
	}
	return value
}
