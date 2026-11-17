package netsec

import "testing"

func TestLiteralAndFormulaStrings(t *testing.T) {
	if got := Lit(1).String(); got != "x1" {
		t.Fatalf("Lit(1).String() = %q", got)
	}
	if got := (-Lit(1)).String(); got != "¬x1" {
		t.Fatalf("(-Lit(1)).String() = %q", got)
	}

	falseIT := &Interpolant{Shared: []int{1}}
	if got := falseIT.String(); got != "false" {
		t.Fatalf("false interpolant string = %q", got)
	}

	trueIT := &Interpolant{Terms: []Term{{}}, Shared: nil}
	if got := trueIT.String(); got != "true" {
		t.Fatalf("true interpolant string = %q", got)
	}

	it := &Interpolant{
		Terms:  []Term{{Lit(1), -Lit(2)}},
		Shared: []int{1, 2},
	}
	if got := it.String(); got != "(x1 ∧ ¬x2)" {
		t.Fatalf("dnf interpolant string = %q", got)
	}

	w := &Witness{Assignment: map[int]bool{1: true, 2: false}}
	if got := w.String(); got != "{x1, ¬x2}" {
		t.Fatalf("witness string = %q", got)
	}
}

func TestLiteralPanics(t *testing.T) {
	t.Run("Lit zero", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = Lit(0)
	})
	t.Run("zero literal methods", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = Literal(0).Not()
	})
}
