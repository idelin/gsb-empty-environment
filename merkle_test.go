package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func testHash(t *testing.T, alg Algorithm) hash.Hash {
	t.Helper()
	var h hash.Hash
	switch alg {
	case AlgorithmSHA256:
		h = sha256.New()
	case AlgorithmSHA512:
		h = sha512.New()
	default:
		t.Fatalf("unsupported algorithm %d", alg)
	}
	return h
}

func referenceBlockHash(t *testing.T, alg Algorithm, salt, block []byte) []byte {
	t.Helper()
	prefix := make([]byte, alg.HashBlockSize())
	copy(prefix, salt)
	page := make([]byte, BlockSize)
	copy(page, block)
	h := testHash(t, alg)
	_, _ = h.Write(prefix)
	_, _ = h.Write(page)
	return h.Sum(nil)
}

func referenceRoot(t *testing.T, data, salt []byte, alg Algorithm) []byte {
	t.Helper()
	digestSize := alg.DigestSize()
	perPage := BlockSize / digestSize
	blockCount := (len(data) + BlockSize - 1) / BlockSize

	level := make([][]byte, blockCount)
	for i := 0; i < blockCount; i++ {
		start := i * BlockSize
		end := start + BlockSize
		if end > len(data) {
			end = len(data)
		}
		level[i] = referenceBlockHash(t, alg, salt, data[start:end])
	}

	if len(level) == 0 {
		return make([]byte, digestSize)
	}
	for len(level) != 1 {
		next := make([][]byte, (len(level)+perPage-1)/perPage)
		for i := range next {
			page := make([]byte, BlockSize)
			start := i * perPage
			end := start + perPage
			if end > len(level) {
				end = len(level)
			}
			for j := start; j < end; j++ {
				copy(page[(j-start)*digestSize:], level[j])
			}
			h := testHash(t, alg)
			prefix := make([]byte, alg.HashBlockSize())
			copy(prefix, salt)
			_, _ = h.Write(prefix)
			_, _ = h.Write(page)
			next[i] = h.Sum(nil)
		}
		level = next
	}
	return level[0]
}

func TestDescriptorLayoutAndRoundTrip(t *testing.T) {
	data := bytes.Repeat([]byte{0x42}, BlockSize*2+7)
	salt := []byte{1, 2, 3, 4, 5}
	d, err := NewDescriptor(data, salt)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != DescriptorSize {
		t.Fatalf("descriptor length = %d, want %d", len(raw), DescriptorSize)
	}
	if raw[offsetVersion] != Version || raw[offsetAlgorithm] != byte(AlgorithmSHA256) ||
		raw[offsetLog2BlockSize] != Log2BlockSize || raw[offsetSaltSize] != byte(len(salt)) {
		t.Fatalf("header fields = %v %v %v %v", raw[0], raw[1], raw[2], raw[3])
	}
	if got := binary.LittleEndian.Uint64(raw[offsetDataSize : offsetDataSize+8]); got != uint64(len(data)) {
		t.Fatalf("data size = %d, want %d", got, len(data))
	}
	if !allZero(raw[offsetReserved04:offsetDataSize]) {
		t.Fatal("bytes 4..8 are not zero")
	}
	if !bytes.Equal(raw[offsetRootHash:offsetRootHash+sha256.Size], d.RootHash) {
		t.Fatal("root hash is not at offset 16")
	}
	if !allZero(raw[offsetRootHash+sha256.Size : offsetSalt]) {
		t.Fatal("unused root hash bytes are not zero")
	}
	if !bytes.Equal(raw[offsetSalt:offsetSalt+len(salt)], salt) {
		t.Fatal("salt is not at offset 80")
	}
	if !allZero(raw[offsetSalt+len(salt):]) {
		t.Fatal("reserved bytes are not zero")
	}

	parsed, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.DataSize != d.DataSize || !bytes.Equal(parsed.RootHash, d.RootHash) ||
		!bytes.Equal(parsed.Salt, d.Salt) || parsed.Algorithm != d.Algorithm {
		t.Fatalf("round trip mismatch: %+v != %+v", parsed, d)
	}

	measurement, err := d.Measurement()
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	if !bytes.Equal(measurement, h[:]) {
		t.Fatal("measurement is not SHA-256(descriptor)")
	}
}

