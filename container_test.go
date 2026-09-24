package secded

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"testing"
)

func encodeOrDie(t *testing.T, data []byte) []byte {
	t.Helper()
	blob, err := Encode(data, 6, DefaultDepth)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// payloadBit converts a payload stream bit index to a blob bit index.
func payloadBit(t int) int { return headerSize*8 + t }

func flipBlobBit(blob []byte, bit int) []byte {
	out := append([]byte(nil), blob...)
	out[bit/8] ^= 1 << uint(bit%8)
	return out
}

func TestContainerRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, n := range []int{0, 1, 7, 57, 456, 457, 4096} {
		data := make([]byte, n)
		rng.Read(data)
		blob := encodeOrDie(t, data)
		res, err := Decode(blob, nil)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !res.Clean() {
			t.Fatalf("n=%d: expected clean decode, corrected=%v uncorrectable=%v",
				n, res.Corrected, res.Uncorrectable)
		}
		if !bytes.Equal(res.Data, data) {
			t.Fatalf("n=%d: data mismatch", n)
		}
	}
}

func TestContainerHeaderChecks(t *testing.T) {
	data := []byte("important payload that must be protected")
	blob := encodeOrDie(t, data)

	// bad magic
	bad := append([]byte(nil), blob...)
	bad[0] = 'X'
	if _, err := Decode(bad, nil); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("magic: got %v", err)
	}
	// unsupported version
	bad = append([]byte(nil), blob...)
	binary.LittleEndian.PutUint16(bad[4:6], 99)
	binary.LittleEndian.PutUint32(bad[32:36], crc32Of(bad[0:32]))
	if _, err := Decode(bad, nil); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("version: got %v", err)
	}
	// header CRC tamper
	bad = append([]byte(nil), blob...)
	bad[32] ^= 0xff
	if _, err := Decode(bad, nil); !errors.Is(err, ErrBadHeaderCRC) {
		t.Fatalf("header crc: got %v", err)
	}
	// truncated
	if _, err := Decode(blob[:len(blob)-3], nil); !errors.Is(err, ErrBadLength) {
		t.Fatalf("truncated: got %v", err)
	}
	// trailing garbage
	if _, err := Decode(append(blob, 0), nil); !errors.Is(err, ErrBadLength) {
		t.Fatalf("trailing: got %v", err)
	}
}

func crc32Of(b []byte) uint32 { return crc32.Checksum(b, crc32.MakeTable(crc32.Castagnoli)) }

// TestOutputQuota: the declared output size is enforced before any
// allocation; a legitimate container larger than the quota is refused.
func TestOutputQuota(t *testing.T) {
	data := make([]byte, 1024)
	blob := encodeOrDie(t, data)
	if _, err := Decode(blob, &Options{MaxOutputBytes: 512}); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("got %v, want ErrOutputTooLarge", err)
	}
	res, err := Decode(blob, &Options{MaxOutputBytes: 1024})
	if err != nil || !bytes.Equal(res.Data, data) {
		t.Fatalf("quota at exact size should pass: %v", err)
	}
}

// TestSingleFlipCorrectedEndToEnd: one payload bit flip is repaired
// and the repair is reported, not hidden.
func TestSingleFlipCorrectedEndToEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	data := make([]byte, 300)
	rng.Read(data)
	blob := encodeOrDie(t, data)
	corrupt := flipBlobBit(blob, payloadBit(123))
	res, err := Decode(corrupt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Data, data) {
		t.Fatal("data not restored")
	}
	if len(res.Corrected) == 0 || len(res.Uncorrectable) != 0 {
		t.Fatalf("expected corrected block report, got corrected=%v uncorrectable=%v",
			res.Corrected, res.Uncorrectable)
	}
}

// TestDoubleFlipNeverClaimedRepaired: two flips in one codeword must
// surface as an uncorrectable block with zeroed output, never as data.
func TestDoubleFlipNeverClaimedRepaired(t *testing.T) {
	data := bytes.Repeat([]byte("cd"), 200)
	blob := encodeOrDie(t, data)
	// Two flips inside the first codeword of block 0 (stream bits 0 and Depth).
	corrupt := flipBlobBit(blob, payloadBit(0))
	corrupt = flipBlobBit(corrupt, payloadBit(DefaultDepth))
	res, err := Decode(corrupt, nil)
	if !errors.Is(err, ErrUncorrectableBlocks) {
		t.Fatalf("got %v, want ErrUncorrectableBlocks", err)
	}
	if len(res.Uncorrectable) == 0 {
		t.Fatal("uncorrectable block list empty")
	}
	blk := res.Uncorrectable[0]
	start := blk * 57 // 456 data bits = 57 bytes per block (m=6, depth=8)
	for i := start; i < start+57 && i < len(res.Data); i++ {
		if res.Data[i] != 0 {
			t.Fatal("uncorrectable block output must be zeroed, not guessed")
		}
	}
}

