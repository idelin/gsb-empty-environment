package allen

import "testing"

func TestComposeBasicPairs(t *testing.T) {
	cases := []struct {
		name string
		a, b Relation
		want Relation
	}{
		{"before before", Before, Before, Before},
		{"meets starts", Meets, Starts, Meets},
		{"before during", Before, During, Before | Meets | Overlaps | Starts | During},
		{"overlaps overlaps", Overlaps, Overlaps, Before | Meets | Overlaps},
		{"before after", Before, After, Any},
		{"meets met-by", Meets, MetBy, Finishes | Equal | FinishedBy},
		{"before overlaps", Before, Overlaps, Before},
		{"during during", During, During, During},
	}
	for _, c := range cases {
		if got := Compose(c.a, c.b); got != c.want {
			t.Errorf("%s: Compose(%v, %v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestComposeWithIdentity(t *testing.T) {
	for _, r := range Basics() {
		if got := Compose(Equal, r); got != r {
			t.Errorf("Compose(eq, %v) = %v", r, got)
		}
		if got := Compose(r, Equal); got != r {
			t.Errorf("Compose(%v, eq) = %v", r, got)
		}
	}
}

// r3 in r1∘r2  <=>  converse(r3) in converse(r2)∘converse(r1)
func TestComposeConverseSymmetry(t *testing.T) {
	for _, r1 := range Basics() {
		for _, r2 := range Basics() {
			comp := Compose(r1, r2)
			compConv := Compose(Converse(r2), Converse(r1))
			for _, r3 := range Basics() {
				if (comp&r3 != 0) != (compConv&Converse(r3) != 0) {
					t.Errorf("asymmetry: %v in %v∘%v but converse not in %v∘%v",
						r3, r1, r2, Converse(r2), Converse(r1))
				}
			}
		}
	}
}

func TestConverseInvolution(t *testing.T) {
	for _, r := range Basics() {
		if Converse(Converse(r)) != r {
			t.Errorf("Converse(Converse(%v)) != %v", r, r)
		}
	}
	if Converse(Any) != Any {
		t.Error("Converse(Any) != Any")
	}
}

func TestNetworkPropagate(t *testing.T) {
	// A before B, B overlaps C  =>  A before C (derived).
	net := NewNetwork(3)
	net.Constrain(0, 1, Before)
	net.Constrain(1, 2, Overlaps)
	if !net.Propagate() {
		t.Fatal("network unexpectedly inconsistent")
	}
	if got := net.Relation(0, 2); got != Before {
		t.Errorf("A->C = %v, want b", got)
	}
	if got := net.Relation(2, 0); got != After {
		t.Errorf("C->A = %v, want bi", got)
	}
}

func TestNetworkPropagateChain(t *testing.T) {
	// A meets B, B meets C, C before D  =>  A before D.
	net := NewNetwork(4)
	net.Constrain(0, 1, Meets)
	net.Constrain(1, 2, Meets)
	net.Constrain(2, 3, Before)
	if !net.Propagate() {
		t.Fatal("network unexpectedly inconsistent")
	}
	if got := net.Relation(0, 3); got != Before {
		t.Errorf("A->D = %v, want b", got)
	}
	if got := net.Relation(0, 2); got != Before {
		t.Errorf("A->C = %v, want b (meets∘meets)", got)
	}
}

func TestNetworkInconsistent(t *testing.T) {
	// A before B, B before C, but also A after C: propagation must
	// derive A before C and intersect it to empty.
	net := NewNetwork(3)
	net.Constrain(0, 1, Before)
	net.Constrain(1, 2, Before)
	net.Constrain(0, 2, After)
	if net.Propagate() {
		t.Fatal("expected inconsistency to be detected")
	}
}

func TestEndpointConstraints(t *testing.T) {
	// Singleton: exact defining constraints.
	cons := EndpointConstraints(Before)
	if len(cons) != 1 || cons[0] != (EndpointConstraint{From: StartY, To: EndX, Strict: true}) {
		t.Errorf("EndpointConstraints(b) = %v", cons)
	}
	// Disjunction {b, m}: only endX <= startY (non-strict) survives.
	cons = EndpointConstraints(Before | Meets)
	if len(cons) != 1 || cons[0] != (EndpointConstraint{From: StartY, To: EndX}) {
		t.Errorf("EndpointConstraints(b|m) = %v", cons)
	}
	// Universal relation implies nothing.
	if cons := EndpointConstraints(Any); len(cons) != 0 {
		t.Errorf("EndpointConstraints(any) = %v, want none", cons)
	}
}
