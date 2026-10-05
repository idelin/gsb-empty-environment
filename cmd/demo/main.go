// Command demo builds a bipartite multigraph, schedules its edges into
// max-degree many rounds, prints the matching of each round, then deletes
// and re-adds edges to show the schedule is recomputed cleanly.
package main

import (
	"fmt"

	"edgecolor"
)

func printRounds(s *edgecolor.Scheduler) {
	rounds := s.Rounds()
	fmt.Printf("max degree = %d, rounds = %d\n", s.MaxDegree(), len(rounds))
	for r, round := range rounds {
		fmt.Printf("  round %d: edges %v\n", r, round)
	}
	if err := s.Verify(rounds); err != nil {
		fmt.Println("  INVALID:", err)
	} else {
		fmt.Println("  verified OK")
	}
}

func main() {
	s := edgecolor.New()
	// K3,3 plus two parallel edges on (0,0).
	for u := 0; u < 3; u++ {
		for v := 0; v < 3; v++ {
			s.AddEdge(u, v)
		}
	}
	s.AddEdge(0, 0)
	s.AddEdge(0, 0)

	fmt.Println("== full graph ==")
	printRounds(s)

	fmt.Println("== after delete + re-add ==")
	edges := s.Edges()
	s.RemoveEdge(edges[0].ID)
	s.RemoveEdge(edges[5].ID)
	s.AddEdge(1, 0)
	s.AddEdge(2, 2)
	printRounds(s)
}
