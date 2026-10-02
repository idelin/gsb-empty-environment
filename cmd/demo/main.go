// Demo: cost-based CTE materialization vs. inlining.
package main

import (
	"fmt"
	"sort"

	"cteopt"
)

const expensiveQuery = `
WITH big_orders AS (
  SELECT o.id, o.cust_id, o.amount
  FROM orders o JOIN customers c ON o.cust_id = c.id
  WHERE o.amount > 100
)
SELECT * FROM big_orders x JOIN big_orders y ON x.cust_id = y.cust_id
`

const cheapQuery = `
WITH few AS (SELECT * FROM customers)
SELECT * FROM few a JOIN few b ON a.id = b.id
`

func tables() map[string][]cteopt.Row {
	return map[string][]cteopt.Row{
		"customers": {
			{"id": 1.0, "name": "alice", "region": "cn"},
			{"id": 2.0, "name": "bob", "region": "us"},
			{"id": 3.0, "name": "carol", "region": "cn"},
		},
		"orders": {
			{"id": 101.0, "cust_id": 1.0, "amount": 250.0},
			{"id": 102.0, "cust_id": 2.0, "amount": 300.0},
			{"id": 103.0, "cust_id": 1.0, "amount": 80.0},
			{"id": 104.0, "cust_id": 2.0, "amount": 450.0},
		},
	}
}

func scanExecs(root *cteopt.Node, eng *cteopt.Engine, table string) int {
	total := 0
	seen := map[*cteopt.Node]bool{}
	var walk func(*cteopt.Node)
	walk = func(n *cteopt.Node) {
		if seen[n] {
			return
		}
		seen[n] = true
		if n.Op == cteopt.Scan && n.Table == table {
			total += eng.ExecCounts[n]
		}
		for _, c := range n.Children {
			walk(c)
		}
		if n.Src != nil {
			walk(n.Src)
		}
	}
	walk(root)
	return total
}

func run(title, query string, stats cteopt.Stats, watchTable string) {
	fmt.Printf("=== %s ===\n", title)
	root, err := cteopt.Parse(query)
	if err != nil {
		panic(err)
	}
	cteopt.Estimate(root, stats)

	engInline := cteopt.NewEngine(tables())
	inlineRows := engInline.Execute(root)

	opt := cteopt.Optimize(root, stats)
	engOpt := cteopt.NewEngine(tables())
	optRows := engOpt.Execute(opt)

	fmt.Println("Optimized plan:")
	fmt.Print(opt)
	fmt.Printf("  inline executions of %s scan:     %d\n", watchTable, scanExecs(root, engInline, watchTable))
	fmt.Printf("  materialized executions of %s scan: %d\n", watchTable, scanExecs(opt, engOpt, watchTable))
	fmt.Printf("  result rows: %d (inline) / %d (materialized), identical=%v\n\n",
		len(inlineRows), len(optRows), sameRows(inlineRows, optRows))
}

func sameRows(a, b []cteopt.Row) bool {
	key := func(rows []cteopt.Row) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, cteopt.RowKey(r))
		}
		sort.Strings(out)
		return out
	}
	ka, kb := key(a), key(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

func main() {
	big := cteopt.Stats{"orders": 1_000_000, "customers": 100_000}
	small := cteopt.Stats{"orders": 10, "customers": 5}
	run("Expensive CTE referenced twice -> materialize once", expensiveQuery, big, "orders")
	run("Cheap CTE referenced twice -> stay inlined", cheapQuery, small, "customers")
}
