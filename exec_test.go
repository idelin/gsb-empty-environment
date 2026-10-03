package decor

import (
	"fmt"
	"sort"
	"testing"
)

func buildTestDB(nDepts, nEmps int) *DB {
	db := NewDB()
	var depts, emps []Row
	for i := 0; i < nDepts; i++ {
		depts = append(depts, Row{"id": int64(i), "name": fmt.Sprintf("dept-%d", i)})
	}
	for i := 0; i < nEmps; i++ {
		// Only the first half of the departments has employees.
		emps = append(emps, Row{"id": int64(i), "dept_id": int64(i % (nDepts / 2)), "name": fmt.Sprintf("emp-%d", i)})
	}
	db.AddTable("depts", depts)
	db.AddTable("emps", emps)
	return db
}

func sortedIDs(rows []Row, key string) []int64 {
	var ids []int64
	for _, r := range rows {
		v, ok := r[key]
		if !ok {
			continue
		}
		if id, ok := v.(int64); ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The decorrelated hash semi-join must return exactly the rows the naive
// per-row correlated evaluation returns.
func TestHashSemiJoinMatchesNaive(t *testing.T) {
	db := buildTestDB(100, 2000)
	stmt, err := Parse("SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rw, err := Decorrelate(stmt)
	if err != nil {
		t.Fatalf("decorrelate: %v", err)
	}

	naiveRows, evals, err := EvalNaive(stmt, db)
	if err != nil {
		t.Fatalf("naive: %v", err)
	}
	hashRows, err := Execute(rw.Plan, db)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, want := sortedIDs(hashRows, "d.id"), sortedIDs(naiveRows, "d.id")
	if !equalIDs(got, want) {
		t.Fatalf("result mismatch:\nnaive: %v\nhash:  %v", want, got)
	}
	if len(got) != 50 {
		t.Fatalf("want 50 matching departments, got %d", len(got))
	}
	if evals == 0 {
		t.Fatal("naive evaluation counter not incremented")
	}
	t.Logf("naive inner evaluations: %d", evals)
}

func TestInSubqueryExecMatchesNaive(t *testing.T) {
	db := buildTestDB(100, 2000)
	stmt, err := Parse("SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.id < 10)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rw, err := Decorrelate(stmt)
	if err != nil {
		t.Fatalf("decorrelate: %v", err)
	}
	naiveRows, _, err := EvalNaive(stmt, db)
	if err != nil {
		t.Fatalf("naive: %v", err)
	}
	hashRows, err := Execute(rw.Plan, db)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, want := sortedIDs(hashRows, "e.id"), sortedIDs(naiveRows, "e.id")
	if !equalIDs(got, want) {
		t.Fatalf("result mismatch:\nnaive: %v\nhash:  %v", want, got)
	}
	// Semi join must not duplicate employees even if the subquery had dupes.
	// 2000 emps spread over 50 departments -> 40 per dept; 10 depts match.
	if len(got) != 400 {
		t.Fatalf("want 400 employees, got %d", len(got))
	}
}

func TestUncorrelatedExistsExec(t *testing.T) {
	stmt, err := Parse("SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.vip = 1)")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rw, err := Decorrelate(stmt)
	if err != nil {
		t.Fatalf("decorrelate: %v", err)
	}

	// Empty emps -> EXISTS false -> no departments.
	dbEmpty := NewDB()
	dbEmpty.AddTable("depts", []Row{{"id": int64(1)}, {"id": int64(2)}})
	dbEmpty.AddTable("emps", nil)
	rows, err := Execute(rw.Plan, dbEmpty)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want 0 rows with empty emps, got %d", len(rows))
	}

	// One vip employee -> EXISTS true -> all departments.
	dbFull := NewDB()
	dbFull.AddTable("depts", []Row{{"id": int64(1)}, {"id": int64(2)}})
	dbFull.AddTable("emps", []Row{{"id": int64(7), "vip": int64(1)}})
	rows, err = Execute(rw.Plan, dbFull)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows with a vip employee, got %d", len(rows))
	}
}
