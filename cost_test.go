package joinenum

import "testing"

func TestDefaultCostModelIntegers(t *testing.T) {
	m := DefaultCostModel{}
	cases := []struct {
		kind           OpKind
		left, right    int64
		sel            Selectivity
		wantOut, wantC int64
	}{
		{InnerJoin, 5, 4, Sel(1, 2), 10, 20},
		{LeftOuterJoin, 5, 4, Sel(1, 2), 12, 20},
		{SemiJoin, 5, 4, Sel(1, 2), 3, 20},
		{AntiJoin, 5, 4, Sel(1, 2), 2, 20},
		{LeftOuterJoin, 5, 0, Sel(1, 2), 5, 0},
		{AntiJoin, 5, 0, Sel(1, 2), 5, 0},
	}
	for _, tc := range cases {
		out, cost, err := m.Join(tc.kind, tc.left, tc.right, tc.sel)
		if err != nil {
			t.Fatal(err)
		}
		if out != tc.wantOut || cost != tc.wantC {
			t.Fatalf("%v(%d,%d,%v) = (%d,%d), want (%d,%d)",
				tc.kind, tc.left, tc.right, tc.sel, out, cost, tc.wantOut, tc.wantC)
		}
	}
}

func TestCostOverflow(t *testing.T) {
	m := DefaultCostModel{}
	if _, _, err := m.Join(InnerJoin, 1<<40, 1<<40, One); err == nil {
		t.Fatal("expected overflow")
	}
}
