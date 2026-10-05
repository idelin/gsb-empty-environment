package rehash

import (
	"fmt"
	"testing"
)

func TestBasicPutGetDelete(t *testing.T) {
	tab := New[string, int](StringHash)
	if _, ok := tab.Get("a"); ok {
		t.Fatal("Get on empty table returned ok")
	}
	tab.Put("a", 1)
	tab.Put("b", 2)
	if v, ok := tab.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %v,%v want 1,true", v, ok)
	}
	tab.Put("a", 10) // update
	if v, ok := tab.Get("a"); !ok || v != 10 {
		t.Fatalf("Get(a) after update = %v,%v want 10,true", v, ok)
	}
	if tab.Len() != 2 {
		t.Fatalf("Len = %d want 2", tab.Len())
	}
	if !tab.Delete("a") {
		t.Fatal("Delete(a) = false want true")
	}
	if tab.Delete("a") {
		t.Fatal("second Delete(a) = true want false")
	}
	if _, ok := tab.Get("a"); ok {
		t.Fatal("Get(a) after delete returned ok")
	}
	if tab.Len() != 1 {
		t.Fatalf("Len = %d want 1", tab.Len())
	}
}

// // force a resize to happen during the test
func fill(t *testing.T, tab *Table[int, int], n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		tab.Put(i, i*i)
	}
}

func TestMigrationTriggeredAndCompletes(t *testing.T) {
	tab := New[int, int](IntHash, WithMigrateChunk(2))
	fill(t, tab, 500)
	// Migration must have started at some point; keep operating until done.
	ops := 0
	for tab.Migrating() {
		tab.Get(-1) // read ops also migrate a bounded chunk
		ops++
		if ops > 10000 {
			t.Fatal("migration did not terminate")
		}
	}
	if tab.Migrating() {
		t.Fatal("still migrating")
	}
	for i := 0; i < 500; i++ {
		if v, ok := tab.Get(i); !ok || v != i*i {
			t.Fatalf("Get(%d) = %v,%v want %d,true", i, v, ok, i*i)
		}
	}
	st := tab.Stats()
	if st.MigratedEntries == 0 {
		t.Fatal("MigratedEntries = 0, expected migration activity")
	}
	if st.WorstOpWork <= 0 {
		t.Fatal("WorstOpWork not recorded")
	}
}

func TestProgressMonotonicAndBounded(t *testing.T) {
	const chunk = 3
	tab := New[int, int](IntHash, WithMigrateChunk(chunk))
	fill(t, tab, 200)
	if !tab.Migrating() {
		t.Skip("resize did not trigger with 200 entries")
	}
	prev := -1
	_, total := tab.Progress()
	for tab.Migrating() {
		m, tot := tab.Progress()
		if tot != total {
			t.Fatalf("total buckets changed mid-migration: %d -> %d", total, tot)
		}
		if m <= prev && m != 0 {
			t.Fatalf("progress not monotonic: %d after %d", m, prev)
		}
		prev = m
		tab.Get(-1)
	}
	// Each op migrates at most chunk buckets, so with B buckets migration
	// needs at least ceil(B/chunk) ops — verify it actually finished.
	if tab.Migrating() {
		t.Fatal("migration incomplete")
	}
}

