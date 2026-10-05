package ghtable_test

import (
	"fmt"
	"maps"
	"math/rand"
	"testing"

	"ghtable"
)

// Controlled hashers: they decide exactly which bucket a key lands in, so
// tests can force collisions and pick keys in not-yet-migrated buckets.
func identityHash(k int) uint64 { return uint64(k) }
func constHash(int) uint64      { return 0 }
func modHash(m uint64) ghtable.Hasher[int] {
	return func(k int) uint64 { return uint64(k) % m }
}

func collect[K comparable, V any](t *testing.T, s *ghtable.Snapshot[K, V]) map[K]V {
	t.Helper()
	got := make(map[K]V, s.Len())
	it := s.Iterator()
	for it.Next() {
		if _, dup := got[it.Key()]; dup {
			t.Fatalf("duplicate key %v in snapshot iteration", it.Key())
		}
		got[it.Key()] = it.Value()
	}
	if len(got) != s.Len() {
		t.Fatalf("snapshot Len=%d but iterator yielded %d keys", s.Len(), len(got))
	}
	return got
}

func TestBasicOperations(t *testing.T) {
	tbl := ghtable.New[int, string]()
	if _, ok := tbl.Get(1); ok {
		t.Fatal("Get on empty table returned ok")
	}
	tbl.Set(1, "one")
	tbl.Set(1, "uno") // overwrite: only one visible value
	if v, ok := tbl.Get(1); !ok || v != "uno" {
		t.Fatalf("Get(1) = %q,%v, want uno,true", v, ok)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d, want 1", tbl.Len())
	}
	if !tbl.Delete(1) {
		t.Fatal("Delete(1) = false, want true")
	}
	if tbl.Delete(1) {
		t.Fatal("Delete of missing key = true, want false")
	}
	if _, ok := tbl.Get(1); ok {
		t.Fatal("deleted key still visible")
	}
	if tbl.Len() != 0 {
		t.Fatalf("Len = %d, want 0", tbl.Len())
	}
}

func TestCollisionChain(t *testing.T) {
	// Every key collides into bucket 0.
	tbl := ghtable.New[int, int](ghtable.Config[int]{Hasher: constHash})
	const n = 50
	for i := 0; i < n; i++ {
		tbl.Set(i, i*i)
	}
	for i := 0; i < n; i++ {
		if v, ok := tbl.Get(i); !ok || v != i*i {
			t.Fatalf("Get(%d) = %d,%v, want %d,true", i, v, ok, i*i)
		}
	}
	for i := 0; i < n; i += 2 {
		if !tbl.Delete(i) {
			t.Fatalf("Delete(%d) failed", i)
		}
	}
	for i := 0; i < n; i++ {
		_, ok := tbl.Get(i)
		if want := i%2 != 0; ok != want {
			t.Fatalf("Get(%d) ok = %v, want %v", i, ok, want)
		}
	}
}

func TestMigrationStartsAndProgresses(t *testing.T) {
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 8; i++ {
		tbl.Set(i, i)
	}
	if !tbl.Migrating() {
		t.Fatal("expected migration after filling 8 buckets")
	}
	prev := -1
	for i := 8; tbl.Migrating(); i++ {
		tbl.Set(i, i) // each op migrates exactly one bucket
		st := tbl.Stats()
		if st.Migrating && st.BucketsMigrated < prev {
			t.Fatalf("progress regressed: %d -> %d", prev, st.BucketsMigrated)
		}
		prev = st.BucketsMigrated
		if st.Migrating && st.NewBuckets == 0 {
			t.Fatal("Stats.NewBuckets = 0 while migrating")
		}
		if i > 100 {
			t.Fatal("migration did not converge")
		}
	}
	if st := tbl.Stats(); st.Progress != 1 || st.Migrating {
		t.Fatalf("after drain: progress=%v migrating=%v", st.Progress, st.Migrating)
	}
	for i := 0; i < 16; i++ {
		if v, ok := tbl.Get(i); !ok || v != i {
			t.Fatalf("Get(%d) = %d,%v after migration", i, v, ok)
		}
	}
}

func TestGetDrivesMigration(t *testing.T) {
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 8; i++ {
		tbl.Set(i, i)
	}
	for i := 0; i < 8 && tbl.Migrating(); i++ {
		tbl.Get(0) // pure reads still advance migration
	}
	if tbl.Migrating() {
		t.Fatal("reads did not drive migration to completion")
	}
}

