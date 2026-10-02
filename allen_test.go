package tempsched

import "testing"

func TestComposeTable(t *testing.T) {
	cases := []struct {
		name string
		a, b Relation
		want Relation
	}{
		{"Before∘Before", Before, Before, Before},
		{"Before∘Meets", Before, Meets, Before},
		{"Meets∘Meets", Meets, Meets, Before},
		{"Before∘Overlaps", Before, Overlaps, Before},
		{"Before∘During", Before, During, Before | Meets | Overlaps | Starts | During},
		{"Meets∘Starts", Meets, Starts, Meets},
		{"Equal∘During", Equal, During, During},
		{"During∘Equal", During, Equal, During},
		{"Overlaps∘Before", Overlaps, Before, Before},
		{"After∘After", After, After, After},
	}
	for _, c := range cases {
		if got := Compose(c.a, c.b); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestComposeTableMatchesPublishedAllenTable(t *testing.T) {
	// Spot-check a full row of the well-known Allen composition table:
	// Overlaps composed with every atomic relation.
	want := map[Relation]Relation{
		Before:   Before,
		Meets:    Before,
		Overlaps: Before | Meets | Overlaps,
		Starts:   Overlaps,
		During:   Overlaps | Starts | During,
		Finishes: Overlaps | Starts | During,
		Equal:    Overlaps,
	}
	for b, w := range want {
		if got := Compose(Overlaps, b); got != w {
			t.Errorf("Overlaps∘%v: got %v, want %v", b, got, w)
		}
	}
}

func TestInverse(t *testing.T) {
	pairs := [][2]Relation{
		{Before, After}, {Meets, MetBy}, {Overlaps, OverlappedBy},
		{Starts, StartedBy}, {During, Contains}, {Finishes, FinishedBy},
		{Equal, Equal},
	}
	for _, p := range pairs {
		if Inverse(p[0]) != p[1] || Inverse(p[1]) != p[0] {
			t.Errorf("inverse mismatch for %v / %v", p[0], p[1])
		}
	}
}

func TestPathConsistencyTransitiveClosure(t *testing.T) {
	n := NewNetwork()
	n.AddTask("A")
	n.AddTask("B")
	n.AddTask("C")
	n.Constrain("A", "B", Before)
	n.Constrain("B", "C", Before)

	if !n.PathConsistent() {
		t.Fatal("network should be consistent")
	}
	if got := n.Relation("A", "C"); got != Before {
		t.Errorf("inferred A->C = %v, want Before", got)
	}
	if got := n.Relation("C", "A"); got != After {
		t.Errorf("inferred C->A = %v, want After", got)
	}
}

func TestPathConsistencyDetectsInconsistency(t *testing.T) {
	n := NewNetwork()
	for _, name := range []string{"A", "B", "C"} {
		n.AddTask(name)
	}
	n.Constrain("A", "B", Before)
	n.Constrain("B", "C", Before)
	n.Constrain("C", "A", Before) // cycle: A<B<C<A is impossible

	if n.PathConsistent() {
		t.Fatal("cyclic Before network must be inconsistent")
	}
}

func TestPathConsistencyTightensDisjunction(t *testing.T) {
	n := NewNetwork()
	n.AddTask("A")
	n.AddTask("B")
	n.AddTask("C")
	n.Constrain("A", "B", Meets)
	n.Constrain("B", "C", Overlaps|Starts|During)

	if !n.PathConsistent() {
		t.Fatal("network should be consistent")
	}
	// Meets∘(Overlaps|Starts|During) = Before|Meets|Overlaps|Starts|During
	want := Before | Meets | Overlaps | Starts | During
	if got := n.Relation("A", "C"); got != want {
		t.Errorf("inferred A->C = %v, want %v", got, want)
	}
}
