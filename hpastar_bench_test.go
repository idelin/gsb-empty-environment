package hpastar

import (
	"math/rand"
	"testing"
)

func makeBenchSetup(b *testing.B, size, cs int, rate float64) (*Hierarchy, *Grid, []Cell, []Cell) {
	b.Helper()
	rng := rand.New(rand.NewSource(99))
	g := randomGrid(rng, size, size, rate)
	hier := NewHierarchy(g, cs)
	starts := make([]Cell, 64)
	goals := make([]Cell, 64)
	for i := range starts {
		starts[i] = randomWalkableCell(rng, g)
		goals[i] = randomWalkableCell(rng, g)
	}
	return hier, g, starts, goals
}

func BenchmarkHierarchyBuild(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		rng := rand.New(rand.NewSource(99))
		g := randomGrid(rng, 128, 128, 0.2)
		b.StartTimer()
		NewHierarchy(g, 10)
	}
}

func BenchmarkHPAQuery(b *testing.B) {
	hier, _, starts, goals := makeBenchSetup(b, 128, 10, 0.2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := i & 63
		if _, _, _, ok := hier.FindPath(starts[k], goals[k]); !ok {
			b.Skip("unreachable query")
		}
	}
}

func BenchmarkFullAStarQuery(b *testing.B) {
	_, g, starts, goals := makeBenchSetup(b, 128, 10, 0.2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := i & 63
		if _, _, ok := FullAStar(g, starts[k], goals[k]); !ok {
			b.Skip("unreachable query")
		}
	}
}

func BenchmarkObstacleUpdate(b *testing.B) {
	rng := rand.New(rand.NewSource(123))
	g := randomGrid(rng, 128, 128, 0.2)
	hier := NewHierarchy(g, 10)
	changes := make(map[Cell]bool, 4)
	for i := 0; i < 4; i++ {
		c := randomWalkableCell(rng, g)
		changes[c] = true
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := hier.UpdateObstacles(changes); err != nil {
			b.Fatal(err)
		}
		for c := range changes {
			changes[c] = !g.Blocked(c.X, c.Y)
		}
	}
}
