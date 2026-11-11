package netsec

import "testing"

func BenchmarkInterpolate12Shared(b *testing.B) {
	shared := make([]int, MaxSharedVars)
	for i := range shared {
		shared[i] = i
	}
	x := MakeLit(0)
	a := CNF{{x}}
	succ := CNF{{x.Neg()}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := Interpolate(a, succ, shared)
		if err != nil || res.SAT || !res.Verification.Passed {
			b.Fatalf("invalid benchmark result: %+v err=%v", res, err)
		}
	}
}
