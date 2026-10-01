package phyprop

import "testing"

func TestOrderSatisfiedBy(t *testing.T) {
	cases := []struct {
		name            string
		required, deliv Order
		want            bool
	}{
		{"empty requirement", nil, nil, true},
		{"exact match", Order{Asc("u.id")}, Order{Asc("u.id")}, true},
		{"prefix of wider index", Order{Asc("u.id")}, Order{Asc("u.id"), Asc("u.name")}, true},
		{"direction mismatch", Order{Desc("u.id")}, Order{Asc("u.id")}, false},
		{"wrong column", Order{Asc("u.id")}, Order{Asc("u.name")}, false},
		{"delivered too short", Order{Asc("a"), Asc("b")}, Order{Asc("a")}, false},
		{"unordered delivery", Order{Asc("u.id")}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.required.SatisfiedBy(tc.deliv); got != tc.want {
				t.Fatalf("SatisfiedBy = %v, want %v", got, tc.want)
			}
		})
	}
}

// Acceptance boundary: ORDER BY u.id with IndexScan(u.id) underneath must
// NOT gain a physical Sort operator.
func TestOrderBySatisfiedByIndexScan(t *testing.T) {
	plan := NewIndexScan("users", Asc("u.id"))
	got := Enforce(plan, Order{Asc("u.id")})

	if n := CountSorts(got); n != 0 {
		t.Fatalf("expected no Sort enforcer, got %d\nplan:\n%s", n, Format(got))
	}
	if _, ok := got.(*IndexScan); !ok {
		t.Fatalf("root should remain IndexScan, got %T", got)
	}
}

func TestOrderByOverSeqScanInsertsSort(t *testing.T) {
	got := Enforce(NewSeqScan("users"), Order{Asc("u.id")})

	sort, ok := got.(*Sort)
	if !ok {
		t.Fatalf("expected Sort root, got %T\nplan:\n%s", got, Format(got))
	}
	if got := sort.Required.String(); got != "u.id ASC" {
		t.Fatalf("Sort requirement = %q, want %q", got, "u.id ASC")
	}
	if _, ok := sort.Children()[0].(*SeqScan); !ok {
		t.Fatalf("Sort child should be SeqScan, got %T", sort.Children()[0])
	}
}

func TestStreamAggUsesIndexOrder(t *testing.T) {
	plan := NewStreamAgg(Order{Asc("u.id")}, NewIndexScan("users", Asc("u.id")))
	got := Enforce(plan, nil)
	if n := CountSorts(got); n != 0 {
		t.Fatalf("expected no Sort enforcer, got %d\nplan:\n%s", n, Format(got))
	}
}

func TestStreamAggOverSeqScanInsertsSort(t *testing.T) {
	plan := NewStreamAgg(Order{Asc("u.id")}, NewSeqScan("users"))
	got := Enforce(plan, nil)

	agg, ok := got.(*StreamAgg)
	if !ok {
		t.Fatalf("expected StreamAgg root, got %T", got)
	}
	if _, ok := agg.Children()[0].(*Sort); !ok {
		t.Fatalf("expected Sort below StreamAgg, got %T\nplan:\n%s", agg.Children()[0], Format(got))
	}
}

func TestMergeJoinEnforcesOnlyUnsortedSide(t *testing.T) {
	plan := NewMergeJoin(
		Order{Asc("u.id")},
		Order{Asc("o.user_id")},
		NewIndexScan("users", Asc("u.id")),
		NewSeqScan("orders"),
	)
	got := Enforce(plan, Order{Asc("u.id")})

	join, ok := got.(*MergeJoin)
	if !ok {
		t.Fatalf("expected MergeJoin root (left order preserved), got %T\nplan:\n%s", got, Format(got))
	}
	if _, ok := join.Children()[0].(*IndexScan); !ok {
		t.Fatalf("left child should stay IndexScan, got %T", join.Children()[0])
	}
	if _, ok := join.Children()[1].(*Sort); !ok {
		t.Fatalf("right child should gain Sort, got %T", join.Children()[1])
	}
	if n := CountSorts(got); n != 1 {
		t.Fatalf("expected exactly 1 Sort, got %d\nplan:\n%s", n, Format(got))
	}
}

func TestMergeJoinBothSidesIndexed(t *testing.T) {
	plan := NewMergeJoin(
		Order{Asc("u.id")},
		Order{Asc("o.user_id")},
		NewIndexScan("users", Asc("u.id")),
		NewIndexScan("orders", Asc("o.user_id")),
	)
	got := Enforce(plan, Order{Asc("u.id")})
	if n := CountSorts(got); n != 0 {
		t.Fatalf("expected no Sort enforcers, got %d\nplan:\n%s", n, Format(got))
	}
}

func TestOrderPropagatesThroughProjectAndLimit(t *testing.T) {
	plan := NewLimit(10,
		NewProject([]string{"u.id", "u.name"},
			NewIndexScan("users", Asc("u.id"))))
	got := Enforce(plan, Order{Asc("u.id")})
	if n := CountSorts(got); n != 0 {
		t.Fatalf("expected no Sort enforcer, got %d\nplan:\n%s", n, Format(got))
	}
}

func TestHashAggDestroysOrder(t *testing.T) {
	plan := NewHashAgg([]string{"u.id"}, NewIndexScan("users", Asc("u.id")))
	got := Enforce(plan, Order{Asc("u.id")})
	if _, ok := got.(*Sort); !ok {
		t.Fatalf("HashAgg output is unordered; expected Sort root, got %T", got)
	}
}

func TestEnforceIsBottomUp(t *testing.T) {
	// Inner requirement (from StreamAgg) and outer requirement (ORDER BY)
	// are both satisfied by one index scan: zero Sorts anywhere.
	plan := NewStreamAgg(Order{Asc("u.id")}, NewIndexScan("users", Asc("u.id")))
	got := Enforce(plan, Order{Asc("u.id")})
	if n := CountSorts(got); n != 0 {
		t.Fatalf("expected no Sort enforcers, got %d\nplan:\n%s", n, Format(got))
	}
}
