// Command demo shows subquery unnesting (decorrelation) end to end:
// parse a correlated query, rewrite it into a semi join, print the
// logical plan, then prove equivalence and measure the speedup against
// naive per-row subquery execution on generated data.
package main

import (
	"fmt"
	"os"
	"time"

	"decor"
)

func main() {
	query := "SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)"

	stmt, err := decor.Parse(query)
	if err != nil {
		fail(err)
	}
	rw, err := decor.Decorrelate(stmt)
	if err != nil {
		fail(err)
	}

	fmt.Println("=== Subquery Unnesting / Decorrelation ===")
	fmt.Println()
	fmt.Println("Input query:")
	fmt.Println("  " + query)
	fmt.Println()
	fmt.Println("Rules applied:")
	for _, r := range rw.Rules {
		fmt.Println("  - " + r)
	}
	fmt.Println()
	fmt.Println("Rewritten query:")
	fmt.Println("  " + rw.SQL)
	fmt.Println()
	fmt.Println("Logical plan:")
	fmt.Println(decor.RenderPlan(rw.Plan))
	fmt.Println()

	// Second example: IN subquery with a local predicate inside.
	inQuery := "SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = 'EU')"
	inStmt, err := decor.Parse(inQuery)
	if err != nil {
		fail(err)
	}
	inRw, err := decor.Decorrelate(inStmt)
	if err != nil {
		fail(err)
	}
	fmt.Println("IN example:")
	fmt.Println("  " + inQuery)
	fmt.Println("  ->")
	fmt.Println("  " + inRw.SQL)
	fmt.Println()

	// Correctness + performance on generated data.
	const nDepts = 200
	const nEmps = 4000
	db := decor.NewDB()
	var depts, emps []decor.Row
	for i := 0; i < nDepts; i++ {
		depts = append(depts, decor.Row{"id": int64(i), "name": fmt.Sprintf("dept-%d", i)})
	}
	for i := 0; i < nEmps; i++ {
		// Only departments 0..nDepts/2-1 have employees.
		emps = append(emps, decor.Row{
			"id":      int64(i),
			"dept_id": int64(i % (nDepts / 2)),
			"name":    fmt.Sprintf("emp-%d", i),
		})
	}
	db.AddTable("depts", depts)
	db.AddTable("emps", emps)

	fmt.Printf("Data: %d departments, %d employees (half the departments are empty)\n\n", nDepts, nEmps)

	start := time.Now()
	naiveRows, evals, err := decor.EvalNaive(stmt, db)
	if err != nil {
		fail(err)
	}
	naiveDur := time.Since(start)

	start = time.Now()
	hashRows, err := decor.Execute(rw.Plan, db)
	if err != nil {
		fail(err)
	}
	hashDur := time.Since(start)

	naiveSet := map[int64]bool{}
	for _, r := range naiveRows {
		naiveSet[r["d.id"].(int64)] = true
	}
	for _, r := range hashRows {
		id := r["d.id"].(int64)
		if !naiveSet[id] {
			fail(fmt.Errorf("hash semi-join produced row not in naive result: %d", id))
		}
		delete(naiveSet, id)
	}
	if len(naiveSet) != 0 || len(hashRows) != len(naiveRows) {
		fail(fmt.Errorf("result mismatch: naive=%d rows, hash=%d rows", len(naiveRows), len(hashRows)))
	}

	fmt.Println("Execution comparison (results verified identical):")
	fmt.Printf("  naive correlated loop : %8v   (%d rows, %d inner predicate evaluations)\n", naiveDur, len(naiveRows), evals)
	fmt.Printf("  decorrelated hash join: %8v   (%d rows, %d build + %d probe operations)\n", hashDur, len(hashRows), nEmps, nDepts)
	if hashDur > 0 {
		fmt.Printf("  speedup               : %.0fx\n", float64(naiveDur)/float64(hashDur))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