func TestSingleVisibleValueDuringMigration(t *testing.T) {
	tab := New[int, int](IntHash, WithMigrateChunk(1))
	fill(t, tab, 100) // trigger migration
	if !tab.Migrating() {
		t.Skip("no migration in flight")
	}
	// Hammer the same key with updates while migration proceeds.
	for round := 0; round < 50 && tab.Migrating(); round++ {
		tab.Put(7, 1000+round)
		v, ok := tab.Get(7)
		if !ok || v != 1000+round {
			t.Fatalf("Get(7) = %v,%v want %d,true", v, ok, 1000+round)
		}
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	// Snapshot must contain key 7 exactly once, with the latest value.
	snap := tab.Snapshot()
	count, last := 0, 0
	for k, v, ok := snap.Next(); ok; k, v, ok = snap.Next() {
		if k == 7 {
			count++
			last = v
		}
	}
	if count != 1 {
		t.Fatalf("key 7 appears %d times in snapshot, want exactly 1", count)
	}
	if v, _ := tab.Get(7); last != v {
		t.Fatalf("snapshot value %d != live value %d", last, v)
	}
}

func TestDeleteNoResurrection(t *testing.T) {
	tab := New[int, int](IntHash, WithMigrateChunk(1))
	fill(t, tab, 100)
	if !tab.Migrating() {
		t.Skip("no migration in flight")
	}
	// Delete a key that is still in the old table mid-migration, then let
	// migration finish: the deleted key must not come back.
	victim := 42
	if !tab.Delete(victim) {
		t.Fatalf("victim %d not present", victim)
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	if _, ok := tab.Get(victim); ok {
		t.Fatalf("deleted key %d resurrected after migration", victim)
	}
	// Re-insert after delete: must land in the newest table only, and the
	// snapshot must show exactly one copy.
	tab.Put(victim, 9999)
	snap := tab.Snapshot()
	count := 0
	for k, _, ok := snap.Next(); ok; k, _, ok = snap.Next() {
		if k == victim {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("re-inserted key appears %d times, want 1", count)
	}
	if v, ok := tab.Get(victim); !ok || v != 9999 {
		t.Fatalf("Get(%d) = %v,%v want 9999,true", victim, v, ok)
	}
}

func TestSnapshotStableUnderMutation(t *testing.T) {
	tab := New[int, int](IntHash, WithMigrateChunk(1))
	fill(t, tab, 300)
	snap := tab.Snapshot()
	want := snap.Collect()
	if len(want) != 300 {
		t.Fatalf("snapshot has %d entries, want 300", len(want))
	}
	ver := snap.Version()
	// Mutate heavily: delete half, update the rest, add new keys, force
	// further migrations.
	for i := 0; i < 300; i += 2 {
		tab.Delete(i)
	}
	for i := 1; i < 300; i += 2 {
		tab.Put(i, -i)
	}
	for i := 300; i < 600; i++ {
		tab.Put(i, i)
	}
	// The snapshot must be byte-for-byte the stable view from before.
	got := snap.Collect()
	if len(got) != len(want) {
		t.Fatalf("snapshot changed: %d entries, want %d", len(got), len(want))
	}
	for k, v := range want {
		if gv, ok := got[k]; !ok || gv != v {
			t.Fatalf("snapshot[%d] = %v,%v want %v,true", k, gv, ok, v)
		}
	}
	if snap.Version() != ver {
		t.Fatal("snapshot version changed")
	}
	// And iteration must yield each key exactly once.
	seen := make(map[int]int)
	for k, _, ok := snap.Next(); ok; k, _, ok = snap.Next() {
		seen[k]++
	}
	for k, c := range seen {
		if c != 1 {
			t.Fatalf("key %d yielded %d times", k, c)
		}
	}
	if len(seen) != 300 {
		t.Fatalf("iteration yielded %d distinct keys, want 300", len(seen))
	}
}

func TestGrowAndShrink(t *testing.T) {
	tab := New[int, int](IntHash, WithMigrateChunk(4))
	fill(t, tab, 1000)
	for tab.Migrating() {
		tab.Get(-1)
	}
	grown := tab.Stats().Buckets
	if grown < 1024 {
		t.Fatalf("buckets = %d after 1000 inserts, expected growth", grown)
	}
	for i := 0; i < 990; i++ {
		tab.Delete(i)
	}
	for tab.Migrating() {
		tab.Get(-1)
	}
	shrunk := tab.Stats().Buckets
	if shrunk >= grown {
		t.Fatalf("buckets = %d, expected shrink below %d", shrunk, grown)
	}
	for i := 990; i < 1000; i++ {
		if v, ok := tab.Get(i); !ok || v != i*i {
			t.Fatalf("Get(%d) = %v,%v after shrink", i, v, ok)
		}
	}
}

func TestVersionCountsMutations(t *testing.T) {
	tab := New[string, int](StringHash)
	tab.Put("x", 1)
	tab.Put("x", 2) // update still counts
	tab.Put("y", 3)
	tab.Delete("x")
	tab.Delete("missing") // no-op: must not bump version
	if got, want := tab.Version(), uint64(4); got != want {
		t.Fatalf("Version = %d want %d", got, want)
	}
	if sv := tab.Snapshot().Version(); sv != tab.Version() {
		t.Fatalf("snapshot version %d != table version %d", sv, tab.Version())
	}
}

func TestStringKeysEndToEnd(t *testing.T) {
	tab := New[string, string](StringHash, WithMigrateChunk(2))
	ref := make(map[string]string)
	for i := 0; i < 400; i++ {
		k := fmt.Sprintf("key-%d", i)
		tab.Put(k, fmt.Sprintf("val-%d", i))
		ref[k] = fmt.Sprintf("val-%d", i)
	}
	for tab.Migrating() {
		tab.Get("")
	}
	got := tab.Snapshot().Collect()
	if len(got) != len(ref) {
		t.Fatalf("snapshot has %d entries, want %d", len(got), len(ref))
	}
	for k, v := range ref {
		if got[k] != v {
			t.Fatalf("snapshot[%q] = %q want %q", k, got[k], v)
		}
	}
}
