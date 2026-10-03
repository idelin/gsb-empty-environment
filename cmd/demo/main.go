// Command demo parses SQL queries containing correlated subqueries and
// prints the decorrelated logical plan.
package main

import (
	"fmt"

	"decorrelator"
)

var queries = []string{
	"SELECT * FROM depts d WHERE EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id)",
	"SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = e.region)",
	"SELECT * FROM emps e WHERE e.dept_id IN (SELECT d.id FROM depts d WHERE d.region = 'EU')",
	"SELECT * FROM depts d WHERE d.budget > 1000 AND EXISTS (SELECT 1 FROM emps e WHERE e.dept_id = d.id AND e.active = 1)",
}

func main() {
	for i, sql := range queries {
		fmt.Printf("=== Query %d ===\n", i+1)
		fmt.Printf("input:     %s\n", sql)
		stmt, err := decorrelator.Parse(sql)
		if err != nil {
			fmt.Printf("parse error: %v\n\n", err)
			continue
		}
		plan, err := decorrelator.Unnest(stmt)
		if err != nil {
			fmt.Printf("unnest error: %v\n\n", err)
			continue
		}
		fmt.Printf("rewritten: %s\n", decorrelator.RenderSQL(plan))
		fmt.Printf("plan:\n%s\n", decorrelator.Explain(plan))
	}
}