func TestSingleBlock(t *testing.T) {
	data := bytes.Repeat([]byte{0xa5}, 100)
	salt := []byte{9, 8, 7}
	d, measurement, err := Measure(data, salt)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := referenceRoot(t, data, salt, AlgorithmSHA256)
	if !bytes.Equal(d.RootHash, wantRoot) {
		t.Fatalf("root = %x, want %x", d.RootHash, wantRoot)
	}

	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		t.Fatal(err)
	}
	if v.NumBlocks() != 1 {
		t.Fatalf("block count = %d, want 1", v.NumBlocks())
	}
	block, err := v.ReadBlock(0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(block, data) {
		t.Fatal("single block content mismatch")
	}
	padded, err := v.ReadBlockPadded(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(padded) != BlockSize || !bytes.Equal(padded[:len(data)], data) ||
		!allZero(padded[len(data):]) {
		t.Fatal("last block was not zero-padded")
	}
	if err := v.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestNonBlockMultiple(t *testing.T) {
	data := make([]byte, BlockSize*2+333)
	for i := range data {
		data[i] = byte(i * 31)
	}
	salt := bytes.Repeat([]byte{0x5a}, 17)
	d, measurement, err := Measure(data, salt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.RootHash, referenceRoot(t, data, salt, AlgorithmSHA256)) {
		t.Fatal("reference root mismatch")
	}

	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		t.Fatal(err)
	}
	if v.NumBlocks() != 3 {
		t.Fatalf("block count = %d, want 3", v.NumBlocks())
	}
	for i := 0; i < v.NumBlocks(); i++ {
		got, err := v.ReadBlock(i)
		if err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
		start := i * BlockSize
		end := start + BlockSize
		if end > len(data) {
			end = len(data)
		}
		if !bytes.Equal(got, data[start:end]) {
			t.Fatalf("block %d content mismatch", i)
		}
	}

	off := int64(BlockSize - 20)
	want := data[off : off+60]
	got := make([]byte, 60)
	n, err := v.ReadAt(got, off)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) || !bytes.Equal(got, want) {
		t.Fatal("ReadAt crossing a block boundary failed")
	}

	all, err := v.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(all, data) {
		t.Fatal("ReadAll content mismatch")
	}
}

func TestAppendAfterMeasurement(t *testing.T) {
	data := make([]byte, BlockSize*3+17)
	for i := range data {
		data[i] = byte(i)
	}
	salt := []byte("append-test")
	d, measurement, err := Measure(data, salt)
	if err != nil {
		t.Fatal(err)
	}

	appended := append(append([]byte(nil), data...), 0x99)
	if _, err := NewVerifierWithData(appended, d, measurement); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("appended data: got %v, want ErrSizeMismatch", err)
	}
	if err := VerifyData(appended, d, measurement); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("VerifyData appended: got %v, want ErrSizeMismatch", err)
	}
	if err := VerifyFile(bytes.NewReader(data[:len(data)-1]), int64(len(data)), d, measurement); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("truncated data: got %v, want ErrSizeMismatch", err)
	}

	path := filepath.Join(t.TempDir(), "appended.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	v, err := NewVerifier(f, d, measurement, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(); err != nil {
		t.Fatal(err)
	}

	f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xaa}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.ReadBlock(0); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("read after append: got %v, want ErrSizeMismatch", err)
	}
	if err := v.Verify(); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("verify after append: got %v, want ErrSizeMismatch", err)
	}
}

