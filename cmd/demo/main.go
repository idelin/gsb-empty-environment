// Demo of bottom-up physical property (sort order) enforcement.
package main

import (
	"fmt"

	"phyprop"
)

func show(title string, plan phyprop.Node, required phyprop.Order) {
	fmt.Printf("== %s ==\n", title)
	fmt.Printf("required by parent: %s\n", required.String())
	optimized := phyprop.Enforce(plan, required)
	fmt.Print(phyprop.Format(optimized))
	fmt.Printf("Sort enforcers inserted: %d\n\n", phyprop.CountSorts(optimized))
}

func main() {
	// Acceptance case: SELECT ... FROM users u ORDER BY u.id, where the
	// table is read via IndexScan on u.id. The index already delivers the
	// required order, so no physical Sort operator (and no expensive
	// external sort) is needed.
	show(
		"ORDER BY u.id over IndexScan(u.id) -> Sort omitted",
		phyprop.NewIndexScan("users", phyprop.Asc("u.id")),
		phyprop.Order{phyprop.Asc("u.id")},
	)

	// Same query over a sequential scan: the optimizer must insert a Sort.
	show(
		"ORDER BY u.id over SeqScan -> Sort enforced",
		phyprop.NewSeqScan("users"),
		phyprop.Order{phyprop.Asc("u.id")},
	)

	// StreamAgg requires grouped input; index on u.id satisfies it.
	show(
		"StreamAgg(group by u.id) over IndexScan(u.id) -> Sort omitted",
		phyprop.NewStreamAgg(
			phyprop.Order{phyprop.Asc("u.id")},
			phyprop.NewIndexScan("users", phyprop.Asc("u.id")),
		),
		nil,
	)

	// MergeJoin: left side indexed, right side not. Only the right side
	// gets a Sort; the top-level ORDER BY u.id is then satisfied by the
	// merge join's preserved left order.
	show(
		"MergeJoin(u.id = o.user_id) + ORDER BY u.id, mixed inputs",
		phyprop.NewMergeJoin(
			phyprop.Order{phyprop.Asc("u.id")},
			phyprop.Order{phyprop.Asc("o.user_id")},
			phyprop.NewIndexScan("users", phyprop.Asc("u.id")),
			phyprop.NewSeqScan("orders"),
		),
		phyprop.Order{phyprop.Asc("u.id")},
	)
}