// TestMiscorrectionCaughtByChecksum: three flips in one codeword alias
// to a single error and miscorrect; the end-to-end data CRC must catch
// what the ECC cannot.
func TestMiscorrectionCaughtByChecksum(t *testing.T) {
	data := bytes.Repeat([]byte("ef"), 200)
	blob := encodeOrDie(t, data)
	corrupt := flipBlobBit(blob, payloadBit(0))
	corrupt = flipBlobBit(corrupt, payloadBit(DefaultDepth))
	corrupt = flipBlobBit(corrupt, payloadBit(2*DefaultDepth))
	_, err := Decode(corrupt, nil)
	if !errors.Is(err, ErrDataChecksum) {
		t.Fatalf("got %v, want ErrDataChecksum", err)
	}
}

// TestErasureRecoveryEndToEnd: a marked burst of 3*Depth payload bits
// is fully recovered through the container API.
func TestErasureRecoveryEndToEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 500)
	rng.Read(data)
	blob := encodeOrDie(t, data)
	start := 0
	n := 3 * DefaultDepth
	corrupt := append([]byte(nil), blob...)
	for i := start; i < start+n; i++ {
		bit := payloadBit(i)
		corrupt[bit/8] ^= 1 << uint(bit%8)
	}
	res, err := Decode(corrupt, &Options{Erasures: []BitRange{{Start: int64(start), Len: int64(n)}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Data, data) {
		t.Fatal("erasure-marked burst not recovered")
	}
	if len(res.Corrected) == 0 {
		t.Fatal("expected erasure recovery to be reported as corrected")
	}
}

// TestBitSourceMapping: the mapping accounts for every output bit and
// reflects the fate of its codeword.
func TestBitSourceMapping(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	data := make([]byte, 114) // exactly one block of data (m=6, depth=8)
	rng.Read(data)
	blob := encodeOrDie(t, data)
	// Flip stream bit 0 = word 0, codeword position 1 (a parity bit).
	corrupt := flipBlobBit(blob, payloadBit(0))
	res, err := Decode(corrupt, &Options{WithMapping: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Mapping) != len(data)*8 {
		t.Fatalf("mapping has %d entries, want %d", len(res.Mapping), len(data)*8)
	}
	code, _ := New(6)
	dataPos := code.DataPositions()
	seen := make(map[int64]bool)
	for _, ms := range res.Mapping {
		seen[ms.OutputBit] = true
		if ms.Position != dataPos[ms.OutputBit%int64(code.K)] {
			t.Fatalf("bit %d: position %d not in data-position map", ms.OutputBit, ms.Position)
		}
		wantFate := FateClean
		if ms.Block == 0 && ms.Word == 0 {
			wantFate = FateCorrected
		}
		if ms.Fate != wantFate {
			t.Fatalf("bit %d (word %d): fate %v, want %v", ms.OutputBit, ms.Word, ms.Fate, wantFate)
		}
	}
	for i := int64(0); i < int64(len(data))*8; i++ {
		if !seen[i] {
			t.Fatalf("output bit %d missing from mapping", i)
		}
	}
}

// TestStreamRoundTrip exercises the streaming façade.
func TestStreamRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	data := make([]byte, 3000)
	rng.Read(data)
	var buf bytes.Buffer
	if err := EncodeStream(&buf, bytes.NewReader(data), 6, DefaultDepth); err != nil {
		t.Fatal(err)
	}
	blob := append([]byte(nil), buf.Bytes()...)
	var out bytes.Buffer
	res, err := DecodeStream(&out, &buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) || !res.Clean() {
		t.Fatal("stream round trip mismatch")
	}
	// A corrupted stream must not emit bytes.
	blob = flipBlobBit(blob, payloadBit(0))
	blob = flipBlobBit(blob, payloadBit(DefaultDepth))
	var out2 bytes.Buffer
	_, err = DecodeStream(&out2, bytes.NewReader(blob), nil)
	if !errors.Is(err, ErrUncorrectableBlocks) {
		t.Fatalf("got %v, want ErrUncorrectableBlocks", err)
	}
	if out2.Len() != 0 {
		t.Fatal("no output may be emitted when blocks are uncorrectable")
	}
}
