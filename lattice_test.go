package iflow

import "testing"

func chainLattice(t *testing.T) *Lattice {
	t.Helper()
	lat, err := NewLattice(
		[]string{"Public", "Internal", "Secret"},
		[][2]string{{"Public", "Internal"}, {"Internal", "Secret"}},
	)
	if err != nil {
		t.Fatalf("chain lattice: %v", err)
	}
	return lat
}

func TestChainLatticeOrder(t *testing.T) {
	lat := chainLattice(t)
	pub, _ := lat.LevelOf("Public")
	intl, _ := lat.LevelOf("Internal")
	sec, _ := lat.LevelOf("Secret")
	if !lat.Leq(pub, intl) || !lat.Leq(intl, sec) || !lat.Leq(pub, sec) {
		t.Fatal("expected Public <= Internal <= Secret")
	}
	if lat.Leq(sec, intl) || lat.Leq(intl, pub) {
		t.Fatal("unexpected downward order")
	}
	if lat.Join(pub, sec) != sec || lat.Join(intl, sec) != sec || lat.Join(pub, intl) != intl {
		t.Fatal("wrong joins on chain lattice")
	}
	if lat.Bottom() != pub {
		t.Fatal("bottom should be Public")
	}
}

func TestDiamondLatticeJoin(t *testing.T) {
	lat, err := NewLattice(
		[]string{"Bot", "A", "B", "Top"},
		[][2]string{{"Bot", "A"}, {"Bot", "B"}, {"A", "Top"}, {"B", "Top"}},
	)
	if err != nil {
		t.Fatalf("diamond lattice: %v", err)
	}
	a, _ := lat.LevelOf("A")
	b, _ := lat.LevelOf("B")
	top, _ := lat.LevelOf("Top")
	if lat.Join(a, b) != top {
		t.Fatal("join(A, B) should be Top")
	}
	if lat.Leq(a, b) || lat.Leq(b, a) {
		t.Fatal("A and B must be incomparable")
	}
}

func TestRejectsNonLattice(t *testing.T) {
	// a and b have two minimal upper bounds c and d and no top: no unique join.
	_, err := NewLattice(
		[]string{"a", "b", "c", "d"},
		[][2]string{{"a", "c"}, {"a", "d"}, {"b", "c"}, {"b", "d"}},
	)
	if err == nil {
		t.Fatal("expected construction to fail for non-lattice order")
	}
}

func TestLabelJoinIntersectsReceivers(t *testing.T) {
	lat := chainLattice(t)
	l1 := NewLabel(lat, "Internal", "audit", "finance")
	l2 := NewLabel(lat, "Secret", "audit")
	j := l1.Join(l2)
	if got := lat.Name(j.Level); got != "Secret" {
		t.Fatalf("join level = %s, want Secret", got)
	}
	if !j.Allows("audit") || j.Allows("finance") || j.Allows("public") {
		t.Fatalf("join receivers = %v, want only audit", j)
	}
	if BottomLabel(lat).Join(l2).Allows("audit") != true {
		t.Fatal("join with bottom should keep l2")
	}
}

func TestLabelOrdering(t *testing.T) {
	lat := chainLattice(t)
	wide := NewLabel(lat, "Internal", "audit", "finance")
	narrow := NewLabel(lat, "Internal", "audit")
	if !wide.Leq(narrow) || narrow.Leq(wide) {
		t.Fatal("more receivers = less restrictive")
	}
	if !NewLabel(lat, "Public", "audit", "finance").Leq(wide) {
		t.Fatal("lower level with wider receivers should be <=")
	}
	if NewLabel(lat, "Secret", "audit").Leq(wide) {
		t.Fatal("higher level must not be <= lower level")
	}
	if !wide.Leq(NewLabel(lat, "Secret")) {
		t.Fatal("lower level with wider receivers is <= higher level with none")
	}
}
