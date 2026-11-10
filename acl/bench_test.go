package acl

import (
	"math/rand"
	"testing"
)

func BenchmarkDiff100k(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	rulesOld := randomRules(rng, 100000)
	rulesNew := randomRules(rng, 100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blocks, err := Diff(rulesOld, rulesNew)
		if err != nil {
			b.Fatal(err)
		}
		if len(blocks) == 0 {
			b.Fatal("expected non-empty diff")
		}
	}
}

func BenchmarkCompile100k(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	rules := randomRules(rng, 100000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Compile(rules)
	}
}
