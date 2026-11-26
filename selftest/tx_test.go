package selftest_test

import (
	"errors"
	"testing"

	temporal "prescreen.local/ps-db7888bb15"
)

func TestAccTransactionCommitRollback(t *testing.T) {
	s := temporal.NewSchema()
	parent := s.CreateTable("p", "k")
	child := s.CreateTable("c", "k")
	s.AddForeignKey(temporal.ForeignKeyCoverage{
		ChildTable: "c", ChildColumns: []string{"k"},
		ParentTable: "p", ParentColumns: []string{"k"},
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(parent.Insert(temporal.Row{Key: temporal.Key{"k": 1}, Range: ir(0, 100)}))
	must(child.Insert(temporal.Row{Key: temporal.Key{"k": 1}, Range: ir(0, 100)}))

	// Rollback discards a valid split.
	tx := s.Begin()
	must(tx.UpdatePortion("c", temporal.Key{"k": 1}, ir(40, 60), "x"))
	if got := len(tx.Table("c").Rows); got != 3 {
		t.Fatalf("tx working copy should split to 3, got %d", got)
	}
	must(tx.Rollback())
	if got := len(child.Rows); got != 1 {
		t.Fatalf("root must be untouched after rollback, got %d rows", got)
	}
	if err := tx.Rollback(); !errors.Is(err, temporal.ErrTxDone) {
		t.Fatalf("second rollback should return ErrTxDone, got %v", err)
	}

	// Commit publishes a covered split.
	tx2 := s.Begin()
	must(tx2.UpdatePortion("c", temporal.Key{"k": 1}, ir(40, 60), "x"))
	must(tx2.Commit())
	if got := len(child.Rows); got != 3 {
		t.Fatalf("committed split should publish 3 rows, got %d", got)
	}

	// Statement-level validation already rolls a bad portion back, so a
	// directly introduced invalid state is what makes Commit fail.
	tx3 := s.Begin()
	bad := temporal.Row{Key: temporal.Key{"k": 2}, Range: ir(0, 100)}
	tx3.Table("c").Rows = append(tx3.Table("c").Rows, bad)
	if err := tx3.Commit(); err == nil {
		t.Fatal("commit of coverage-breaking tx must fail")
	}
}