func TestChangedSaltAndAlgorithmRejected(t *testing.T) {
	data := bytes.Repeat([]byte{0x31}, BlockSize+99)
	saltA := []byte("salt-A")
	saltB := []byte("salt-B")
	dA, mA, err := Measure(data, saltA)
	if err != nil {
		t.Fatal(err)
	}
	dB, mB, err := Measure(data, saltB)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(mA, mB) {
		t.Fatal("changing salt did not change the measurement")
	}
	if err := dA.VerifyMeasurement(mB); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("salt swap: got %v, want ErrMeasurementMismatch", err)
	}
	if _, err := NewVerifierWithData(data, dB, mA); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("salt swap verifier: got %v, want ErrMeasurementMismatch", err)
	}

	d512, m512, err := Measure(data, saltA, AlgorithmSHA512)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(m512, mA) {
		t.Fatal("changing algorithm did not change the measurement")
	}
	if err := dA.VerifyMeasurement(m512); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("algorithm swap: got %v, want ErrMeasurementMismatch", err)
	}
	if _, err := NewVerifierWithData(data, d512, mA); !errors.Is(err, ErrMeasurementMismatch) {
		t.Fatalf("algorithm swap verifier: got %v, want ErrMeasurementMismatch", err)
	}
	if err := d512.VerifyMeasurement(m512); err != nil {
		t.Fatal(err)
	}
}

func TestModifiedMiddleBlockRejected(t *testing.T) {
	data := make([]byte, BlockSize*5+11)
	for i := range data {
		data[i] = byte(i * 7)
	}
	salt := []byte("middle")
	d, measurement, err := Measure(data, salt)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		t.Fatal(err)
	}

	data[BlockSize*2+100] ^= 0xff
	if _, err := v.ReadBlock(2); !errors.Is(err, ErrCorruptBlock) {
		t.Fatalf("modified block read: got %v, want ErrCorruptBlock", err)
	}
	if err := v.VerifyBlock(2); !errors.Is(err, ErrCorruptBlock) {
		t.Fatalf("modified block verify: got %v, want ErrCorruptBlock", err)
	}
	if _, err := v.ReadAt(make([]byte, 10), int64(BlockSize*2+95)); !errors.Is(err, ErrCorruptBlock) {
		t.Fatalf("modified block ReadAt: got %v, want ErrCorruptBlock", err)
	}
	if err := v.Verify(); !errors.Is(err, ErrRootHashMismatch) {
		t.Fatalf("full verify after modification: got %v, want ErrRootHashMismatch", err)
	}
	if _, err := v.ReadAll(); !errors.Is(err, ErrRootHashMismatch) {
		t.Fatalf("ReadAll after modification: got %v, want ErrRootHashMismatch", err)
	}

	data[BlockSize*2+100] ^= 0xff
	if _, err := v.ReadBlock(2); err != nil {
		t.Fatalf("restored block should verify: %v", err)
	}
}

func TestMultipleTreeLevels(t *testing.T) {
	data := make([]byte, BlockSize*129+3)
	for i := range data {
		data[i] = byte(i*13 + 5)
	}
	salt := bytes.Repeat([]byte{0x3c}, SaltSize)
	d, measurement, err := Measure(data, salt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.RootHash, referenceRoot(t, data, salt, AlgorithmSHA256)) {
		t.Fatal("multi-level root mismatch")
	}
	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		t.Fatal(err)
	}
	if v.NumBlocks() != 130 {
		t.Fatalf("block count = %d, want 130", v.NumBlocks())
	}
	for _, index := range []int{0, 64, 127, 128, 129} {
		if _, err := v.ReadBlock(index); err != nil {
			t.Fatalf("block %d: %v", index, err)
		}
	}
	data[BlockSize*128+1] ^= 1
	if _, err := v.ReadBlock(128); !errors.Is(err, ErrCorruptBlock) {
		t.Fatalf("modified level boundary block: got %v", err)
	}
}

