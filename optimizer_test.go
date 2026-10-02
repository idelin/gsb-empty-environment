package physprop

import "testing"

var usersIDIndex = &Index{Name: "idx_users_id", Columns: []string{"u.id"}}

func usersScan(rows float64, withIndex bool) *Scan {
	s := &Scan{Table: "users u", NRows: rows}
	if withIndex {
		s.Index = usersIDIndex
	}
	return s
}

func TestSatisfies(t *testing.T) {
	cases := []struct {
		provided, required Order
		want               bool
	}{
		{nil, nil, true},
		{nil, Order{"u.id"}, false},
		{Order{"u.id"}, nil, true},
		{Order{"u.id"}, Order{"u.id"}, true},
		{Order{"u.id", "u.name"}, Order{"u.id"}, true}, // prefix satisfies
		{Order{"u.id"}, Order{"u.id", "u.name"}, false},
		{Order{"u.name"}, Order{"u.id"}, false},
	}
	for _, c := range cases {
		if got := Satisfies(c.provided, c.required); got != c.want {
			t.Errorf("Satisfies(%v, %v) = %v, want %v", c.provided, c.required, got, c.want)
		}
	}
}

// Acceptance boundary: ORDER BY u.id over IndexScan(u.id) must NOT get a
// physical Sort operator — the index already delivers the order.
func TestOrderByCoveredByIndexScan_SortElided(t *testing.T) {
	query := &LogicalSort{
		Keys:  Order{"u.id"},
		Child: usersScan(10000, true),
	}
	plan := (&Optimizer{}).Optimize(query)

	if ContainsSort(plan) {
		t.Fatalf("expected no Sort enforcer, got plan:\n%s", Format(plan))
	}
	scan, ok := plan.(*IndexScan)
	if !ok {
		t.Fatalf("expected root IndexScan, got %T\n%s", plan, Format(plan))
	}
	if !Satisfies(scan.Ordering(), Order{"u.id"}) {
		t.Fatalf("IndexScan ordering %v does not satisfy ORDER BY u.id", scan.Ordering())
	}
}

// Without an index the optimizer must enforce the order with a Sort.
func TestOrderByWithoutIndex_SortEnforced(t *testing.T) {
	query := &LogicalSort{
		Keys:  Order{"u.id"},
		Child: usersScan(10000, false),
	}
	plan := (&Optimizer{}).Optimize(query)

	sort, ok := plan.(*Sort)
	if !ok {
		t.Fatalf("expected root Sort enforcer, got %T\n%s", plan, Format(plan))
	}
	if _, ok := sort.Child.(*SeqScan); !ok {
		t.Fatalf("expected Sort over SeqScan, got %T", sort.Child)
	}
}

// GroupBy on an indexed column uses StreamAggregate with no Sort.
func TestGroupByUsesIndexOrder(t *testing.T) {
	query := &GroupBy{
		Keys:  []string{"u.id"},
		Child: usersScan(10000, true),
	}
	plan := (&Optimizer{}).Optimize(query)

	if ContainsSort(plan) {
		t.Fatalf("expected no Sort enforcer, got plan:\n%s", Format(plan))
	}
	if _, ok := plan.(*StreamAggregate); !ok {
		t.Fatalf("expected StreamAggregate, got %T\n%s", plan, Format(plan))
	}
}

// GroupBy without an index prefers HashAggregate over Sort+StreamAggregate.
func TestGroupByWithoutIndex_PrefersHashAggregate(t *testing.T) {
	query := &GroupBy{
		Keys:  []string{"u.id"},
		Child: usersScan(10000, false),
	}
	plan := (&Optimizer{}).Optimize(query)

	if _, ok := plan.(*HashAggregate); !ok {
		t.Fatalf("expected HashAggregate, got %T\n%s", plan, Format(plan))
	}
}

// MergeJoin with both inputs indexed on the join keys: no Sort anywhere,
// and the join output stays ordered on the left keys.
func TestMergeJoinBothSidesIndexed_NoSort(t *testing.T) {
	ordersIndex := &Index{Name: "idx_orders_user", Columns: []string{"o.user_id"}}
	query := &LogicalMergeJoin{
		LeftKeys:  Order{"u.id"},
		RightKeys: Order{"o.user_id"},
		Left:      usersScan(10000, true),
		Right:     &Scan{Table: "orders o", NRows: 50000, Index: ordersIndex},
	}
	plan := (&Optimizer{}).Optimize(query)

	if ContainsSort(plan) {
		t.Fatalf("expected no Sort enforcer, got plan:\n%s", Format(plan))
	}
	join, ok := plan.(*MergeJoin)
	if !ok {
		t.Fatalf("expected MergeJoin, got %T\n%s", plan, Format(plan))
	}
	if !Satisfies(join.Ordering(), Order{"u.id"}) {
		t.Fatalf("MergeJoin output ordering %v, want prefix u.id", join.Ordering())
	}
}

// MergeJoin without indexes enforces Sort on both inputs.
func TestMergeJoinWithoutIndexes_SortsEnforced(t *testing.T) {
	query := &LogicalMergeJoin{
		LeftKeys:  Order{"u.id"},
		RightKeys: Order{"o.user_id"},
		Left:      usersScan(10000, false),
		Right:     &Scan{Table: "orders o", NRows: 50000},
	}
	plan := (&Optimizer{}).Optimize(query)

	join, ok := plan.(*MergeJoin)
	if !ok {
		t.Fatalf("expected MergeJoin, got %T\n%s", plan, Format(plan))
	}
	if _, ok := join.Left.(*Sort); !ok {
		t.Fatalf("expected Sort on left input, got %T\n%s", join.Left, Format(plan))
	}
	if _, ok := join.Right.(*Sort); !ok {
		t.Fatalf("expected Sort on right input, got %T\n%s", join.Right, Format(plan))
	}
}

// ORDER BY u.id on top of a MergeJoin whose left key is u.id: the join
// already preserves that order, so no extra Sort is added on top.
func TestOrderByAfterMergeJoin_SortElided(t *testing.T) {
	ordersIndex := &Index{Name: "idx_orders_user", Columns: []string{"o.user_id"}}
	query := &LogicalSort{
		Keys: Order{"u.id"},
		Child: &LogicalMergeJoin{
			LeftKeys:  Order{"u.id"},
			RightKeys: Order{"o.user_id"},
			Left:      usersScan(10000, true),
			Right:     &Scan{Table: "orders o", NRows: 50000, Index: ordersIndex},
		},
	}
	plan := (&Optimizer{}).Optimize(query)

	if ContainsSort(plan) {
		t.Fatalf("expected no Sort enforcer, got plan:\n%s", Format(plan))
	}
	if _, ok := plan.(*MergeJoin); !ok {
		t.Fatalf("expected root MergeJoin, got %T\n%s", plan, Format(plan))
	}
}

// ORDER BY on a column the index does not cover still needs a Sort.
func TestOrderByUncoveredColumn_SortEnforced(t *testing.T) {
	query := &LogicalSort{
		Keys:  Order{"u.name"},
		Child: usersScan(10000, true),
	}
	plan := (&Optimizer{}).Optimize(query)

	if !ContainsSort(plan) {
		t.Fatalf("expected Sort enforcer for u.name, got plan:\n%s", Format(plan))
	}
}
