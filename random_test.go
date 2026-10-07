package gsb

import (
	"math/rand"
	"testing"
)

// Cross-validate Solve against exhaustive enumeration on many small random
// instances. Auxiliary uses are sprinkled into options so that demands are coupled
// through shared auxiliary stock, which is exactly the interaction the
// branch-and-bound solver must respect.
func TestRandomCrossValidation(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := randomProblem(r)
		got, err := Solve(p)
		if err != nil {
			t.Fatalf("seed %d: Solve: %v", seed, err)
		}
		want, err := BruteForce(p)
		if err != nil {
			t.Fatalf("seed %d: BruteForce: %v", seed, err)
		}
		if got.CompleteOrders != want.CompleteOrders || got.Cost != want.Cost {
			t.Fatalf("seed %d: Solve = (%d orders, cost %d), BruteForce = (%d orders, cost %d)\nproblem: %+v",
				seed, got.CompleteOrders, got.Cost, want.CompleteOrders, want.Cost, p)
		}
		checkEvidence(t, p, got)
	}
}

func randomProblem(r *rand.Rand) *Problem {
	nMat := 1 + r.Intn(3)
	p := &Problem{}
	for i := 0; i < nMat; i++ {
		p.Materials = append(p.Materials, Material{
			Stock:    int64(r.Intn(6)),
			UnitCost: int64(r.Intn(5)),
		})
	}
	nDem := 1 + r.Intn(3)
	for d := 0; d < nDem; d++ {
		dem := Demand{Quantity: 1 + int64(r.Intn(2))}
		for o := 0; o < r.Intn(3); o++ {
			opt := Option{
				Material: r.Intn(nMat),
				Ratio:    1 + int64(r.Intn(2)),
			}
			for m := 0; m < nMat; m++ {
				if r.Intn(4) == 0 {
					opt.Aux = append(opt.Aux, AuxUse{Material: m, Amount: 1 + int64(r.Intn(2))})
				}
			}
			dem.Options = append(dem.Options, opt)
		}
		p.Demands = append(p.Demands, dem)
	}
	return p
}
