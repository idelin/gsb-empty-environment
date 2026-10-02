package main

import (
	"fmt"

	"physprop"
)

func show(title, sql string, plan physprop.PhysicalPlan) {
	fmt.Printf("== %s ==\n%s\n\n%s", title, sql, physprop.Format(plan))
	if physprop.ContainsSort(plan) {
		fmt.Println("-> Sort enforcer INSERTED (input was unordered)")
	} else {
		fmt.Println("-> Sort enforcer ELIDED (order already provided)")
	}
	fmt.Println()
}

func main() {
	opt := &physprop.Optimizer{}
	usersID := &physprop.Index{Name: "idx_users_id", Columns: []string{"u.id"}}

	// 1. Acceptance case: ORDER BY u.id with an index on u.id.
	show(
		"ORDER BY u.id, index available",
		"SELECT * FROM users u ORDER BY u.id;",
		opt.Optimize(&physprop.LogicalSort{
			Keys:  physprop.Order{"u.id"},
			Child: &physprop.Scan{Table: "users u", NRows: 10000, Index: usersID},
		}),
	)

	// 2. Same query, no index: the optimizer must enforce with Sort.
	show(
		"ORDER BY u.id, no index",
		"SELECT * FROM users u ORDER BY u.id;",
		opt.Optimize(&physprop.LogicalSort{
			Keys:  physprop.Order{"u.id"},
			Child: &physprop.Scan{Table: "users u", NRows: 10000},
		}),
	)

	// 3. GroupBy over an indexed column: StreamAggregate, no Sort.
	show(
		"GROUP BY u.id, index available",
		"SELECT u.id, COUNT(*) FROM users u GROUP BY u.id;",
		opt.Optimize(&physprop.GroupBy{
			Keys:  []string{"u.id"},
			Child: &physprop.Scan{Table: "users u", NRows: 10000, Index: usersID},
		}),
	)

	// 4. MergeJoin + ORDER BY on the left join key, both sides indexed:
	//    no Sort anywhere in the plan.
	ordersUser := &physprop.Index{Name: "idx_orders_user", Columns: []string{"o.user_id"}}
	show(
		"MergeJoin + ORDER BY u.id, both sides indexed",
		"SELECT * FROM users u JOIN orders o ON u.id = o.user_id ORDER BY u.id;",
		opt.Optimize(&physprop.LogicalSort{
			Keys: physprop.Order{"u.id"},
			Child: &physprop.LogicalMergeJoin{
				LeftKeys:  physprop.Order{"u.id"},
				RightKeys: physprop.Order{"o.user_id"},
				Left:      &physprop.Scan{Table: "users u", NRows: 10000, Index: usersID},
				Right:     &physprop.Scan{Table: "orders o", NRows: 50000, Index: ordersUser},
			},
		}),
	)

	// 5. Same join without indexes: Sort enforced on both join inputs.
	show(
		"MergeJoin, no indexes",
		"SELECT * FROM users u JOIN orders o ON u.id = o.user_id;",
		opt.Optimize(&physprop.LogicalMergeJoin{
			LeftKeys:  physprop.Order{"u.id"},
			RightKeys: physprop.Order{"o.user_id"},
			Left:      &physprop.Scan{Table: "users u", NRows: 10000},
			Right:     &physprop.Scan{Table: "orders o", NRows: 50000},
		}),
	)
}
