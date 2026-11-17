package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"sync"
	"testing"

	netsec "example.org/netsec/q-013"
)

// fixedReader returns a deterministic random source for reproducible
// table secrets.
func fixedReader(seed byte) *bytes.Reader {
	secret := bytes.Repeat([]byte{seed}, 16)
	return bytes.NewReader(secret)
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

type shortReader struct{}

func (shortReader) Read(p []byte) (int, error) { return 1, nil } // never fills p

func constantHash([]byte) uint64 { return 0 }

func makeKey(i int) []byte {
	var k [8]byte
	for j := 0; j < 8; j++ {
		k[j] = byte(i >> (8 * j))
	}
	return k[:]
}

func TestNewRejectsBadRandomSource(t *testing.T) {
	if tbl, err := netsec.New(nil); err == nil || tbl != nil {
		t.Fatalf("New(nil) = %v, %v; want error and nil table", tbl, err)
	}
	wantErr := errors.New("entropy exhausted")
	if tbl, err := netsec.New(errReader{err: wantErr}); !errors.Is(err, wantErr) || tbl != nil {
		t.Fatalf("New(errReader) = %v, %v; want wrapped %v and nil table", tbl, err, wantErr)
	}
	if tbl, err := netsec.New(bytes.NewReader([]byte{1, 2, 3})); err == nil || tbl != nil {
		t.Fatalf("New(short source) = %v, %v; want error and nil table", tbl, err)
	}
	// A reader that trickles one byte per Read must still work via
	// io.ReadFull, proving the full 16-byte secret is consumed.
	if tbl, err := netsec.New(shortReader{}); err != nil || tbl == nil {
		t.Fatalf("New(trickling source) = %v, %v; want table", tbl, err)
	}
}

func TestBasicOperations(t *testing.T) {
	tbl, err := netsec.New(fixedReader(0x01))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tbl.Get([]byte("missing")); ok {
		t.Fatal("Get on empty table reported a hit")
	}
	if err := tbl.Insert([]byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := tbl.Insert([]byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if got, ok := tbl.Get([]byte("k")); !ok || string(got) != "v2" {
		t.Fatalf("Get = %q, %v; want v2, true", got, ok)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d, want 1", tbl.Len())
	}
	if !tbl.Delete([]byte("k")) {
		t.Fatal("Delete of existing key returned false")
	}
	if tbl.Delete([]byte("k")) {
		t.Fatal("Delete of removed key returned true")
	}
	if tbl.Len() != 0 {
		t.Fatalf("Len = %d, want 0", tbl.Len())
	}

	tooLong := make([]byte, netsec.MaxKeyLen+1)
	if err := tbl.Insert(tooLong, nil); !errors.Is(err, netsec.ErrKeyTooLong) {
		t.Fatalf("Insert(257-byte key) err = %v, want ErrKeyTooLong", err)
	}
	if _, ok := tbl.Get(tooLong); ok {
		t.Fatal("Get of overlong key reported a hit")
	}
	if tbl.Delete(tooLong) {
		t.Fatal("Delete of overlong key returned true")
	}

	// A 256-byte key is accepted.
	maxKey := make([]byte, netsec.MaxKeyLen)
	if err := tbl.Insert(maxKey, []byte("x")); err != nil {
		t.Fatalf("Insert(256-byte key) err = %v", err)
	}
	if _, ok := tbl.Get(maxKey); !ok {
		t.Fatal("Get of 256-byte key missed")
	}
}

func TestPerTableSecretChangesHash(t *testing.T) {
	a, err := netsec.New(fixedReader(0xAA))
	if err != nil {
		t.Fatal(err)
	}
	b, err := netsec.New(fixedReader(0xBB))
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("attacker-controlled")
	if a.HashOf(key) == b.HashOf(key) {
		t.Fatal("tables with distinct secrets produced identical hashes")
	}
}

// TestFullCollisionLogarithmic forces every key into a single bucket via
// a constant hash and proves that per-operation key comparisons stay
// logarithmic in the table size.
func TestFullCollisionLogarithmic(t *testing.T) {
	const n = 4096
	tbl, err := netsec.NewForTest(fixedReader(0x02), constantHash)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < n; i++ {
		if err := tbl.Insert(makeKey(i), makeKey(i)); err != nil {
			t.Fatal(err)
		}
	}
	if tbl.TreeBucketCount() == 0 {
		t.Fatal("overfull bucket was not converted to a tree")
	}
	if tbl.Len() != n {
		t.Fatalf("Len = %d, want %d", tbl.Len(), n)
	}

	log2n := bits.Len(uint(n - 1)) // ceil(log2(n)) for n a power of two
	// AVL height is bounded by 1.44*log2(size+2); deletion additionally
	// walks to the in-order successor. A bound of 4*log2(n)+16 compares
	// per operation is far above any balanced-tree operation yet far
	// below the O(n) a list would need (n compares at size n).
	perOpBound := int64(4*log2n + 16)

	var maxGet, maxInsert, maxDelete int64
	for i := 0; i < n; i++ {
		tbl.ResetCompares()
		if got, ok := tbl.Get(makeKey(i)); !ok || !bytes.Equal(got, makeKey(i)) {
			t.Fatalf("Get(%d) failed under full collision", i)
		}
		if c := tbl.Compares(); c > maxGet {
			maxGet = c
		}
	}
	for i := 0; i < n; i++ {
		tbl.ResetCompares()
		if err := tbl.Insert(makeKey(i), makeKey(i+1)); err != nil {
			t.Fatal(err)
		}
		if c := tbl.Compares(); c > maxInsert {
			maxInsert = c
		}
	}
	for i := 0; i < n; i++ {
		tbl.ResetCompares()
		if !tbl.Delete(makeKey(i)) {
			t.Fatalf("Delete(%d) failed under full collision", i)
		}
		if c := tbl.Compares(); c > maxDelete {
			maxDelete = c
		}
	}
	if tbl.Len() != 0 {
		t.Fatalf("Len = %d after deleting everything", tbl.Len())
	}

	t.Logf("full collision, n=%d: max compares per op: get=%d insert=%d delete=%d (bound=%d, linear would be ~%d)",
		n, maxGet, maxInsert, maxDelete, perOpBound, n)
	for name, got := range map[string]int64{"get": maxGet, "insert": maxInsert, "delete": maxDelete} {
		if got > perOpBound {
			t.Fatalf("%s used %d compares, exceeds logarithmic bound %d", name, got, perOpBound)
		}
	}
}

// TestLongCommonPrefix uses 256-byte keys sharing a 255-byte prefix
// under a constant hash: the tree must order by raw key bytes, so all
// keys remain distinct and lookups stay logarithmic.
func TestLongCommonPrefix(t *testing.T) {
	const n = 512
	tbl, err := netsec.NewForTest(fixedReader(0x03), constantHash)
	if err != nil {
		t.Fatal(err)
	}
	prefix := bytes.Repeat([]byte{0xAB}, netsec.MaxKeyLen-2)
	key := func(i int) []byte {
		k := append([]byte(nil), prefix...)
		return append(k, byte(i>>8), byte(i))
	}
	for i := 0; i < n; i++ {
		if err := tbl.Insert(key(i), makeKey(i)); err != nil {
			t.Fatal(err)
		}
	}
	if tbl.Len() != n {
		t.Fatalf("Len = %d, want %d: keys ordered by hash alone would collapse", tbl.Len(), n)
	}
	log2n := bits.Len(uint(n - 1))
	perOpBound := int64(4*log2n + 16)
	var maxGet int64
	for i := 0; i < n; i++ {
		tbl.ResetCompares()
		got, ok := tbl.Get(key(i))
		if !ok || !bytes.Equal(got, makeKey(i)) {
			t.Fatalf("Get(%d) failed for long-common-prefix key", i)
		}
		if c := tbl.Compares(); c > maxGet {
			maxGet = c
		}
	}
	t.Logf("long common prefix, n=%d: max get compares=%d (bound=%d)", n, maxGet, perOpBound)
	if maxGet > perOpBound {
		t.Fatalf("get used %d compares, exceeds logarithmic bound %d", maxGet, perOpBound)
	}
	for i := 0; i < n; i += 2 {
		if !tbl.Delete(key(i)) {
			t.Fatalf("Delete(%d) failed", i)
		}
	}
	for i := 1; i < n; i += 2 {
		if _, ok := tbl.Get(key(i)); !ok {
			t.Fatalf("Get(%d) missed after deleting neighbours", i)
		}
	}
	if tbl.Len() != n/2 {
		t.Fatalf("Len = %d, want %d", tbl.Len(), n/2)
	}
}

// TestGrowthRace hammers a production-hash table with concurrent
// inserts, deletes and lookups across many doublings. Run with -race.
func TestGrowthRace(t *testing.T) {
	tbl, err := netsec.New(fixedReader(0x04))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 2000
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := w * perWorker
			for i := 0; i < perWorker; i++ {
				k := makeKey(base + i)
				if err := tbl.Insert(k, k); err != nil {
					t.Errorf("Insert: %v", err)
					return
				}
				if i%3 == 0 {
					tbl.Delete(makeKey(base + i/2))
				}
				if i%5 == 0 {
					tbl.Get(makeKey(base + i/2))
				}
			}
		}(w)
	}
	wg.Wait()

	// Consistency check: every key the table reports is retrievable,
	// and Len agrees with an exact accounting of live keys.
	live := 0
	for i := 0; i < workers*perWorker; i++ {
		if _, ok := tbl.Get(makeKey(i)); ok {
			live++
		}
	}
	if tbl.Len() != live {
		t.Fatalf("Len = %d, but %d keys are retrievable", tbl.Len(), live)
	}
	if tbl.BucketCount() < 16 {
		t.Fatalf("table did not grow under load: buckets=%d", tbl.BucketCount())
	}
	t.Logf("after concurrent growth: buckets=%d entries=%d", tbl.BucketCount(), tbl.Len())
}

// TestConcurrentSameKey checks linearizable behaviour when many
// goroutines race on one key: the final state must equal some
// sequential interleaving of the operations.
func TestConcurrentSameKey(t *testing.T) {
	tbl, err := netsec.New(fixedReader(0x05))
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("hot-key")
	const writers = 16
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			v := []byte{byte(w)}
			for r := 0; r < rounds; r++ {
				if err := tbl.Insert(key, v); err != nil {
					t.Errorf("Insert: %v", err)
					return
				}
				tbl.Get(key)
				tbl.Delete(key)
			}
		}(w)
	}
	wg.Wait()
	// Whatever the interleaving, the key maps to at most one entry and
	// any stored value is one of the written values.
	if n := tbl.Len(); n > 1 {
		t.Fatalf("Len = %d, want 0 or 1 for a single hot key", n)
	}
	if got, ok := tbl.Get(key); ok {
		if len(got) != 1 || int(got[0]) >= writers {
			t.Fatalf("stored value %v was never written", got)
		}
	}
}

// TestNoSecretLeak verifies the table secret never appears in formatted
// output or errors.
func TestNoSecretLeak(t *testing.T) {
	secret := []byte("TOP-SECRET-12345") // 16 bytes
	tbl, err := netsec.New(bytes.NewReader(secret))
	if err != nil {
		t.Fatal(err)
	}
	if err := tbl.Insert([]byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	outputs := []string{
		fmt.Sprintf("%v", tbl),
		fmt.Sprintf("%+v", tbl),
		fmt.Sprintf("%#v", tbl),
		fmt.Sprint(tbl),
		tbl.String(),
		fmt.Sprint(netsec.ErrKeyTooLong),
	}
	for _, out := range outputs {
		if bytes.Contains([]byte(out), secret) {
			t.Fatalf("output %q leaks the table secret", out)
		}
	}
	t.Logf("formatted table: %s", tbl)
}
