package netsec

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"sync"
	"testing"
)

// constantHash forces every key into bucket 0, simulating an attacker
// who has found a complete collision set.
func constantHash([]byte) uint64 { return 0 }

func TestSipHashReferenceVectors(t *testing.T) {
	// Reference vectors from the SipHash paper (key = 00 01 .. 0f,
	// message = 00 01 .. (n-1)).
	var key [16]byte
	for i := range key {
		key[i] = byte(i)
	}
	k0 := binary.LittleEndian.Uint64(key[:8])
	k1 := binary.LittleEndian.Uint64(key[8:])
	want := []uint64{
		0x726fdb47dd0e0e31,
		0x74f839c593dc67fd,
		0x0d6c8009d9a94f5a,
		0x85676696d7fb7e2d,
	}
	for n, w := range want {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i)
		}
		if got := siphash(k0, k1, msg); got != w {
			t.Fatalf("siphash len=%d: got %#016x, want %#016x", n, got, w)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("rng failure") }

func TestNewTableRandomSourceFailure(t *testing.T) {
	if _, err := NewTable(nil); err == nil {
		t.Fatal("nil random source: expected error, got nil")
	}
	if _, err := NewTable(errReader{}); err == nil {
		t.Fatal("failing random source: expected error, got nil")
	}
	// Short read: only 4 of 16 secret bytes available.
	if _, err := NewTable(bytes.NewReader([]byte{1, 2, 3, 4})); err == nil {
		t.Fatal("short random source: expected error, got nil")
	}
	// Exactly 16 bytes must succeed.
	if _, err := NewTable(bytes.NewReader(make([]byte, 16))); err != nil {
		t.Fatalf("valid random source: unexpected error: %v", err)
	}
}

func TestKeyLengthLimit(t *testing.T) {
	tab, err := NewTable(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Insert(make([]byte, MaxKeyLen), nil); err != nil {
		t.Fatalf("256-byte key rejected: %v", err)
	}
	if err := tab.Insert(make([]byte, MaxKeyLen+1), nil); !errors.Is(err, ErrKeyTooLong) {
		t.Fatalf("257-byte key: got %v, want ErrKeyTooLong", err)
	}
	if _, ok := tab.Lookup(make([]byte, MaxKeyLen+1)); ok {
		t.Fatal("lookup of oversized key succeeded")
	}
	if tab.Delete(make([]byte, MaxKeyLen+1)) {
		t.Fatal("delete of oversized key succeeded")
	}
}

// TestFullCollisionLogComparisons injects a constant hash so all keys
// land in one bucket, then proves insert/lookup/delete each cost
// O(log n) key comparisons.
func TestFullCollisionLogComparisons(t *testing.T) {
	const n = 1024
	tab := newTableWithHash(constantHash)

	keys := make([][]byte, n)
	for i := 0; i < n; i++ {
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, uint64(i))
		keys[i] = key
		if err := tab.Insert(key, key); err != nil {
			t.Fatal(err)
		}
	}
	if tab.Len() != n {
		t.Fatalf("Len = %d, want %d", tab.Len(), n)
	}
	if tab.buckets[0].tree == nil {
		t.Fatal("bucket did not switch to a tree under collision")
	}
	if got := tab.buckets[0].tree.size; got != n {
		t.Fatalf("tree size = %d, want %d", got, n)
	}

	// AVL height is bounded by 1.4405*log2(n+2); allow generous slack.
	logBound := uint64(3 * (bits.Len(uint(n)) + 1))

	tab.ResetCompareCount()
	var maxLookup uint64
	for _, key := range keys {
		before := tab.CompareCount()
		if _, ok := tab.Lookup(key); !ok {
			t.Fatalf("key %v missing after collision inserts", key)
		}
		if c := tab.CompareCount() - before; c > maxLookup {
			maxLookup = c
		}
	}
	t.Logf("full collision: n=%d, max lookup comparisons=%d, log2 bound=%d (list scan would need up to %d)",
		n, maxLookup, logBound, n)
	if maxLookup > logBound {
		t.Fatalf("lookup comparisons %d exceed logarithmic bound %d", maxLookup, logBound)
	}

	tab.ResetCompareCount()
	var maxDelete uint64
	for _, key := range keys {
		before := tab.CompareCount()
		if !tab.Delete(key) {
			t.Fatalf("delete of %v failed", key)
		}
		if c := tab.CompareCount() - before; c > maxDelete {
			maxDelete = c
		}
	}
	t.Logf("full collision: n=%d, max delete comparisons=%d (2x log bound=%d)",
		n, maxDelete, 2*logBound)
	if maxDelete > 2*logBound {
		t.Fatalf("delete comparisons %d exceed logarithmic bound %d", maxDelete, 2*logBound)
	}
	if tab.Len() != 0 {
		t.Fatalf("Len = %d after deleting all keys, want 0", tab.Len())
	}
}

