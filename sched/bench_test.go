package sched

import (
	"math/rand"
	"testing"
)

// BenchmarkSolve12 measures a full-size (12 orders) instance.
func BenchmarkSolve12(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	p := randomProblem(rng, MaxOrders, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Solve(p); err != nil {
			b.Fatal(err)
		}
	}
}
