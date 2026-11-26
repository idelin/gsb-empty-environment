package admission_test

import (
	"errors"
	"math/big"
	"testing"

	admission "formal.local/h26-20dcb4071f"
	sub "formal.local/h26-20dcb4071f/admission"
)

func TestPublicImportSurface(t *testing.T) {
	cfg := admission.Config{
		Variables: []admission.Variable{{Name: "x", Modulus: big.NewInt(6)}},
		Policies: []admission.CollusionPolicy{{
			Name: "x",
			Protected: []admission.ProtectedForm{{
				Name: "x", Modulus: big.NewInt(6),
				Terms: []admission.Term{{Variable: "x", Coefficient: big.NewInt(1)}},
			}},
			MaxShrink: admission.Shrink(2, 1),
		}},
	}
	g, err := admission.NewGate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Admit(&admission.Batch{ID: "b", Releases: []admission.ReleaseRow{{
		ID: "r", Modulus: big.NewInt(6), Terms: []admission.Term{{Variable: "x", Coefficient: big.NewInt(1)}}, Value: big.NewInt(0),
	}}})
	if !errors.Is(err, admission.ErrCollusionExceeded) {
		t.Fatalf("err=%v", err)
	}

	if _, err := sub.NewGate(cfg); err != nil {
		t.Fatalf("subpackage import: %v", err)
	}
}
