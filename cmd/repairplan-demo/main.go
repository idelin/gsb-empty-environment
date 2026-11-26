// Command repairplan-demo builds a small cluster topology and runs the
// fault-domain repair planner, printing the round-by-round repair timeline.
package main

import (
	"fmt"
	"os"

	"prescreen.local/ps-b3152aa848/repairplan"
)

func main() {
	nodes := []repairplan.Node{
		{ID: "root", Level: repairplan.LevelRoot, Online: true,
			RemainingCapacity: repairplan.Unlimited,
			IngressLimit:      repairplan.Unlimited, EgressLimit: repairplan.Unlimited},
		{ID: "row1", Level: repairplan.LevelRow, ParentID: "root", Online: true,
			RemainingCapacity: repairplan.Unlimited,
			IngressLimit:      repairplan.Unlimited, EgressLimit: repairplan.Unlimited},
	}
	const racks, hosts, osdsPerHost = 4, 2, 2
	for r := 1; r <= racks; r++ {
		rackID := fmt.Sprintf("rack%d", r)
		egress := repairplan.Unlimited
		if r == 1 {
			egress = 100 // tight ToR uplink forces repair queuing
		}
		nodes = append(nodes, repairplan.Node{
			ID: rackID, Level: repairplan.LevelRack, ParentID: "row1", Online: true,
			RemainingCapacity: repairplan.Unlimited,
			IngressLimit:      repairplan.Unlimited, EgressLimit: int64(egress),
		})
		for h := 1; h <= hosts; h++ {
			hostID := fmt.Sprintf("%s_host%d", rackID, h)
			nodes = append(nodes, repairplan.Node{
				ID: hostID, Level: repairplan.LevelHost, ParentID: rackID, Online: true,
				RemainingCapacity: repairplan.Unlimited,
				IngressLimit:      repairplan.Unlimited, EgressLimit: repairplan.Unlimited,
			})
			for o := 1; o <= osdsPerHost; o++ {
				osdID := fmt.Sprintf("%s_osd%d", hostID, o)
				nodes = append(nodes, repairplan.Node{
					ID: osdID, Level: repairplan.LevelOSD, ParentID: hostID, Online: true,
					RemainingCapacity: repairplan.Unlimited,
					IngressLimit:      repairplan.Unlimited, EgressLimit: repairplan.Unlimited,
				})
			}
		}
	}

	topo, err := repairplan.NewTopology(nodes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	stripes := []repairplan.Stripe{
		// Margin 0 (two survivors == k=2): repaired first.
		{
			ID: "pg-critical", K: 2, M: 2, ShardSize: 100,
			FaultDomain: repairplan.LevelRack,
			Shards: []repairplan.ShardPlacement{
				{Index: 0, OSDID: "rack1_host1_osd1"},
				{Index: 1, OSDID: "rack2_host1_osd1"},
			},
		},
		// EC k=2,m=2 with three survivors (margin 1); needs k sources.
		{
			ID: "ec-wide", K: 2, M: 2, ShardSize: 100,
			FaultDomain: repairplan.LevelRack,
			Shards: []repairplan.ShardPlacement{
				{Index: 0, OSDID: "rack1_host2_osd1"},
				{Index: 1, OSDID: "rack2_host2_osd1"},
				{Index: 2, OSDID: "rack3_host1_osd1"},
			},
		},
		// k=3 with only two survivors: unrecoverable, never scheduled.
		{
			ID: "pg-dead", K: 3, M: 2, ShardSize: 100,
			FaultDomain: repairplan.LevelRack,
			Shards: []repairplan.ShardPlacement{
				{Index: 0, OSDID: "rack1_host1_osd2"},
				{Index: 1, OSDID: "rack2_host1_osd2"},
			},
		},
	}

	plan, err := repairplan.BuildPlan(topo, stripes, repairplan.Options{
		GlobalConcurrent: 10,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := repairplan.RenderText(os.Stdout, plan); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