func TestSHA512(t *testing.T) {
	data := bytes.Repeat([]byte{0xc3}, BlockSize*2+1)
	salt := []byte("sha512-salt")
	d, measurement, err := Measure(data, salt, AlgorithmSHA512)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.RootHash) != sha512.Size || len(measurement) != sha512.Size {
		t.Fatalf("SHA-512 sizes = %d, %d", len(d.RootHash), len(measurement))
	}
	if !bytes.Equal(d.RootHash, referenceRoot(t, data, salt, AlgorithmSHA512)) {
		t.Fatal("SHA-512 reference root mismatch")
	}
	raw, err := d.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !allZero(raw[offsetRootHash+sha512.Size : offsetSalt]) {
		t.Fatal("SHA-512 descriptor has non-zero trailing root bytes")
	}
	if _, err := NewVerifierWithData(data, d, measurement); err != nil {
		t.Fatal(err)
	}
}

func TestDescriptorValidation(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 10)
	d, measurement, err := Measure(data, []byte("salt"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	short := raw[:DescriptorSize-1]
	if _, err := ParseDescriptor(short); !errors.Is(err, ErrDescriptorSize) {
		t.Fatalf("short descriptor: got %v", err)
	}
	badVersion := append([]byte(nil), raw...)
	badVersion[offsetVersion] = 2
	if _, err := ParseDescriptor(badVersion); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("bad version: got %v", err)
	}
	badAlg := append([]byte(nil), raw...)
	badAlg[offsetAlgorithm] = 0
	if _, err := ParseDescriptor(badAlg); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("bad algorithm: got %v", err)
	}
	badLog := append([]byte(nil), raw...)
	badLog[offsetLog2BlockSize] = 13
	if _, err := ParseDescriptor(badLog); !errors.Is(err, ErrUnsupportedBlockSize) {
		t.Fatalf("bad block size: got %v", err)
	}
	badReserved := append([]byte(nil), raw...)
	badReserved[offsetReserved] = 1
	if _, err := ParseDescriptor(badReserved); !errors.Is(err, ErrReservedBytes) {
		t.Fatalf("non-zero reserved: got %v", err)
	}
	badRoot := append([]byte(nil), raw...)
	badRoot[offsetRootHash+sha256.Size] = 1
	if _, err := ParseDescriptor(badRoot); !errors.Is(err, ErrInvalidRootHash) {
		t.Fatalf("non-zero root tail: got %v", err)
	}
	badSalt := append([]byte(nil), raw...)
	badSalt[offsetSalt+4] = 1
	if _, err := ParseDescriptor(badSalt); !errors.Is(err, ErrInvalidSalt) {
		t.Fatalf("non-zero salt tail: got %v", err)
	}

	parsed, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.VerifyMeasurement(measurement); err != nil {
		t.Fatal(err)
	}
}

func TestReadAtPastEnd(t *testing.T) {
	data := bytes.Repeat([]byte{6}, 20)
	d, measurement, err := Measure(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifierWithData(data, d, measurement)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	n, err := v.ReadAt(buf, 15)
	if n != 5 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt past end = %d, %v; want 5, EOF", n, err)
	}
	if !bytes.Equal(buf[:5], data[15:]) {
		t.Fatal("short ReadAt content mismatch")
	}
}

func TestEmptyFile(t *testing.T) {
	d, measurement, err := Measure(nil, []byte("empty"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.RootHash) != sha256.Size || !allZero(d.RootHash) {
		t.Fatalf("empty root = %x, want all zeroes", d.RootHash)
	}
	v, err := NewVerifierWithData(nil, d, measurement)
	if err != nil {
		t.Fatal(err)
	}
	if v.NumBlocks() != 0 {
		t.Fatalf("empty block count = %d", v.NumBlocks())
	}
	if _, err := v.ReadBlock(0); !errors.Is(err, ErrBlockIndex) {
		t.Fatalf("empty ReadBlock: got %v", err)
	}
	if err := v.Verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewVerifierWithData([]byte{1}, d, measurement); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("empty descriptor with data: got %v", err)
	}
}
