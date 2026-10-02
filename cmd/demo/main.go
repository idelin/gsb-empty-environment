// Demo: a 100-node cluster where a new node joins through one seed, and its
// join event spreads purely via piggybacked Ping/Ack headers.
package main

import (
	"fmt"
	"math"

	"piggyback/gossip"
)

func main() {
	const total = 100
	c := gossip.NewCluster(total-1, 3, 6, 42)

	joined := c.Join(0)
	fmt.Printf("node %s joined via seed %s; disseminating via piggyback gossip\n",
		joined.ID, c.Nodes[0].ID)
	fmt.Printf("convergence budget: ceil(log2(%d)) = %d periods\n\n",
		total, int(math.Ceil(math.Log2(total))))

	for period := 1; ; period++ {
		c.RunPeriod()
		informed := c.InformedCount(joined.ID)
		fmt.Printf("period %2d: %3d/%d nodes aware of %s\n", period, informed, total, joined.ID)
		if informed == total {
			fmt.Printf("\nconverged: all %d nodes updated their membership view in %d periods\n",
				total, period)
			break
		}
	}

	m, _ := c.Nodes[total-2].Member(joined.ID)
	fmt.Printf("sample view entry on %s: %+v\n", c.Nodes[total-2].ID, m)
}
