// Demo: a 100-node cluster, one node joins, and we watch its JOIN event
// spread via piggybacked Ping/Ack gossip until everyone knows.
package main

import (
	"fmt"
	"math"

	"piggyback/gossip"
)

func main() {
	const n = 100
	cluster := gossip.NewCluster(n, 42)
	newbie := cluster.AddNode("node-new")

	fmt.Printf("cluster: %d nodes + newcomer %q\n", n, newbie.ID)
	fmt.Printf("dissemination cap: %d rounds (lambda*log2(N))\n", gossip.MaxRounds(n+1))
	fmt.Printf("%-8s %-12s %s\n", "period", "aware", "bar")

	period := 0
	for !cluster.AllKnow(newbie.ID) {
		cluster.RunPeriod()
		period++
		aware := cluster.AwareCount(newbie.ID)
		bar := ""
		for i := 0; i < aware*40/(n+1); i++ {
			bar += "#"
		}
		fmt.Printf("%-8d %-12d %s\n", period, aware, bar)
	}
	fmt.Printf("converged after %d periods (log2(%d) = %.1f)\n", period, n, math.Log2(n))
}
