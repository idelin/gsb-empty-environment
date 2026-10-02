package hashfn

import (
	"crypto/md5"
	"encoding/binary"
	"hash/fnv"
	"testing"
)

func TestFNV1aMatchesStdlib(t *testing.T) {
	inputs := [][]byte{
		{},
		{0},
		[]byte("a"),
		[]byte("hello world"),
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 255},
	}
	for _, in := range inputs {
		ref := fnv.New64a()
		ref.Write(in)
		if got := FNV1aHash(in); got != ref.Sum64() {
			t.Errorf("FNV1aHash(%q) = %d, stdlib = %d", in, got, ref.Sum64())
		}
	}
}

func TestMD5HashMatchesDigest(t *testing.T) {
	in := []byte("some cache key")
	sum := md5.Sum(in)
	want := binary.LittleEndian.Uint64(sum[:8])
	if got := MD5Hash(in); got != want {
		t.Errorf("MD5Hash = %d, want %d", got, want)
	}
}

func TestMurmur3EmptyIsZero(t *testing.T) {
	// With seed 0 the x64_128 reference yields 0 for the empty input.
	if got := Murmur3Hash(nil); got != 0 {
		t.Errorf("Murmur3Hash(nil) = %d, want 0", got)
	}
	if got := Murmur3Hash([]byte{}); got != 0 {
		t.Errorf("Murmur3Hash(empty) = %d, want 0", got)
	}
}

func TestHashesAreDeterministicAndDistinct(t *testing.T) {
	hashes := map[string]Hash64{
		"md5":    MD5Hash,
		"murmur": Murmur3Hash,
		"fnv1a":  FNV1aHash,
	}
	for name, h := range hashes {
		a := h([]byte("key-1"))
		b := h([]byte("key-1"))
		if a != b {
			t.Errorf("%s not deterministic", name)
		}
		c := h([]byte("key-2"))
		if a == c {
			t.Errorf("%s collides on key-1/key-2", name)
		}
	}
}

func TestMurmur3AllLengths(t *testing.T) {
	// Exercise every tail path (lengths 0..33) for determinism and to
	// make sure no path panics or degenerates to a constant.
	seen := make(map[uint64]int)
	key := make([]byte, 33)
	for i := range key {
		key[i] = byte(i*31 + 7)
	}
	for n := 0; n <= 33; n++ {
		h := Murmur3Hash(key[:n])
		if h != Murmur3Hash(key[:n]) {
			t.Fatalf("nondeterministic at length %d", n)
		}
		if prev, ok := seen[h]; ok {
			t.Fatalf("collision between lengths %d and %d", prev, n)
		}
		seen[h] = n
	}
}
