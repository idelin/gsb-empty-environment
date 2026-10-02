package cowtree

import (
	"fmt"
	"sync"
	"testing"
)

func valOf(key int64) int64 { return key * 10 }

func fill(db *DB, from, to int64) {
	for k := from; k < to; k++ {
		db.Put(k, valOf(k))
	}
}

func TestPutAndGet(t *testing.T) {
	db := Open()
	fill(db, 0, 500)
	for k := int64(0); k < 500; k++ {
		v, ok := db.Get(k)
		if !ok || v != valOf(k) {
			t.Fatalf("Get(%d) = %d, %v; want %d, true", k, v, ok, valOf(k))
		}
	}
	if _, ok := db.Get(500); ok {
		t.Fatal("Get(500) should miss")
	}
}

func TestUpdateExistingKey(t *testing.T) {
	db := Open()
	fill(db, 0, 100)
	db.Put(42, 9999)
	if v, _ := db.Get(42); v != 9999 {
		t.Fatalf("Get(42) = %d; want 9999", v)
	}
	// Neighbours untouched.
	if v, _ := db.Get(41); v != valOf(41) {
		t.Fatalf("Get(41) = %d; want %d", v, valOf(41))
	}
}

func TestIteratorEmpty(t *testing.T) {
	db := Open()
	it := db.View().Iterator()
	if it.Next() {
		t.Fatal("empty snapshot must not iterate")
	}
}

// TestPageLevelCoW verifies that a write allocates new pages and leaves the
// old root page byte-for-byte untouched.
func TestPageLevelCoW(t *testing.T) {
	db := Open()
	fill(db, 0, 200)

	oldRoot := db.root.Load()
	oldRootID := oldRoot.id
	oldKeys := append([]int64(nil), oldRoot.keys...)
	pagesBefore := db.PagesAllocated()

	db.Put(7, 777)

	newRoot := db.root.Load()
	if newRoot == oldRoot {
		t.Fatal("root page was reused: write modified the tree in place")
	}
	if newRoot.id == oldRootID {
		t.Fatal("root page ID was reused")
	}
	if db.PagesAllocated() <= pagesBefore {
		t.Fatal("write allocated no new pages")
	}
	if oldRoot.id != oldRootID || len(oldRoot.keys) != len(oldKeys) {
		t.Fatal("old root page was mutated")
	}
	for i, k := range oldKeys {
		if oldRoot.keys[i] != k {
			t.Fatalf("old root page mutated at key slot %d", i)
		}
	}
}

// TestSnapshotIsolation is the acceptance test: a read-only transaction
// holding the old root scans the whole table while a concurrent writer is
// busy splitting nodes. The scan must observe exactly the snapshot taken
// before the writer started.
func TestSnapshotIsolation(t *testing.T) {
	db := Open()
	const base = int64(1000)
	fill(db, 0, base)

	snap := db.View()
	snapRootID := snap.RootPageID()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Each batch of inserts forces many leaf and internal splits.
		fill(db, base, base*6)
	}()

	// Scan the snapshot repeatedly while the writer splits nodes.
	scans := 0
	for {
		select {
		case <-done:
			goto finished
		default:
		}
		it := snap.Iterator()
		prev := int64(-1)
		count := int64(0)
		for it.Next() {
			k, v := it.Key(), it.Value()
			if k != count {
				t.Fatalf("snapshot scan: got key %d at position %d; snapshot is inconsistent", k, count)
			}
			if v != valOf(k) {
				t.Fatalf("snapshot scan: key %d has value %d; want %d", k, v, valOf(k))
			}
			if k <= prev {
				t.Fatalf("snapshot scan: keys out of order at %d", k)
			}
			prev = k
			count++
		}
		if count != base {
			t.Fatalf("snapshot scan saw %d keys; want exactly %d (writer's keys leaked in)", count, base)
		}
		scans++
	}
finished:
	if scans == 0 {
		t.Fatal("no scan overlapped with the writer")
	}
	t.Logf("completed %d consistent full-table scans while writer split nodes", scans)

	// The snapshot still sees the old root and old data after the writer
	// committed everything.
	if snap.RootPageID() != snapRootID {
		t.Fatal("snapshot root changed")
	}
	if _, ok := snap.Get(base); ok {
		t.Fatal("snapshot sees a key written after it began")
	}
	// The live DB sees everything.
	for k := int64(0); k < base*6; k++ {
		if _, ok := db.Get(k); !ok {
			t.Fatalf("live DB missing key %d", k)
		}
	}
}

// TestConcurrentReadersAndWriter hammers the tree with one writer and many
// readers under `go test -race`. Every scan must be strictly ordered and
// every observed key must carry its matching value (no torn reads).
func TestConcurrentReadersAndWriter(t *testing.T) {
	db := Open()
	fill(db, 0, 200)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := db.View()
				it := snap.Iterator()
				prev := int64(-1)
				for it.Next() {
					k, v := it.Key(), it.Value()
					if k <= prev {
						t.Errorf("reader %d: keys out of order at %d", id, k)
						return
					}
					if v != valOf(k) {
						t.Errorf("reader %d: torn read, key %d value %d", id, k, v)
						return
					}
					prev = k
				}
			}
		}(r)
	}
	for k := int64(200); k < 2000; k++ {
		db.Put(k, valOf(k))
	}
	close(stop)
	wg.Wait()
}

// TestManySnapshots verifies that snapshots taken at different versions all
// remain valid simultaneously (multi-version reads).
func TestManySnapshots(t *testing.T) {
	db := Open()
	var snaps []*Snapshot
	for version := int64(0); version < 20; version++ {
		fill(db, version*50, version*50+50)
		snaps = append(snaps, db.View())
	}
	for version, snap := range snaps {
		want := int64(version+1) * 50
		count := int64(0)
		it := snap.Iterator()
		for it.Next() {
			if it.Key() != count || it.Value() != valOf(count) {
				t.Fatalf("snapshot %d: inconsistent at key %d", version, it.Key())
			}
			count++
		}
		if count != want {
			t.Fatalf("snapshot %d saw %d keys; want %d", version, count, want)
		}
	}
}

func ExampleDB_View() {
	db := Open()
	for k := int64(0); k < 100; k++ {
		db.Put(k, k*10)
	}
	snap := db.View()
	// Writer commits more data and splits pages after the snapshot began.
	for k := int64(100); k < 300; k++ {
		db.Put(k, k*10)
	}
	count := 0
	it := snap.Iterator()
	for it.Next() {
		count++
	}
	fmt.Println("snapshot sees", count, "keys")
	fmt.Println("live sees", db.PagesAllocated() > 0 && func() bool {
		_, ok := db.Get(299)
		return ok
	}(), "key 299")
	// Output:
	// snapshot sees 100 keys
	// live sees true key 299
}
