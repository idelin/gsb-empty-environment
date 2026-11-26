package seccomp

import (
	"math/rand"
	"testing"
)

// TestDifferentialEnumerationMultiSeed broadens the randomized cross-check
// across seeds.  It is skipped under -short.
func TestDifferentialEnumerationMultiSeed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-seed stress in -short mode")
	}
	argCandidates := []uint64{
		0, 1, 2, 3, 4, 7, 0xff, 0xffff, 0x1234, 0xbeef,
		0x7fffffff, 0x80000000, 0xbfffffff, 0xc000003e, 0xffffffff,
		0x100000000, 0xdeadbeef00000000, 0xdeadbeef00000001,
		0xffffffff00000000, 0xffffffffffffffff,
	}
	for seed := int64(1); seed <= 12; seed++ {
		rng := rand.New(rand.NewSource(seed*7919 + 13))
		for iter := 0; iter < 100; iter++ {
			activeArg := rng.Intn(ArgCount)
			insns, nr, argIndex, mask, want := genCascadeFilter(t, rng, activeArg)
			conds := []ArgCond{}
			if rng.Intn(2) == 0 {
				conds = append(conds, ArgCond{Index: argIndex, Mask: mask, Value: want})
			}
			pol := NewPolicy(Deny("r", []Arch{ArchX8664}, []uint32{nr}, conds...))
			rep, err := Verify(insns, pol)
			if err != nil {
				t.Fatalf("seed %d iter %d verify: %v", seed, iter, err)
			}
			for _, c := range rep.Counterexamples {
				act, err := RunTuple(insns, c.Tuple)
				if err != nil || !IsLaxAction(act, false) || pol.MatchesAnyRule(c.Tuple) == nil {
					t.Fatalf("false positive seed %d iter %d: %s act=%#x err=%v",
						seed, iter, c.Tuple, act, err)
				}
			}
			dom := EnumDomain{Archs: []Arch{ArchX8664}, NRs: []uint32{nr}}
			dom.ArgValues[activeArg] = argCandidates
			hits, _, err := Enumerate(insns, pol, dom)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) > 0 && rep.Complete && rep.Safe {
				t.Fatalf("false negative seed %d iter %d enum=%s\n%s",
					seed, iter, hits[0].Tuple, Disassemble(insns))
			}
		}
	}
}
