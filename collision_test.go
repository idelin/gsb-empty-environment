package rehash

import (
	"testing"
)

// constHash forces every key into a single bucket: the worst-case
// controlled collision scenario.
func constHash(int) uint64 { return 7 }

// modHash spreads keys over exactly 4 buckets.
func modHash(i int) uint64 { return uint64(i%4) * 0x1000000000000000 }

// TestConstantCollisionCorrectness hammers the table when every key
// collides into one chain, including through migration.
func TestConstantCollisionCorrectness(t *testing.T) {
	tab := New[int, int](constHash, WithMigrateChunk(1))
	const n = 200
	for i := 0; i < n; i++ {
		tab.Put(i, i)
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	for i := 0; i < n; i++ {
		if v, ok := tab.Get(i); !ok || v != i {
			t.Fatalf("Get(%d) = %v,%v", i, v, ok)
		}
	}
	// Delete half, re-put a quarter, verify no resurrection and one value.
	for i := 0; i < n; i += 2 {
		tab.Delete(i)
	}
	for i := 0; i < n; i += 4 {
		tab.Put(i, -i)
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	for i := 0; i < n; i++ {
		v, ok := tab.Get(i)
		switch {
		case i%4 == 0:
			if !ok || v != -i {
				t.Fatalf("Get(%d) = %v,%v want %d,true", i, v, ok, -i)
			}
		case i%2 == 0:
			if ok {
				t.Fatalf("deleted key %d resurrected with value %d", i, v)
			}
		default:
			if !ok || v != i {
				t.Fatalf("Get(%d) = %v,%v want %d,true", i, v, ok, i)
			}
		}
	}
	snap := tab.Snapshot()
	if snap.Len() != tab.Len() {
		t.Fatalf("snapshot len %d != table len %d", snap.Len(), tab.Len())
	}
}

// TestCollisionWorkRecorded checks that the worst single-step work metric
// actually reflects the giant chain move caused by total collision.
func TestCollisionWorkRecorded(t *testing.T) {
	tab := New[int, int](constHash, WithMigrateChunk(1))
	const n = 100
	for i := 0; i < n; i++ {
		tab.Put(i, i)
	}
	st := tab.Stats()
	// With all keys in one chain, some operation must have moved or
	// scanned a chain of size >= n/2 in a single step.
	if st.WorstOpWork < n/2 {
		t.Fatalf("WorstOpWork = %d, expected >= %d under total collision", st.WorstOpWork, n/2)
	}
	t.Logf("collision worst single-step work: %d units (n=%d)", st.WorstOpWork, n)
}

// TestBoundedWorkWithGoodHash verifies per-operation work stays small when
// the hash spreads keys well: migration moves at most `chunk` buckets per
// op and chains stay short.
func TestBoundedWorkWithGoodHash(t *testing.T) {
	const chunk = 2
	tab := New[int, int](IntHash, WithMigrateChunk(chunk))
	const n = 2000
	for i := 0; i < n; i++ {
		tab.Put(i, i)
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	st := tab.Stats()
	// chunk bucket visits + at most a handful of entry moves/probes each.
	// With a good hash, chains are O(1): allow generous slack.
	const bound = chunk + 16
	if st.WorstOpWork > bound {
		t.Fatalf("WorstOpWork = %d exceeds bound %d for a good hash", st.WorstOpWork, bound)
	}
	t.Logf("good-hash worst single-step work: %d units (bound %d)", st.WorstOpWork, bound)
}

// TestPartialCollisionMigration uses modHash so exactly 4 buckets hold all
// keys; migration must still preserve every entry exactly once.
func TestPartialCollisionMigration(t *testing.T) {
	tab := New[int, int](modHash, WithMigrateChunk(1))
	const n = 500
	for i := 0; i < n; i++ {
		tab.Put(i, i*10)
	}
	// Verify mid-migration reads on every key.
	if tab.Migrating() {
		for i := 0; i < n; i++ {
			if v, ok := tab.Get(i); !ok || v != i*10 {
				t.Fatalf("mid-migration Get(%d) = %v,%v", i, v, ok)
			}
		}
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	snap := tab.Snapshot()
	seen := make(map[int]int)
	for k, v, ok := snap.Next(); ok; k, v, ok = snap.Next() {
		seen[k]++
		if v != k*10 {
			t.Fatalf("snapshot[%d] = %d want %d", k, v, k*10)
		}
	}
	if len(seen) != n {
		t.Fatalf("snapshot has %d distinct keys, want %d", len(seen), n)
	}
	for k, c := range seen {
		if c != 1 {
			t.Fatalf("key %d yielded %d times", k, c)
		}
	}
}
