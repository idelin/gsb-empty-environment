package skiplist

import (
	"math/rand"
	"sync"
	"testing"
)

// insertConcurrent shuffles keys [0, n) and inserts them from multiple
// goroutines, exercising the CAS splice paths under contention.
func insertConcurrent(tb testing.TB, sl *SkipList, n int) {
	tb.Helper()
	keys := rand.Perm(n)
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += workers {
				sl.Insert(int64(keys[i]), int64(keys[i]*10))
			}
		}(w)
	}
	wg.Wait()
}

// Acceptance 1: 10,000 random integer key-value pairs inserted concurrently.
func TestConcurrentRandomInsert10k(t *testing.T) {
	sl := New()
	const n = 10000
	insertConcurrent(t, sl, n)

	if got := sl.Len(); got != n {
		t.Fatalf("Len() = %d, want %d", got, n)
	}
	for i := 0; i < n; i++ {
		v, ok := sl.Get(int64(i))
		if !ok {
			t.Fatalf("key %d missing after concurrent insert", i)
		}
		if want := int64(i * 10); v != want {
			t.Fatalf("key %d: value = %d, want %d", i, v, want)
		}
	}
}

// Acceptance 2: iterators over arbitrary intervals yield strictly
// monotonically increasing keys that all lie inside the interval.
func TestIteratorStrictlyIncreasingOverRanges(t *testing.T) {
	sl := New()
	const n = 10000
	insertConcurrent(t, sl, n)

	ranges := [][2]int64{
		{0, n - 1},      // full range
		{0, 0},          // single element
		{1234, 8765},    // arbitrary interior interval
		{-100, 50},      // start below minimum key
		{n - 50, n + 9}, // end above maximum key
		{5000, 4000},    // empty: start > end
		{n, n + 100},    // empty: beyond maximum key
	}
	for _, r := range ranges {
		start, end := r[0], r[1]
		var prev int64
		count := 0
		for it := sl.NewIterator(start, end); it.Valid(); it.Next() {
			k := it.Key()
			if k < start || k > end {
				t.Fatalf("range [%d,%d]: key %d out of bounds", start, end, k)
			}
			if count > 0 && k <= prev {
				t.Fatalf("range [%d,%d]: keys not strictly increasing: %d then %d", start, end, prev, k)
			}
			if v := it.Value(); v != k*10 {
				t.Fatalf("key %d: iterator value = %d, want %d", k, v, k*10)
			}
			prev = k
			count++
		}
		want := 0
		if start <= end {
			lo := max(start, 0)
			hi := min(end, int64(n-1))
			if lo <= hi {
				want = int(hi - lo + 1)
			}
		}
		if count != want {
			t.Fatalf("range [%d,%d]: iterated %d keys, want %d", start, end, count, want)
		}
	}
}

// Acceptance 3: while an iterator is being consumed, new data is inserted
// continuously; the iterator's result set must remain fully stable and
// isolated from those writes.
func TestIteratorSnapshotIsolationUnderConcurrentWrites(t *testing.T) {
	sl := New()
	const base = 10000
	for i := 0; i < base; i++ {
		sl.Insert(int64(i), int64(i))
	}

	// Snapshot iterator created BEFORE the extra writes begin.
	it := sl.NewIterator(0, 2*base)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Continuously insert brand-new keys (and update existing ones)
		// for the whole duration of the traversal.
		for i := base; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			sl.Insert(int64(i), int64(i))
			sl.Insert(int64(i%base), int64(-i)) // overwrite existing keys too
		}
	}()

	count := 0
	var prev int64 = -1
	for ; it.Valid(); it.Next() {
		k := it.Key()
		if k < 0 || k >= base {
			t.Fatalf("snapshot iterator leaked concurrently inserted key %d", k)
		}
		if k <= prev {
			t.Fatalf("iterator output not strictly increasing: %d then %d", prev, k)
		}
		prev = k
		count++
	}
	close(stop)
	wg.Wait()

	if count != base {
		t.Fatalf("snapshot iterator yielded %d keys, want exactly %d", count, base)
	}
}

// A second iterator created after the writes must observe the new keys,
// proving isolation comes from the snapshot, not a frozen structure.
func TestIteratorSeesWritesAfterCreation(t *testing.T) {
	sl := New()
	for i := 0; i < 100; i++ {
		sl.Insert(int64(i), int64(i))
	}
	old := sl.NewIterator(0, 1000)
	for i := 100; i < 200; i++ {
		sl.Insert(int64(i), int64(i))
	}
	fresh := sl.NewIterator(0, 1000)

	countKeys := func(it *Iterator) int {
		c := 0
		for ; it.Valid(); it.Next() {
			c++
		}
		return c
	}
	if got := countKeys(old); got != 100 {
		t.Fatalf("old iterator saw %d keys, want 100", got)
	}
	if got := countKeys(fresh); got != 200 {
		t.Fatalf("fresh iterator saw %d keys, want 200", got)
	}
}

func TestDuplicateInsertUpdatesValue(t *testing.T) {
	sl := New()
	sl.Insert(42, 1)
	sl.Insert(42, 2)
	if v, ok := sl.Get(42); !ok || v != 2 {
		t.Fatalf("Get(42) = %d, %v; want 2, true", v, ok)
	}
	if got := sl.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
}

func BenchmarkConcurrentInsert(b *testing.B) {
	sl := New()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sl.Insert(int64(i), int64(i))
			i++
		}
	})
}
