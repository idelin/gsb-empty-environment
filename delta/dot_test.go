package delta

import "testing"

func TestVersionVectorDominates(t *testing.T) {
	vv := VersionVector{"A": 3, "B": 1}
	cases := []struct {
		dot  Dot
		want bool
	}{
		{Dot{"A", 1}, true},
		{Dot{"A", 3}, true},
		{Dot{"A", 4}, false},
		{Dot{"B", 1}, true},
		{Dot{"B", 2}, false},
		{Dot{"C", 1}, false},
		{Dot{}, true}, // zero dot is always dominated
	}
	for _, c := range cases {
		if got := vv.Dominates(c.dot); got != c.want {
			t.Errorf("Dominates(%v) = %v, want %v", c.dot, got, c.want)
		}
	}
}

func TestVersionVectorJoin(t *testing.T) {
	a := VersionVector{"A": 3, "B": 1}
	b := VersionVector{"A": 2, "C": 5}
	got := a.Join(b)
	want := VersionVector{"A": 3, "B": 1, "C": 5}
	if !got.Equal(want) {
		t.Fatalf("join = %v, want %v", got, want)
	}
	// Join must not mutate its receivers.
	if !a.Equal(VersionVector{"A": 3, "B": 1}) {
		t.Fatalf("join mutated receiver: %v", a)
	}
}

func TestVersionVectorEqual(t *testing.T) {
	if !((VersionVector{"A": 1}).Equal(VersionVector{"A": 1})) {
		t.Fatal("equal vectors reported unequal")
	}
	if (VersionVector{"A": 1}).Equal(VersionVector{"A": 2}) {
		t.Fatal("unequal vectors reported equal")
	}
	if (VersionVector{"A": 1}).Equal(VersionVector{"A": 1, "B": 1}) {
		t.Fatal("different-length vectors reported equal")
	}
}
