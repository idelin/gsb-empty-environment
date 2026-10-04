package corpustrim

import (
	"math/rand"
	"testing"
	"time"
)

// Dense coverage, uniform costs: many symmetric optima, stresses the
// cost-tie pruning.
func TestDenseUniform(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		rng := rand.New(rand.NewSource(seed))
		inputs := make([]Input, 30)
		for i := range inputs {
			in := Input{ID: string(rune('a' + i)), Content: string(rune('A' + i)), Cost: 10}
			for e := 0; e < 30; e++ {
				if rng.Intn(2) == 0 {
					in.Coverage = append(in.Coverage, string(rune('a'+e%26))+string(rune('0'+e/26)))
				}
			}
			inputs[i] = in
		}
		start := time.Now()
		res, err := Trim(inputs)
		if err != nil {
			t.Fatal(err)
		}
		checkCoversUniverse(t, inputs, res)
		t.Logf("seed %d: %d samples, %d nodes, %v", seed, res.Samples, res.SearchNodes, time.Since(start))
	}
}

// Sparse disjoint edges: each edge covered by exactly 2 inputs, forcing
// many near-forced choices.
func TestSparseBinary(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		rng := rand.New(rand.NewSource(seed))
		inputs := make([]Input, 30)
		for i := range inputs {
			in := Input{ID: string(rune('a' + i)), Content: string(rune('A' + i)), Cost: int64(rng.Intn(5))}
			for _, e := range rng.Perm(30)[:3] {
				in.Coverage = append(in.Coverage, string(rune('a'+e%26))+string(rune('0'+e/26)))
			}
			inputs[i] = in
		}
		start := time.Now()
		res, err := Trim(inputs)
		if err != nil {
			t.Fatal(err)
		}
		checkCoversUniverse(t, inputs, res)
		t.Logf("seed %d: %d samples, %d nodes, %v", seed, res.Samples, res.SearchNodes, time.Since(start))
	}
}
