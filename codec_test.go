package erasure

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

func randomData(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	r.Read(b)
	return b
}

func mustEncode(t *testing.T, data []byte, k, m int) [][]byte {
	shards, err := Encode(data, k, m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(shards) != k+m {
		t.Fatalf("got %d shards, want %d", len(shards), k+m)
	}
	return shards
}

func TestRoundTripAllShards(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{0, 1, 7, 100, 1024, 10007} {
		data := randomData(r, n)
		shards := mustEncode(t, data, 6, 3)
		got, err := Decode(shards, int64(n))
		if err != nil {
			t.Fatalf("n=%d Decode: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d round trip mismatch", n)
		}
	}
}

func TestRecoverAnySubset(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	k, m := 6, 3
	data := randomData(r, 5000)
	shards := mustEncode(t, data, k, m)
	for trial := 0; trial < 40; trial++ {
		perm := r.Perm(k + m)[:k]
		sel := make([][]byte, k)
		for i, idx := range perm {
			sel[i] = shards[idx]
		}
		got, err := Decode(sel, -1)
		if err != nil {
			t.Fatalf("trial %d subset %v: %v", trial, perm, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("trial %d subset %v: mismatch", trial, perm)
		}
	}
}

func TestInsufficientShards(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	k, m := 4, 2
	data := randomData(r, 999)
	shards := mustEncode(t, data, k, m)
	// k-1 valid shards: one more than tolerance missing.
	_, err := Decode(shards[:k-1], -1)
	if err != ErrInsufficientShards {
		t.Fatalf("got %v, want ErrInsufficientShards", err)
	}
	// No shards at all.
	if _, err := Decode(nil, -1); err != ErrInsufficientShards {
		t.Fatalf("got %v, want ErrInsufficientShards", err)
	}
}

func TestDuplicateShardRejected(t *testing.T) {
	data := []byte("duplicate shard id must be rejected")
	shards := mustEncode(t, data, 3, 2)
	dup := [][]byte{shards[0], shards[0], shards[1], shards[2]}
	if _, err := Decode(dup, -1); err != ErrDuplicateShard {
		t.Fatalf("got %v, want ErrDuplicateShard", err)
	}
}

func corruptPayload(blob []byte) []byte {
	c := make([]byte, len(blob))
	copy(c, blob)
	c[HeaderSize] ^= 0xFF // flip payload bits, breaking the CRC
	return c
}

func TestCorruptShardExcluded(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	k, m := 5, 2
	data := randomData(r, 2048)
	shards := mustEncode(t, data, k, m)

	// Corrupt one shard; exactly k valid shards remain and must recover.
	mixed := make([][]byte, 0, k+1)
	mixed = append(mixed, corruptPayload(shards[0]))
	mixed = append(mixed, shards[1:k+1]...)
	got, err := Decode(mixed, -1)
	if err != nil {
		t.Fatalf("corrupt shard must be excluded, not fatal: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("recovered data mismatch with corrupt shard present")
	}

	// Corrupt beyond tolerance: only k-1 valid shards remain.
	mixed = nil
	mixed = append(mixed, corruptPayload(shards[0]))
	mixed = append(mixed, corruptPayload(shards[1]))
	mixed = append(mixed, shards[2:k+1]...)
	if _, err := Decode(mixed, -1); err != ErrInsufficientShards {
		t.Fatalf("got %v, want ErrInsufficientShards", err)
	}
}

func TestCorruptShardNotUsedInEquations(t *testing.T) {
	// A shard with a forged valid CRC but wrong payload would silently
	// corrupt the output if used; the checksum is what keeps damaged
	// blocks out of the equation solving. Verify that a bit-flipped
	// payload is detected via CRC and never contributes.
	r := rand.New(rand.NewSource(5))
	k, m := 4, 2
	data := randomData(r, 512)
	shards := mustEncode(t, data, k, m)

	bad := corruptPayload(shards[0])
	if _, _, err := parseShard(bad); err != ErrChecksumMismatch {
		t.Fatalf("tampered payload not detected: %v", err)
	}

	// All k+m shards, one corrupt: decode must still succeed and match.
	all := append([][]byte{bad}, shards[1:]...)
	got, err := Decode(all, -1)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("corrupt shard influenced decode: err=%v match=%v",
			err, bytes.Equal(got, data))
	}
}

func TestTamperedHeaderRejected(t *testing.T) {
	data := []byte("header fields are protected by the checksum")
	shards := mustEncode(t, data, 3, 2)

	// Unsupported version.
	v := append([]byte(nil), shards[0]...)
	v[2] = 99
	if _, err := Decode([][]byte{v, shards[1], shards[2]}, -1); err != ErrUnsupportedVersion {
		t.Fatalf("version: got %v, want ErrUnsupportedVersion", err)
	}

	// An impossibly large declared length is structurally malformed and
	// rejected outright.
	bad := append([]byte(nil), shards[0]...)
	binary.LittleEndian.PutUint64(bad[10:18], 1<<40)
	if _, err := Decode([][]byte{bad, shards[1], shards[2]}, -1); err != ErrMalformedShard {
		t.Fatalf("huge tampered length: got %v, want ErrMalformedShard", err)
	}

	// A tampered but structurally plausible length breaks the CRC: the
	// shard is excluded; with only k-1 valid shards left decode reports
	// insufficiency rather than using the corrupt block.
	bad = append([]byte(nil), shards[0]...)
	binary.LittleEndian.PutUint64(bad[10:18], 1)
	if _, _, err := parseShard(bad); err != ErrChecksumMismatch {
		t.Fatalf("plausible tampered length: got %v, want ErrChecksumMismatch", err)
	}
	if _, err := Decode([][]byte{bad, shards[1], shards[2]}, -1); err != ErrInsufficientShards {
		t.Fatalf("tampered length: got %v, want ErrInsufficientShards", err)
	}

	// Truncated shard is malformed.
	if _, err := Decode([][]byte{shards[0][:HeaderSize+1]}, -1); err != ErrMalformedShard {
		t.Fatalf("truncated: got %v, want ErrMalformedShard", err)
	}

	// Bad magic.
	m := append([]byte(nil), shards[0]...)
	m[0] = 'X'
	if _, err := Decode([][]byte{m}, -1); err != ErrMalformedShard {
		t.Fatalf("magic: got %v, want ErrMalformedShard", err)
	}
}

func TestInconsistentHeaders(t *testing.T) {
	a := mustEncode(t, []byte("message A, length twenty-seven"), 3, 2)
	b := mustEncode(t, []byte("B"), 3, 2)
	mixed := [][]byte{a[0], a[1], b[2]}
	if _, err := Decode(mixed, -1); err != ErrInconsistentHeader {
		t.Fatalf("got %v, want ErrInconsistentHeader", err)
	}
}

func TestOutputLimit(t *testing.T) {
	data := []byte("bounded decode output")
	shards := mustEncode(t, data, 3, 2)
	if _, err := Decode(shards, int64(len(data)-1)); err != ErrOutputLimit {
		t.Fatalf("got %v, want ErrOutputLimit", err)
	}
	got, err := Decode(shards, int64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("limit at exact size should pass: %v", err)
	}
}

func TestInvalidParams(t *testing.T) {
	for _, km := range [][2]int{{0, 1}, {1, 0}, {-1, 2}, {200, 57}, {255, 2}} {
		if _, err := Encode([]byte("x"), km[0], km[1]); err != ErrInvalidParams {
			t.Fatalf("k=%d m=%d: got %v, want ErrInvalidParams", km[0], km[1], err)
		}
	}
}

func TestShardIDOutOfRange(t *testing.T) {
	data := []byte("id range check")
	shards := mustEncode(t, data, 2, 1)
	// Forge a shard with id == k+m (out of range) and a valid CRC.
	forged := append([]byte(nil), shards[0]...)
	forged[5] = 3
	binary.LittleEndian.PutUint32(forged[18:22],
		crc32.ChecksumIEEE(append(forged[0:18:18], forged[HeaderSize:]...)))
	if _, err := Decode([][]byte{forged, shards[1]}, -1); err != ErrMalformedShard {
		t.Fatalf("got %v, want ErrMalformedShard", err)
	}
}

func TestEmptyAndSingleByte(t *testing.T) {
	for _, data := range [][]byte{{}, {0xAB}} {
		shards := mustEncode(t, data, 4, 2)
		// Drop two shards (at tolerance) and recover.
		keep := append([][]byte{}, shards[1], shards[2], shards[3], shards[5])
		got, err := Decode(keep, -1)
		if err != nil {
			t.Fatalf("len=%d: %v", len(data), err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("len=%d mismatch", len(data))
		}
	}
}

func TestLargeKManyParities(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	k, m := 10, 5
	data := randomData(r, 12345)
	shards := mustEncode(t, data, k, m)
	// Lose all 5 parity shards' worth of tolerance from mixed positions.
	perm := r.Perm(k + m)
	sel := make([][]byte, 0, k)
	for _, idx := range perm[:k] {
		sel = append(sel, shards[idx])
	}
	got, err := Decode(sel, int64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("err=%v match=%v", err, bytes.Equal(got, data))
	}
}
