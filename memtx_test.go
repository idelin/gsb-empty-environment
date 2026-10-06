package memtx

import (
	"reflect"
	"testing"
)

func mustPut(t *testing.T, tx *Txn, key, value string) {
	t.Helper()
	if err := tx.Put(key, []byte(value)); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func mustCommit(t *testing.T, tx *Txn) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func seed(t *testing.T, db *DB, kvs map[string]string) {
	t.Helper()
	tx := db.Begin()
	for k, v := range kvs {
		mustPut(t, tx, k, v)
	}
	mustCommit(t, tx)
}

func scanAll(t *testing.T, tx *Txn, start, end string) []KV {
	t.Helper()
	page, err := tx.Scan(start, end, 0, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if page.Next != nil {
		t.Fatalf("unlimited scan returned a continuation cursor")
	}
	return page.Items
}

func keys(items []KV) []string {
	out := make([]string, len(items))
	for i, kv := range items {
		out[i] = kv.Key
	}
	return out
}

func TestSnapshotIsFixedAtBegin(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1"})

	tx := db.Begin()
	// Another transaction commits after tx's snapshot was taken.
	other := db.Begin()
	mustPut(t, other, "a", "2")
	mustPut(t, other, "b", "9")
	mustCommit(t, other)

	got, err := tx.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "1" {
		t.Fatalf("snapshot read = %q, want %q", got, "1")
	}
	if _, err := tx.Get("b"); err != ErrKeyNotFound {
		t.Fatalf("Get(b) err = %v, want ErrKeyNotFound", err)
	}
	if got := keys(scanAll(t, tx, "", "")); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("snapshot scan keys = %v, want [a]", got)
	}
	tx.Rollback()
}

func TestReadYourOwnWrites(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1"})

	tx := db.Begin()
	defer tx.Rollback()
	mustPut(t, tx, "a", "updated")
	mustPut(t, tx, "new", "fresh")

	got, err := tx.Get("a")
	if err != nil || string(got) != "updated" {
		t.Fatalf("Get(a) = %q, %v; want updated", got, err)
	}
	got, err = tx.Get("new")
	if err != nil || string(got) != "fresh" {
		t.Fatalf("Get(new) = %q, %v; want fresh", got, err)
	}

	// Overwrite within the same transaction: last write wins.
	mustPut(t, tx, "new", "fresher")
	got, _ = tx.Get("new")
	if string(got) != "fresher" {
		t.Fatalf("Get(new) after rewrite = %q, want fresher", got)
	}
}

func TestDeleteMasksSnapshotAndWriteSet(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1", "b": "2"})

	tx := db.Begin()
	defer tx.Rollback()
	if err := tx.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := tx.Get("a"); err != ErrKeyNotFound {
		t.Fatalf("Get(a) after delete err = %v, want ErrKeyNotFound", err)
	}
	if got := keys(scanAll(t, tx, "", "")); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("scan keys after delete = %v, want [b]", got)
	}

	// Delete a key that only exists in the write set.
	mustPut(t, tx, "tmp", "x")
	if err := tx.Delete("tmp"); err != nil {
		t.Fatalf("Delete(tmp): %v", err)
	}
	if _, err := tx.Get("tmp"); err != ErrKeyNotFound {
		t.Fatalf("Get(tmp) err = %v, want ErrKeyNotFound", err)
	}

	// Put after delete resurrects the key.
	mustPut(t, tx, "a", "revived")
	got, err := tx.Get("a")
	if err != nil || string(got) != "revived" {
		t.Fatalf("Get(a) after re-put = %q, %v; want revived", got, err)
	}
}

func TestScanMergesWriteSetInSortedOrder(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{
		"a": "1", "c": "3", "e": "5", "g": "7",
	})

	tx := db.Begin()
	defer tx.Rollback()
	mustPut(t, tx, "b", "ws-b") // insert between snapshot keys
	mustPut(t, tx, "d", "ws-d")
	mustPut(t, tx, "f", "ws-f")
	mustPut(t, tx, "c", "ws-c") // overwrite snapshot key
	if err := tx.Delete("e"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	items := scanAll(t, tx, "", "")
	wantKeys := []string{"a", "b", "c", "d", "f", "g"}
	if got := keys(items); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("merged keys = %v, want %v", got, wantKeys)
	}
	for _, kv := range items {
		if kv.Key == "c" && string(kv.Value) != "ws-c" {
			t.Fatalf("overwritten key c = %q, want ws-c", kv.Value)
		}
	}

	// Range bounds apply to both snapshot and write-set entries.
	items = scanAll(t, tx, "b", "f")
	if got := keys(items); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Fatalf("ranged keys = %v, want [b c d]", got)
	}
}