func TestTombstonePreventsResurrection(t *testing.T) {
	tbl := ghtable.New[int, string](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 8; i++ {
		tbl.Set(i, fmt.Sprintf("v%d", i))
	}
	if !tbl.Migrating() {
		t.Fatal("expected migration in flight")
	}
	// Key 3 lives in old bucket 3, which has not migrated yet. Deleting it
	// writes a tombstone into the new level.
	if !tbl.Delete(3) {
		t.Fatal("Delete(3) failed")
	}
	if _, ok := tbl.Get(3); ok {
		t.Fatal("deleted key visible before migration completes")
	}
	// Force the stale old-level entry through migration; the tombstone must
	// stop it from coming back.
	tbl.MigrateAll()
	if _, ok := tbl.Get(3); ok {
		t.Fatal("tombstoned key resurrected by migration")
	}
	snap := tbl.Snapshot()
	if _, ok := collect(t, snap)[3]; ok {
		t.Fatal("snapshot iteration resurrected deleted key")
	}
	// Re-setting the key must yield exactly one visible value.
	tbl.Set(3, "new")
	if v, ok := tbl.Get(3); !ok || v != "new" {
		t.Fatalf("Get(3) = %q,%v, want new,true", v, ok)
	}
	tbl.MigrateAll()
	got := collect(t, tbl.Snapshot())
	if got[3] != "new" || len(got) != 8 {
		t.Fatalf("snapshot = %v, want 8 keys with 3->new", got)
	}
}

func TestDuplicateUpdateSingleVisibleValue(t *testing.T) {
	tbl := ghtable.New[int, string](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 8; i++ {
		tbl.Set(i, "old")
	}
	// Key 5 is stale in the old level; repeated updates go to the new level.
	tbl.Set(5, "a")
	tbl.Set(5, "b")
	tbl.Set(5, "c")
	if v, _ := tbl.Get(5); v != "c" {
		t.Fatalf("Get(5) = %q, want c", v)
	}
	if got := collect(t, tbl.Snapshot()); got[5] != "c" {
		t.Fatalf("mid-migration snapshot has 5->%q, want c", got[5])
	}
	tbl.MigrateAll()
	got := collect(t, tbl.Snapshot())
	if got[5] != "c" {
		t.Fatalf("post-migration snapshot has 5->%q, want c", got[5])
	}
	if tbl.Len() != 8 {
		t.Fatalf("Len = %d, want 8", tbl.Len())
	}
}

func TestSnapshotStableDuringMutation(t *testing.T) {
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 12; i++ {
		tbl.Set(i, i)
	}
	if !tbl.Migrating() {
		t.Fatal("expected migration in flight")
	}
	snap := tbl.Snapshot()
	want := collect(t, snap)
	if len(want) != 12 {
		t.Fatalf("snapshot has %d keys, want 12", len(want))
	}
	ver := snap.Version()

	// Hammer the table: writes, deletes, and a forced drain.
	for i := 100; i < 300; i++ {
		tbl.Set(i, i)
	}
	for i := 0; i < 6; i++ {
		tbl.Delete(i)
	}
	tbl.MigrateAll()

	if tbl.Version() == ver {
		t.Fatal("table version did not advance after writes")
	}
	if snap.Version() != ver {
		t.Fatal("snapshot version changed")
	}
	if got := collect(t, snap); !maps.Equal(got, want) {
		t.Fatalf("snapshot drifted under mutation: got %v, want %v", got, want)
	}
}

func TestModelVsReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         modHash(13), // heavy controlled collisions
		InitialBuckets: 8,
		MigrateBudget:  2,
	})
	ref := make(map[int]int)
	for i := 0; i < 5000; i++ {
		k := rng.Intn(300)
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			v := rng.Int()
			tbl.Set(k, v)
			ref[k] = v
		case 5, 6, 7:
			tbl.Delete(k)
			delete(ref, k)
		default:
			got, ok := tbl.Get(k)
			want, wok := ref[k]
			if ok != wok || (ok && got != want) {
				t.Fatalf("op %d: Get(%d) = %d,%v, want %d,%v", i, k, got, ok, want, wok)
			}
		}
		if tbl.Len() != len(ref) {
			t.Fatalf("op %d: Len = %d, want %d", i, tbl.Len(), len(ref))
		}
		if i%97 == 0 {
			if got := collect(t, tbl.Snapshot()); !maps.Equal(got, ref) {
				t.Fatalf("op %d: snapshot mismatch vs reference", i)
			}
		}
	}
	tbl.MigrateAll()
	if got := collect(t, tbl.Snapshot()); !maps.Equal(got, ref) {
		t.Fatal("final snapshot mismatch vs reference")
	}
}

