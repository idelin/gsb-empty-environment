// Command demo simulates a 3-node cluster (A, B, C) exchanging messages
// for several rounds, then shows node A using its matrix clock to safely
// truncate its physical message log.
package main

import (
	"fmt"

	"matrixclock"
)

func main() {
	ids := []string{"A", "B", "C"}
	nodes := make(map[string]*matrixclock.Node, len(ids))
	for _, id := range ids {
		peers := make([]string, 0, len(ids)-1)
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		nodes[id] = matrixclock.NewNode(id, peers)
	}
	hub := matrixclock.NewHub(nodes["A"], nodes["B"], nodes["C"])

	for round := 1; round <= 4; round++ {
		for _, id := range ids {
			n := nodes[id]
			n.LocalEvent(fmt.Sprintf("round-%d local work", round))
			for _, peer := range ids {
				if peer != id {
					hub.Send(n.Send(peer, fmt.Sprintf("round-%d %s->%s", round, id, peer)))
				}
			}
		}
		delivered := hub.DeliverAll()
		fmt.Printf("round %d: delivered %d messages, A's safe LSN = %d\n",
			round, delivered, nodes["A"].SafeLSN())
	}

	a := nodes["A"]
	fmt.Println("\nA's matrix clock (rows: knowledge of, columns: about):")
	matrix := a.Matrix()
	for _, i := range ids {
		fmt.Printf("  row %s: {", i)
		for _, j := range ids {
			fmt.Printf(" %s:%d", j, matrix[i][j])
		}
		fmt.Println(" }")
	}
	fmt.Printf("\nA's GC watermark (min of each column): %v\n", a.GCWatermark())
	fmt.Printf("A's log before truncation: %d entries, LSN %d..%d\n",
		len(a.Log()), a.Log()[0].LSN, a.Log()[len(a.Log())-1].LSN)

	truncated := a.Truncate()
	fmt.Printf("\n==> A proves every event up to LSN %d is acknowledged by ALL nodes\n", truncated)
	fmt.Printf("==> physical log truncated; %d entries remain, starting at LSN %d\n",
		len(a.Log()), a.BaseLSN())
	for _, ev := range a.Log() {
		fmt.Println("   ", ev)
	}
}
