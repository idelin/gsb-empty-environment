package scheduler

import (
	"math/rand"
	"testing"
)

func TestStressRandomOps(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		s := New()
		var live []int
		for op := 0; op < 120; op++ {
			if len(live) == 0 || rng.Intn(3) > 0 {
				id := s.AddEdge(rng.Intn(6), rng.Intn(6))
				live = append(live, id)
			} else {
				i := rng.Intn(len(live))
				s.DeleteEdge(live[i])
				live = append(live[:i], live[i+1:]...)
			}
			verifySchedule(t, s)
		}
	}
}
