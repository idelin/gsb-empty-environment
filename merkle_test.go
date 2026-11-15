package netsec

import (
	"bytes"
	"errors"
	"testing"
)

var testSalt = []byte("0123456789abcdef")

func makeData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func newVerifier(t *testing.T, tree *Tree, actualSize int64) *Verifier {
	t.Helper()
	m, err := tree.Measurement()
	if err != nil {
		t.Fatalf("Measurement: %v", err)
	}
	v, err := NewVerifier(tree.Descriptor(), m, tree.Serialize(), actualSize)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestSingleBlock(t *testing.T) {
	for _, n := range []int{1, 1000, BlockSize} {
		data := makeData(n)
		tree, err := BuildTree(data, AlgSHA256, testSalt)
		if err != nil {
			t.Fatalf("BuildTree(%d): %v", n, err)
		}
		if len(tree.Levels) != 0 {
			t.Fatalf("size %d: want no tree levels, got %d", n, len(tree.Levels))
		}
		v := newVerifier(t, tree, int64(n))
		if err := v.VerifyBlock(0, data); err != nil {
			t.Fatalf("size %d: VerifyBlock: %v", n, err)
		}
		if err := v.VerifyAll(data); err != nil {
			t.Fatalf("size %d: VerifyAll: %v", n, err)
		}
		if err := v.VerifyBlock(1, makeData(BlockSize)); !errors.Is(err, ErrBlockOutOfRange) {
			t.Fatalf("size %d: block 1 should be out of range, got %v", n, err)
		}
	}
}

func TestNonMultipleOfBlock(t *testing.T) {
	data := makeData(3*BlockSize + 123)
	tree, err := BuildTree(data, AlgSHA256, testSalt)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	v := newVerifier(t, tree, int64(len(data)))
	if err := v.VerifyAll(data); err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}
	// Last partial block must be passed at its exact length.
	last := data[3*BlockSize:]
	if err := v.VerifyBlock(3, last); err != nil {
		t.Fatalf("VerifyBlock(last): %v", err)
	}
	full := make([]byte, BlockSize)
	copy(full, last)
	if err := v.VerifyBlock(3, full); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("padded last block should be rejected, got %v", err)
	}
}

func TestAppendRejected(t *testing.T) {
	data := makeData(2*BlockSize + 10)
	tree, err := BuildTree(data, AlgSHA256, testSalt)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	m, err := tree.Measurement()
	if err != nil {
		t.Fatalf("Measurement: %v", err)
	}
	appended := append(append([]byte(nil), data...), makeData(500)...)

	// The actual file no longer matches the signed data length.
	if _, err := NewVerifier(tree.Descriptor(), m, tree.Serialize(), int64(len(appended))); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("appended file should be rejected, got %v", err)
	}
	// Even if the length check were bypassed, the extra block is out of
	// range and the whole-file check fails.
	v := newVerifier(t, tree, int64(len(data)))
	if err := v.VerifyAll(appended); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("VerifyAll(appended) should fail, got %v", err)
	}
	if err := v.VerifyBlock(3, make([]byte, BlockSize)); !errors.Is(err, ErrBlockOutOfRange) {
		t.Fatalf("appended block should be out of range, got %v", err)
	}
}

func TestSaltAndAlgorithmChangeRejected(t *testing.T) {
	data := makeData(2 * BlockSize)
	tree, err := BuildTree(data, AlgSHA256, testSalt)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	m, err := tree.Measurement()
	if err != nil {
		t.Fatalf("Measurement: %v", err)
	}

	// Attacker swaps the salt in the descriptor: measurement no longer matches.
	badSalt := tree.Descriptor()
	badSalt[offSalt] ^= 0xff
	if _, err := NewVerifier(badSalt, m, tree.Serialize(), int64(len(data))); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("changed salt should be rejected, got %v", err)
	}

	// Attacker swaps the algorithm field: measurement no longer matches.
	badAlg := tree.Descriptor()
	badAlg[offAlgorithm] = AlgSHA512
	if _, err := NewVerifier(badAlg, m, tree.Serialize(), int64(len(data))); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("changed algorithm should be rejected, got %v", err)
	}

	// A tree rebuilt with a different salt yields a different measurement.
	other, err := BuildTree(data, AlgSHA256, []byte("FEDCBA9876543210"))
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	m2, err := other.Measurement()
	if err != nil {
		t.Fatalf("Measurement: %v", err)
	}
	if bytes.Equal(m, m2) {
		t.Fatal("different salts must yield different measurements")
	}
}

func TestTamperedIntermediateBlock(t *testing.T) {
	// 200 data blocks + 1 byte: level 0 has 2 hash blocks, level 1 has 1.
	data := makeData(200*BlockSize + 1)
	tree, err := BuildTree(data, AlgSHA256, testSalt)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	if len(tree.Levels) != 2 {
		t.Fatalf("want 2 tree levels, got %d", len(tree.Levels))
	}
	m, err := tree.Measurement()
	if err != nil {
		t.Fatalf("Measurement: %v", err)
	}

	// Corrupt one hash inside the first level-0 block.
	blob := tree.Serialize()
	blob[5*32+7] ^= 0x01
	v, err := NewVerifier(tree.Descriptor(), m, blob, int64(len(data)))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.VerifyBlock(5, data[5*BlockSize:6*BlockSize]); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("block under tampered hash should fail, got %v", err)
	}
	// A block covered by the untouched level-0 block still verifies.
	if err := v.VerifyBlock(150, data[150*BlockSize:151*BlockSize]); err != nil {
		t.Fatalf("unaffected block should verify: %v", err)
	}

	// Corrupt the single top-level block: every block fails.
	blob2 := tree.Serialize()
	blob2[len(blob2)-100] ^= 0x01
	v2, err := NewVerifier(tree.Descriptor(), m, blob2, int64(len(data)))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v2.VerifyBlock(0, data[:BlockSize]); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("tampered top level should fail, got %v", err)
	}
}

func TestTamperedData(t *testing.T) {
	data := makeData(4 * BlockSize)
	tree, err := BuildTree(data, AlgSHA256, testSalt)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	v := newVerifier(t, tree, int64(len(data)))
	tampered := append([]byte(nil), data...)
	tampered[BlockSize+42] ^= 0x80
	if err := v.VerifyBlock(1, tampered[BlockSize:2*BlockSize]); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("tampered data block should fail, got %v", err)
	}
	if err := v.VerifyAll(tampered); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("VerifyAll(tampered) should fail, got %v", err)
	}
}

func TestSHA512(t *testing.T) {
	data := makeData(2*BlockSize + 7)
	tree, err := BuildTree(data, AlgSHA512, testSalt)
	if err != nil {
		t.Fatalf("BuildTree: %v", err)
	}
	v := newVerifier(t, tree, int64(len(data)))
	if err := v.VerifyAll(data); err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}
}
