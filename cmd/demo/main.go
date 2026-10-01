// Demo: progressive migration from Top_Old to Top_New with a 10%-step
// canary rollout, verifying zero request loss at every step.
package main

import (
	"fmt"
	"log"

	"migration"
)

func main() {
	// 1. Build the two cluster topologies (3 nodes each).
	var oldNodes, newNodes []migration.Node
	for i := 0; i < 3; i++ {
		oldNodes = append(oldNodes, migration.NewMemNode(fmt.Sprintf("old-node-%d", i)))
		newNodes = append(newNodes, migration.NewMemNode(fmt.Sprintf("new-node-%d", i)))
	}
	topOld, err := migration.NewTopology("Top_Old", oldNodes, 100)
	if err != nil {
		log.Fatal(err)
	}
	topNew, err := migration.NewTopology("Top_New", newNodes, 100)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Controller starts with 100% of traffic on Top_Old.
	ctrl, err := migration.NewController(topOld)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Seed 1000 keys through the controller (all land on Top_Old).
	const total = 1000
	for i := 0; i < total; i++ {
		ctrl.Set(fmt.Sprintf("user:%d", i), fmt.Sprintf("profile-%d", i))
	}
	fmt.Printf("seeded %d keys on %s\n", total, topOld.Name())

	// 4. Publish the new cluster configuration (no traffic shifted yet).
	if err := ctrl.PublishNewTopology(topNew); err != nil {
		log.Fatal(err)
	}
	fmt.Println("published new cluster config (Top_New)")

	// 5. Canary rollout: 10% -> 100% in 10% steps, verifying the new
	//    cluster before each step and checking for loss after each step.
	totalLost := 0
	for p := 10; p <= 100; p += 10 {
		if err := ctrl.VerifyNewCluster(); err != nil {
			log.Fatalf("new cluster not ready at %d%%: %v", p, err)
		}
		if err := ctrl.SetTrafficPercent(p); err != nil {
			log.Fatal(err)
		}

		// Serve a full read pass; the controller migrates keys lazily.
		lost := 0
		for i := 0; i < total; i++ {
			key := fmt.Sprintf("user:%d", i)
			v, ok := ctrl.Get(key)
			if !ok || v != fmt.Sprintf("profile-%d", i) {
				lost++
			}
		}
		totalLost += lost

		// Count how many keys are now owned by each generation.
		onNew := 0
		for i := 0; i < total; i++ {
			if ctrl.OwnerOf(fmt.Sprintf("user:%d", i)) == migration.GenNew {
				onNew++
			}
		}
		fmt.Printf("traffic=%3d%%  keys on Top_New=%4d/%d  lost reads=%d\n", p, onNew, total, lost)
	}

	// 6. Post-migration write check: all new writes go to Top_New.
	for i := 0; i < 10; i++ {
		ctrl.Set(fmt.Sprintf("fresh:%d", i), "new-value")
	}
	stats := ctrl.Stats()
	fmt.Printf("\nrequest stats: Top_Old=%d hits, Top_New=%d hits\n", stats.OldHits, stats.NewHits)
	fmt.Printf("migration complete: total lost reads across rollout = %d\n", totalLost)
	if totalLost != 0 {
		log.Fatal("FAIL: packet loss detected during migration")
	}
	fmt.Println("OK: zero loss during progressive migration")
}