// TestLongCommonPrefix uses 256-byte keys sharing a 255-byte prefix.
// Under a constant hash, correct behavior proves the tree orders by
// raw key bytes: ordering by hash alone would make keys indistinguishable.
func TestLongCommonPrefix(t *testing.T) {
	const n = 200
	tab := newTableWithHash(constantHash)

	prefix := bytes.Repeat([]byte("p"), MaxKeyLen-1)
	makeKey := func(i int) []byte {
		key := make([]byte, MaxKeyLen)
		copy(key, prefix)
		key[MaxKeyLen-1] = byte(i)
		return key
	}

	for i := 0; i < n; i++ {
		if err := tab.Insert(makeKey(i), []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}

	// In-order traversal must yield keys sorted by raw bytes.
	var got [][]byte
	tab.buckets[0].tree.forEach(func(key, _ []byte) {
		got = append(got, append([]byte(nil), key...))
	})
	if len(got) != n || !sort.IsSorted(sortBytes(got)) {
		t.Fatal("tree is not ordered by raw key bytes")
	}

	for i := 0; i < n; i++ {
		v, ok := tab.Lookup(makeKey(i))
		if !ok || !bytes.Equal(v, []byte{byte(i)}) {
			t.Fatalf("lookup %d: ok=%v v=%v", i, ok, v)
		}
	}
	for i := 0; i < n; i += 2 {
		if !tab.Delete(makeKey(i)) {
			t.Fatalf("delete %d failed", i)
		}
	}
	for i := 0; i < n; i++ {
		_, ok := tab.Lookup(makeKey(i))
		if want := i%2 == 1; ok != want {
			t.Fatalf("lookup %d after deletes: ok=%v, want %v", i, ok, want)
		}
	}
}

type sortBytes [][]byte

func (s sortBytes) Len() int           { return len(s) }
func (s sortBytes) Less(i, j int) bool { return bytes.Compare(s[i], s[j]) < 0 }
func (s sortBytes) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// TestConcurrentGrowDeleteLookup hammers the table with concurrent
// inserts (forcing repeated growth), lookups, and deletes, then checks
// the final state is exactly the linearizable outcome.
func TestConcurrentGrowDeleteLookup(t *testing.T) {
	tab, err := NewTable(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 500

	key := func(w, i int) []byte {
		return []byte(fmt.Sprintf("tenant-%d-key-%d", w, i))
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := tab.Insert(key(w, i), key(w, i)); err != nil {
					t.Errorf("insert: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	if got, want := tab.Len(), workers*perWorker; got != want {
		t.Fatalf("Len = %d, want %d", got, want)
	}

	// Concurrently look up odd keys (never deleted) while deleting
	// even keys; every observed state must be linearizable.
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func(w int) {
			defer wg.Done()
			for i := 1; i < perWorker; i += 2 {
				if _, ok := tab.Lookup(key(w, i)); !ok {
					t.Errorf("lookup of live key %q failed", key(w, i))
				}
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i += 2 {
				tab.Delete(key(w, i))
			}
		}(w)
	}
	wg.Wait()

	if got, want := tab.Len(), workers*perWorker/2; got != want {
		t.Fatalf("Len after deletes = %d, want %d", got, want)
	}
	for w := 0; w < workers; w++ {
		for i := 0; i < perWorker; i++ {
			_, ok := tab.Lookup(key(w, i))
			if want := i%2 == 1; ok != want {
				t.Fatalf("final lookup %q: ok=%v, want %v", key(w, i), ok, want)
			}
		}
	}
}

// TestListToTreeTransition verifies the bucket switches from a list to
// a tree exactly when it grows past bucketListMax entries.
func TestListToTreeTransition(t *testing.T) {
	tab := newTableWithHash(constantHash)
	for i := 0; i < bucketListMax; i++ {
		if err := tab.Insert([]byte{byte(i)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if tab.buckets[0].tree != nil {
		t.Fatal("bucket became a tree at or below 8 entries")
	}
	if err := tab.Insert([]byte{byte(bucketListMax)}, nil); err != nil {
		t.Fatal(err)
	}
	if tab.buckets[0].tree == nil {
		t.Fatal("bucket did not become a tree above 8 entries")
	}
	for i := 0; i <= bucketListMax; i++ {
		if _, ok := tab.Lookup([]byte{byte(i)}); !ok {
			t.Fatalf("key %d lost during tree transition", i)
		}
	}
}

// TestPerTableSecret verifies that two tables built from different
// random sources hash the same key differently (independent secrets).
func TestPerTableSecret(t *testing.T) {
	secretA := bytes.Repeat([]byte("A"), 16)
	secretB := bytes.Repeat([]byte("B"), 16)
	tabA, err := NewTable(bytes.NewReader(secretA))
	if err != nil {
		t.Fatal(err)
	}
	tabB, err := NewTable(bytes.NewReader(secretB))
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("attacker-controlled")
	if tabA.hashFn(key) == tabB.hashFn(key) {
		t.Fatal("distinct secrets produced identical hashes")
	}
}