func TestBoundedStepWork(t *testing.T) {
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  3,
	})
	for i := 0; i < 500; i++ {
		tbl.Set(i, i)
		if st := tbl.Stats(); st.Metrics.MaxStepBuckets > 3 {
			t.Fatalf("op %d: single step migrated %d buckets, budget 3", i, st.Metrics.MaxStepBuckets)
		}
	}
	tbl.MigrateAll()
	st := tbl.Stats()
	if st.Metrics.MaxStepBuckets == 0 {
		t.Fatal("no migration work recorded")
	}
	if st.Metrics.EntriesMoved == 0 || st.Metrics.EntriesExamined < st.Metrics.EntriesMoved {
		t.Fatalf("inconsistent entry metrics: %+v", st.Metrics)
	}
	if st.Metrics.BucketsMigrated == 0 {
		t.Fatal("no bucket progress recorded")
	}
}

func TestWorstStepEntriesUnderCollision(t *testing.T) {
	// All keys collide in bucket 0; migrating that bucket must show up as
	// the worst single-step entry count.
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         constHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 8; i++ {
		tbl.Set(i, i)
	}
	tbl.Set(8, 8) // this op migrates old bucket 0 with its 8-entry chain
	st := tbl.Stats()
	if st.Metrics.MaxStepEntries != 8 {
		t.Fatalf("MaxStepEntries = %d, want 8", st.Metrics.MaxStepEntries)
	}
	if st.Metrics.MaxStepBuckets != 1 {
		t.Fatalf("MaxStepBuckets = %d, want 1", st.Metrics.MaxStepBuckets)
	}
	tbl.MigrateAll()
	for i := 0; i <= 8; i++ {
		if v, ok := tbl.Get(i); !ok || v != i {
			t.Fatalf("Get(%d) = %d,%v", i, v, ok)
		}
	}
}

func TestShrink(t *testing.T) {
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  4,
	})
	for i := 0; i < 200; i++ {
		tbl.Set(i, i)
	}
	tbl.MigrateAll()
	big := tbl.Stats().MainBuckets
	if big <= 8 {
		t.Fatalf("table did not grow: %d buckets", big)
	}
	for i := 0; i < 195; i++ {
		tbl.Delete(i)
	}
	tbl.MigrateAll()
	if got := tbl.Stats().MainBuckets; got >= big || got > 16 {
		t.Fatalf("MainBuckets = %d, want shrink below %d (and <= 16)", got, big)
	}
	for i := 195; i < 200; i++ {
		if v, ok := tbl.Get(i); !ok || v != i {
			t.Fatalf("Get(%d) = %d,%v after shrink", i, v, ok)
		}
	}
}

func TestTombstonesCleanedByRehash(t *testing.T) {
	tbl := ghtable.New[int, int](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	for i := 0; i < 8; i++ {
		tbl.Set(i, i)
	}
	for i := 0; i < 7; i++ {
		tbl.Delete(i) // tombstones into the new level
	}
	if st := tbl.Stats(); !st.Migrating || st.Tombstones != 7 {
		t.Fatalf("mid-migration: migrating=%v tombstones=%d, want true,7",
			st.Migrating, st.Tombstones)
	}
	// Migration (and the chained cleanup resize) carries live entries only.
	tbl.MigrateAll()
	if st := tbl.Stats(); st.Tombstones != 0 {
		t.Fatalf("Tombstones = %d after rehash, want 0", st.Tombstones)
	}
	if tbl.Len() != 1 {
		t.Fatalf("Len = %d, want 1", tbl.Len())
	}
	if _, ok := tbl.Get(7); !ok {
		t.Fatal("surviving key 7 lost during cleanup")
	}
}

func TestVersionSemantics(t *testing.T) {
	tbl := ghtable.New[int, string](ghtable.Config[int]{
		Hasher:         identityHash,
		InitialBuckets: 8,
		MigrateBudget:  1,
	})
	tbl.Set(1, "a")
	v := tbl.Version()
	tbl.Get(1)
	if tbl.Version() != v {
		t.Fatal("Get bumped the mapping version")
	}
	for i := 2; i <= 8; i++ {
		tbl.Set(i, "x")
	}
	v = tbl.Version()
	tbl.MigrateAll()
	if tbl.Version() != v {
		t.Fatal("migration bumped the mapping version")
	}
	if tbl.Delete(999) {
		t.Fatal("Delete of missing key = true")
	}
	if tbl.Version() != v {
		t.Fatal("failed Delete bumped the mapping version")
	}
	tbl.Delete(1)
	if tbl.Version() != v+1 {
		t.Fatal("Delete did not bump the mapping version")
	}
}

func TestEmptySnapshot(t *testing.T) {
	tbl := ghtable.New[int, int]()
	snap := tbl.Snapshot()
	if snap.Len() != 0 {
		t.Fatalf("Len = %d, want 0", snap.Len())
	}
	if it := snap.Iterator(); it.Next() {
		t.Fatal("iterator over empty snapshot yielded a pair")
	}
	n := 0
	snap.Range(func(int, int) bool { n++; return true })
	if n != 0 {
		t.Fatalf("Range visited %d pairs, want 0", n)
	}
}
