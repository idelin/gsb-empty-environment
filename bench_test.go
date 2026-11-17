package netsec

import "testing"

func BenchmarkInterpolateMaxShared(b *testing.B) {
	shared := make([]int, MaxShared)
	for i := range shared {
		shared[i] = i + 1
	}
	formulaA := make(CNF, 0, MaxShared)
	for _, v := range shared {
		formulaA = append(formulaA, Clause{Lit(v)})
	}
	formulaB := CNF{{-Lit(1)}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it, w, err := Interpolate(formulaA, formulaB, shared)
		if err != nil || w != nil || it == nil {
			b.Fatalf("unexpected result: %v %v %v", it, w, err)
		}
	}
}
