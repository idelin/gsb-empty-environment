package kvdb

import (
	"errors"
	"reflect"
	"testing"
)

func seed(t *testing.T, db *DB, kvs map[int]string) {
	t.Helper()
	tx := db.Begin()
	for k, v := range kvs {
		tx.Put(k, v)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

func TestInsertInsideRangeAborts(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{11: "a", 15: "b"})

	t1 := db.Begin()
	if got := t1.Scan(10, 20); len(got) != 2 {
		t.Fatalf("scan got %v", got)
	}
	t2 := db.Begin()
	t2.Put(12, "phantom")
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t1 commit = %v, want ErrTxnAborted", err)
	}
	if _, ok := db.All()[12]; !ok {
		t.Fatal("t2 write must survive")
	}
}

func TestInsertOutsideRangeSucceeds(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{11: "a"})

	t1 := db.Begin()
	t1.Scan(10, 20)
	t2 := db.Begin()
	t2.Put(25, "outside")
	t2.Put(9, "outside")
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); err != nil {
		t.Fatalf("t1 commit = %v, want nil (writes outside range)", err)
	}
}

// Boundary-adjacent half-open intervals: [0,10) and [10,20) share the edge
// key 10, which belongs only to the second interval.
func TestBoundaryAdjacentIntervals(t *testing.T) {
	cases := []struct {
		name      string
		writeKey  int
		wantT1Err bool // t1 scanned [0,10)
		wantT2Err bool // t2 scanned [10,20)
	}{
		{"edge key 10 belongs to second interval only", 10, false, true},
		{"key 9 inside first interval", 9, true, false},
		{"key 19 inside second interval", 19, false, true},
		{"key 20 outside both", 20, false, false},
		{"key 0 at start of first interval", 0, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := Open()
			t1 := db.Begin()
			t1.Scan(0, 10)
			t2 := db.Begin()
			t2.Scan(10, 20)

			w := db.Begin()
			w.Put(tc.writeKey, "x")
			if err := w.Commit(); err != nil {
				t.Fatalf("writer commit: %v", err)
			}

			err1 := t1.Commit()
			if got := errors.Is(err1, ErrTxnAborted); got != tc.wantT1Err {
				t.Fatalf("t1 aborted = %v, want %v", got, tc.wantT1Err)
			}
			err2 := t2.Commit()
			if got := errors.Is(err2, ErrTxnAborted); got != tc.wantT2Err {
				t.Fatalf("t2 aborted = %v, want %v", got, tc.wantT2Err)
			}
		})
	}
}

// An empty scan result must still be protected: a later insert into the
// scanned range is a phantom and aborts the reader.
func TestEmptyRangePhantomProtection(t *testing.T) {
	db := Open()

	t1 := db.Begin()
	if got := t1.Scan(100, 200); len(got) != 0 {
		t.Fatalf("scan got %v, want empty", got)
	}
	t2 := db.Begin()
	t2.Put(150, "phantom")
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t1 commit = %v, want ErrTxnAborted", err)
	}
}

func TestDeleteInsideRangeAborts(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{5: "victim", 50: "bystander"})

	t1 := db.Begin()
	t1.Scan(0, 10)
	t2 := db.Begin()
	t2.Delete(5)
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t1 commit = %v, want ErrTxnAborted", err)
	}
}

func TestPointReadProtection(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{7: "old"})

	t1 := db.Begin()
	if v, ok := t1.Get(7); !ok || v != "old" {
		t.Fatalf("get = %q,%v", v, ok)
	}
	t2 := db.Begin()
	t2.Put(7, "new")
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t1 commit = %v, want ErrTxnAborted", err)
	}
}

func TestWriteWriteConflict(t *testing.T) {
	db := Open()

	t1 := db.Begin()
	t1.Put(1, "t1")
	t2 := db.Begin()
	t2.Put(1, "t2")
	if err := t1.Commit(); err != nil {
		t.Fatalf("t1 commit: %v", err)
	}
	if err := t2.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t2 commit = %v, want ErrTxnAborted", err)
	}
	if got := db.All()[1]; got != "t1" {
		t.Fatalf("db[1] = %q, want t1 (first committer wins)", got)
	}
}

func TestReadYourWrites(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{1: "a", 2: "b", 3: "c"})

	tx := db.Begin()
	tx.Put(2, "b2")
	tx.Delete(3)
	tx.Put(4, "d")
	if v, ok := tx.Get(2); !ok || v != "b2" {
		t.Fatalf("get own write = %q,%v", v, ok)
	}
	if _, ok := tx.Get(3); ok {
		t.Fatal("get own delete must miss")
	}
	want := []Pair{{1, "a"}, {2, "b2"}, {4, "d"}}
	if got := tx.Scan(0, 10); !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestRollbackReleasesSnapshot(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{1: "a"})

	t1 := db.Begin()
	t1.Scan(0, 10)
	if err := t1.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	t2 := db.Begin()
	t2.Put(5, "x")
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrTxnClosed) {
		t.Fatalf("commit after rollback = %v, want ErrTxnClosed", err)
	}
}

func TestUpdateInsideRangeAborts(t *testing.T) {
	db := Open()
	seed(t, db, map[int]string{30: "old"})

	t1 := db.Begin()
	t1.Scan(20, 40)
	t2 := db.Begin()
	t2.Put(30, "new") // update, not insert: still changes the read result
	if err := t2.Commit(); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	if err := t1.Commit(); !errors.Is(err, ErrTxnAborted) {
		t.Fatalf("t1 commit = %v, want ErrTxnAborted", err)
	}
}