func TestPaginationAcrossMergedView(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{
		"k01": "s", "k03": "s", "k05": "s", "k07": "s", "k09": "s",
	})

	tx := db.Begin()
	defer tx.Rollback()
	mustPut(t, tx, "k02", "w")
	mustPut(t, tx, "k04", "w")
	mustPut(t, tx, "k06", "w")
	mustPut(t, tx, "k08", "w")
	mustPut(t, tx, "k10", "w")
	if err := tx.Delete("k05"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	want := []string{"k01", "k02", "k03", "k04", "k06", "k07", "k08", "k09", "k10"}
	var got []string
	var cursor *Cursor
	for {
		page, err := tx.Scan("", "", 3, cursor)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got = append(got, keys(page.Items)...)
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paginated keys = %v, want %v", got, want)
	}
}

func TestStaleCursorRejectedAfterWriteSetChange(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"})

	tx := db.Begin()
	defer tx.Rollback()
	page, err := tx.Scan("", "", 2, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	cursor := page.Next
	if cursor == nil {
		t.Fatalf("expected continuation cursor")
	}

	// Any write-set mutation invalidates the cursor.
	mustPut(t, tx, "e", "5")
	if _, err := tx.Scan("", "", 2, cursor); err != ErrStaleCursor {
		t.Fatalf("Scan with stale cursor err = %v, want ErrStaleCursor", err)
	}

	// Fresh cursor works, then a Delete invalidates it too.
	page, err = tx.Scan("", "", 2, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	cursor = page.Next
	if err := tx.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := tx.Scan("", "", 2, cursor); err != ErrStaleCursor {
		t.Fatalf("Scan after Delete err = %v, want ErrStaleCursor", err)
	}
}

func TestCursorBoundToIssuingTransaction(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1", "b": "2", "c": "3"})

	tx1 := db.Begin()
	defer tx1.Rollback()
	page, err := tx1.Scan("", "", 1, nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	cursor := page.Next

	// A cursor carries its snapshot revision, so another transaction at a
	// different snapshot rejects it.
	seed(t, db, map[string]string{"z": "26"})
	tx2 := db.Begin()
	defer tx2.Rollback()
	if _, err := tx2.Scan("", "", 1, cursor); err != ErrStaleCursor {
		t.Fatalf("cross-transaction cursor err = %v, want ErrStaleCursor", err)
	}
}

func TestCommitConflict(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1"})

	tx1 := db.Begin()
	tx2 := db.Begin()
	mustPut(t, tx1, "a", "from-tx1")
	mustPut(t, tx2, "a", "from-tx2")

	mustCommit(t, tx1)
	if err := tx2.Commit(); err != ErrConflict {
		t.Fatalf("tx2.Commit err = %v, want ErrConflict", err)
	}
	// The losing transaction is closed.
	if err := tx2.Put("b", []byte("x")); err != ErrTxnClosed {
		t.Fatalf("Put after failed commit err = %v, want ErrTxnClosed", err)
	}

	// Non-overlapping write sets do not conflict.
	tx3 := db.Begin()
	tx4 := db.Begin()
	mustPut(t, tx3, "k1", "v1")
	mustPut(t, tx4, "k2", "v2")
	mustCommit(t, tx3)
	mustCommit(t, tx4)
}

func TestCommitPublishesAtNewRevision(t *testing.T) {
	db := Open()
	seed(t, db, map[string]string{"a": "1"})
	before := db.Revision()

	tx := db.Begin()
	mustPut(t, tx, "a", "2")
	mustPut(t, tx, "b", "3")
	mustCommit(t, tx)

	if db.Revision() != before+1 {
		t.Fatalf("revision = %d, want %d (one revision per commit)", db.Revision(), before+1)
	}
	reader := db.Begin()
	defer reader.Rollback()
	got, err := reader.Get("a")
	if err != nil || string(got) != "2" {
		t.Fatalf("Get(a) = %q, %v; want 2", got, err)
	}
	if got := keys(scanAll(t, reader, "", "")); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("scan keys = %v, want [a b]", got)
	}
}

func TestClosedTransactionRejectsOperations(t *testing.T) {
	db := Open()
	tx := db.Begin()
	mustCommit(t, tx)
	if _, err := tx.Get("a"); err != ErrTxnClosed {
		t.Fatalf("Get on committed txn err = %v, want ErrTxnClosed", err)
	}
	if _, err := tx.Scan("", "", 0, nil); err != ErrTxnClosed {
		t.Fatalf("Scan on committed txn err = %v, want ErrTxnClosed", err)
	}
	if err := tx.Commit(); err != ErrTxnClosed {
		t.Fatalf("double Commit err = %v, want ErrTxnClosed", err)
	}
}
