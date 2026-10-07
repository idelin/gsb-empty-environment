package flowsec

import "testing"

func TestChainLatticeOrder(t *testing.T) {
	l, err := ChainLattice("Public", "Internal", "Secret")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Leq(0, 2) || l.Leq(2, 0) {
		t.Fatal("chain order wrong")
	}
	if l.Join(0, 2) != 2 || l.Meet(0, 2) != 0 || l.Join(1, 1) != 1 {
		t.Fatal("chain join/meet wrong")
	}
}

func TestDiamondLattice(t *testing.T) {
	// bot < {a, b} < top
	l, err := NewLattice([]string{"bot", "a", "b", "top"}, [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	if l.Join(1, 2) != 3 {
		t.Fatal("join(a,b) should be top")
	}
	if l.Meet(1, 2) != 0 {
		t.Fatal("meet(a,b) should be bot")
	}
	if l.Leq(1, 2) || l.Leq(2, 1) {
		t.Fatal("a and b must be incomparable")
	}
}

func TestNonLatticeRejected(t *testing.T) {
	// two incomparable minimal upper bounds for {a,b}: c and d both
	// above a and b, with nothing relating them.
	_, err := NewLattice([]string{"a", "b", "c", "d"}, [][2]int{{0, 2}, {0, 3}, {1, 2}, {1, 3}})
	if err == nil {
		t.Fatal("expected non-lattice to be rejected")
	}
}
