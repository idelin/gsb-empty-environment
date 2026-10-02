package cteopt

import "fmt"

// DemoTables builds the sample data set: 200 users and 1000 orders.
//
//	users:  (user_id, name, region)
//	orders: (order_id, user_id, amount)
func DemoTables() map[string][]Row {
	users := make([]Row, 0, 200)
	for i := 0; i < 200; i++ {
		users = append(users, Row{i, fmt.Sprintf("user%03d", i), []string{"east", "west", "north", "south"}[i%4]})
	}
	orders := make([]Row, 0, 1000)
	for i := 0; i < 1000; i++ {
		orders = append(orders, Row{i, i % 200, float64((i*37)%2000 + 1)})
	}
	return map[string][]Row{"users": users, "orders": orders}
}

// DemoSQL is the query the demo plan corresponds to.
const DemoSQL = `WITH big_orders AS (
  SELECT o.order_id, o.amount, u.region
  FROM orders o JOIN users u ON o.user_id = u.user_id
  WHERE o.amount > 100
)
SELECT region, COUNT(*) FROM big_orders GROUP BY region
UNION ALL
SELECT region, COUNT(*) FROM big_orders WHERE amount > 1000 GROUP BY region`

// BuildDemoPlan returns the plan DAG for DemoSQL. The CTE subtree is a
// single *Node shared by both branches, exactly like a real CTE reference.
// It also returns the CTE root so callers can inspect execution stats.
func BuildDemoPlan() (root *Node, cte *Node) {
	orders := Scan("orders")
	users := Scan("users")
	join := Join("orders.user_id = users.user_id",
		orders, users,
		func(r Row) any { return r[1] }, // orders.user_id
		func(r Row) any { return r[0] }, // users.user_id
	)
	// joined row: (order_id, user_id, amount, user_id, name, region)
	cte = Filter("amount > 100", func(r Row) bool { return r[2].(float64) > 100 }, join)

	region := func(r Row) any { return r[5] }
	branchA := Aggregate("orders per region", region, "count", 0, cte)
	branchB := Aggregate("vip orders per region", region, "count", 0,
		Filter("amount > 1000", func(r Row) bool { return r[2].(float64) > 1000 }, cte),
	)
	return UnionAll(branchA, branchB), cte
}

// BuildCheapCTEPlan returns a plan whose shared subexpression is so cheap
// that the optimizer should keep it inlined.
func BuildCheapCTEPlan() (root *Node, cte *Node) {
	cte = Scan("regions")
	north := Filter("region = 'north'", func(r Row) bool { return r[0].(string) == "north" }, cte)
	south := Filter("region = 'south'", func(r Row) bool { return r[0].(string) == "south" }, cte)
	return UnionAll(north, south), cte
}

// CheapTables provides the tiny table for BuildCheapCTEPlan.
func CheapTables() map[string][]Row {
	return map[string][]Row{
		"regions": {Row{"east"}, Row{"west"}, Row{"north"}, Row{"south"}},
	}
}
