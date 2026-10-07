package rematch

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestStressBruteForce(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		rng := rand.New(rand.NewSource(seed))
		for trial := 0; trial < 500; trial++ {
			n := 1 + rng.Intn(6)
			m := rng.Intn(7)
			var full [][2]int
			for e := 0; e < n; e++ {
				for p := 0; p < m; p++ {
					if rng.Float64() < 0.55 {
						full = append(full, [2]int{e, p})
					}
				}
			}
			orig := make([]int, n)
			usedP := make([]bool, m)
			for e := 0; e < n; e++ {
				orig[e] = Unassigned
			}
			for _, e := range rng.Perm(n) {
				var cands []int
				for _, ed := range full {
					if ed[0] == e && !usedP[ed[1]] {
						cands = append(cands, ed[1])
					}
				}
				if len(cands) > 0 {
					p := cands[rng.Intn(len(cands))]
					orig[e] = p
					usedP[p] = true
				}
			}
			var edges [][2]int
			for _, ed := range full {
				if rng.Float64() < 0.65 {
					edges = append(edges, ed)
				}
			}
			res := Reassign(n, m, edges, orig)
			checkValid(t, n, m, edges, res.Assignment)
			want := bruteForce(n, m, buildAdj(n, m, edges), orig)
			if !reflect.DeepEqual(res.Assignment, want) {
				t.Fatalf("seed=%d trial=%d n=%d m=%d edges=%v orig=%v\ngot  %v\nwant %v",
					seed, trial, n, m, edges, orig, res.Assignment, want)
			}
			checkChains(t, n, orig, res.Assignment, res)
		}
	}
}
