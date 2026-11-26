package admission

import (
	"fmt"
	"math/big"
)

func ExampleGate() {
	cfg := Config{
		Variables: []Variable{
			{Name: "a", Modulus: big.NewInt(6)},
			{Name: "b", Modulus: big.NewInt(6)},
		},
		Policies: []CollusionPolicy{{
			Name: "ab",
			Protected: []ProtectedForm{
				{Name: "a", Modulus: big.NewInt(6),
					Terms: []Term{{Variable: "a", Coefficient: big.NewInt(1)}}},
				{Name: "b", Modulus: big.NewInt(6),
					Terms: []Term{{Variable: "b", Coefficient: big.NewInt(1)}}},
			},
			MaxShrink: Shrink(6, 1),
		}},
	}
	g, _ := NewGate(cfg)
	rep, _ := g.Admit(&Batch{
		ID: "release-1",
		Releases: []ReleaseRow{{
			ID: "sum", Modulus: big.NewInt(6),
			Terms: []Term{
				{Variable: "a", Coefficient: big.NewInt(1)},
				{Variable: "b", Coefficient: big.NewInt(1)},
			},
			Value: big.NewInt(0),
		}},
	})
	fmt.Println(rep.CommittedFeasibleCount, rep.Policies[0].Post, rep.Policies[0].CumulativeShrink.RatString())
	// Output: 6 6 6
}
