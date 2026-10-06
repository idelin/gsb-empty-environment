package memtx

import (
	"reflect"
	"testing"
)

func mustGet(t *testing.T, tx *Txn, key, want string, wantOK bool) {
	t.Helper()
	got, ok, err := tx.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if ok != wantOK || got != want {
		t.Fatalf("Get(%q) = (%q, %v), want (%q, %v)", key, got, ok, want, wantOK)
	}
}

func mustScan(t *testing.T, tx *Txn, opts ScanOptions) ([]KV, *Cursor) {
	t.Helper()
	kvs, cur, err := tx.Scan(opts)
	if err != nil {
		t.Fatalf("Scan(%+v): %v", opts, err)
	}
	return kvs, cur
}

func keys(kvs []KV) []string {
	out := make([]string, len(kvs))
	for i, kv := range kvs {
		out[i] = kv.Key
	}
	return out
}

func seed(t *testing.T, s *Store, kvs ...string) {
	t.Helper()
	tx := s.Begin()
	for i := 0; i < len(kvs); i += 2 {
		if err := tx.Put(kvs[i], kvs[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1", "b", "2")

	tx1 := s.Begin()
	tx2 := s.Begin()
	if err := tx2.Put("a", "changed"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Put("c", "new"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	// tx1 still sees its fixed snapshot.
	mustGet(t, tx1, "a", "1", true)
	mustGet(t, tx1, "b", "2", true)
	mustGet(t, tx1, "c", "", false)
	kvs, cur := mustScan(t, tx1, ScanOptions{})
	if cur != nil {
		t.Fatalf("unexpected cursor: %+v", cur)
	}
	if got, want := keys(kvs), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

func TestReadYourWrites(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1", "b", "2")

	tx := s.Begin()
	if err := tx.Put("a", "mine"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put("c", "new"); err != nil {
		t.Fatal(err)
	}
	mustGet(t, tx, "a", "mine", true)
	mustGet(t, tx, "b", "", false)
	mustGet(t, tx, "c", "new", true)

	// Overwrite then delete then re-put within the same txn.
	if err := tx.Put("c", "v2"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete("c"); err != nil {
		t.Fatal(err)
	}
	mustGet(t, tx, "c", "", false)
	if err := tx.Put("c", "v3"); err != nil {
		t.Fatal(err)
	}
	mustGet(t, tx, "c", "v3", true)
}

func TestScanMergesWriteSet(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1", "c", "3", "e", "5")

	tx := s.Begin()
	if err := tx.Delete("c"); err != nil { // hide committed key
		t.Fatal(err)
	}
	if err := tx.Put("b", "2"); err != nil { // insert between committed keys
		t.Fatal(err)
	}
	if err := tx.Put("d", "4"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put("a", "1x"); err != nil { // override committed key
		t.Fatal(err)
	}

	kvs, cur := mustScan(t, tx, ScanOptions{})
	if cur != nil {
		t.Fatalf("unexpected cursor: %+v", cur)
	}
	want := []KV{{"a", "1x"}, {"b", "2"}, {"d", "4"}, {"e", "5"}}
	if !reflect.DeepEqual(kvs, want) {
		t.Fatalf("scan = %v, want %v", kvs, want)
	}

	// Bounded range excludes write-set entries outside [Start, End).
	kvs, _ = mustScan(t, tx, ScanOptions{Start: "b", End: "e"})
	if got, want := keys(kvs), []string{"b", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ranged scan = %v, want %v", got, want)
	}
}

func TestScanPagination(t *testing.T) {
	s := NewStore()
	seed(t, s, "k1", "v1", "k3", "v3", "k5", "v5", "k7", "v7")

	tx := s.Begin()
	if err := tx.Put("k2", "v2"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put("k4", "v4"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete("k5"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Put("k6", "v6"); err != nil {
		t.Fatal(err)
	}

	var got []string
	var cur *Cursor
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("pagination did not terminate")
		}
		kvs, next := mustScan(t, tx, ScanOptions{Limit: 2, Cursor: cur})
		got = append(got, keys(kvs)...)
		cur = next
		if cur == nil {
			break
		}
	}
	want := []string{"k1", "k2", "k3", "k4", "k6", "k7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged scan = %v, want %v", got, want)
	}
}

func TestCursorBoundToWriteSetRevision(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1", "b", "2", "c", "3", "d", "4")

	tx := s.Begin()
	_, cur := mustScan(t, tx, ScanOptions{Limit: 2})
	if cur == nil {
		t.Fatal("expected cursor after first page")
	}

	// A Put invalidates the cursor.
	if err := tx.Put("z", "9"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tx.Scan(ScanOptions{Limit: 2, Cursor: cur}); err != ErrStaleCursor {
		t.Fatalf("scan with stale cursor: err = %v, want ErrStaleCursor", err)
	}

	// Fresh cursor at the new revision works...
	_, cur2 := mustScan(t, tx, ScanOptions{Limit: 2})
	// ...until a Delete bumps the revision again.
	if err := tx.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tx.Scan(ScanOptions{Limit: 2, Cursor: cur2}); err != ErrStaleCursor {
		t.Fatalf("scan after delete: err = %v, want ErrStaleCursor", err)
	}

	// Cursor survives encode/decode and still validates against revision.
	_, cur3 := mustScan(t, tx, ScanOptions{Limit: 1})
	tok := cur3.Encode()
	dec, err := DecodeCursor(tok)
	if err != nil {
		t.Fatal(err)
	}
	kvs, _, err := tx.Scan(ScanOptions{Limit: 1, Cursor: dec})
	if err != nil {
		t.Fatalf("scan with decoded cursor: %v", err)
	}
	if len(kvs) != 1 {
		t.Fatalf("decoded-cursor page = %v, want 1 item", keys(kvs))
	}
}

func TestCommitConflict(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1")

	tx1 := s.Begin()
	tx2 := s.Begin()
	if err := tx1.Put("a", "from-1"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Put("a", "from-2"); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != ErrConflict {
		t.Fatalf("tx2 commit: err = %v, want ErrConflict", err)
	}

	// Non-overlapping write sets do not conflict.
	tx3 := s.Begin()
	tx4 := s.Begin()
	if err := tx3.Put("a", "again"); err != nil {
		t.Fatal(err)
	}
	if err := tx4.Put("b", "other"); err != nil {
		t.Fatal(err)
	}
	if err := tx3.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx4.Commit(); err != nil {
		t.Fatalf("disjoint commit: %v", err)
	}

	tx := s.Begin()
	mustGet(t, tx, "a", "again", true)
	mustGet(t, tx, "b", "other", true)
}

func TestCommitDeleteTombstone(t *testing.T) {
	s := NewStore()
	seed(t, s, "a", "1")

	tx := s.Begin()
	if err := tx.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete("never-existed"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2 := s.Begin()
	mustGet(t, tx2, "a", "", false)
	kvs, cur := mustScan(t, tx2, ScanOptions{})
	if cur != nil || len(kvs) != 0 {
		t.Fatalf("scan after delete = %v, want empty", kvs)
	}
}

func TestClosedTxn(t *testing.T) {
	s := NewStore()
	tx := s.Begin()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tx.Get("a"); err != ErrClosed {
		t.Fatalf("Get on closed txn: %v", err)
	}
	if err := tx.Put("a", "1"); err != ErrClosed {
		t.Fatalf("Put on closed txn: %v", err)
	}
	if _, _, err := tx.Scan(ScanOptions{}); err != ErrClosed {
		t.Fatalf("Scan on closed txn: %v", err)
	}
	if err := tx.Commit(); err != ErrClosed {
		t.Fatalf("double commit: %v", err)
	}

	tx2 := s.Begin()
	tx2.Rollback()
	if err := tx2.Delete("a"); err != ErrClosed {
		t.Fatalf("Delete on rolled-back txn: %v", err)
	}
}
