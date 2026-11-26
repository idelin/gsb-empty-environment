// Command bgpsim-demo prints round-by-round expansions for the four
// acceptance scenarios. It is a thin consumer of the importable bgpsim
// package.
package main

import (
	"fmt"
	"os"

	bgpsim "prescreen.local/ps-b15ac20600"
)

const prefix = "198.51.100.0/24"

func triangleConfig(acme bool) *bgpsim.Config {
	ids := []bgpsim.NodeID{"rr1", "rr2", "rr3", "b1", "b2", "b3"}
	igp, err := bgpsim.DenseIGP(ids, [][]int{
		{0, 9, 9, 2, 1, 3},
		{9, 0, 9, 3, 2, 1},
		{9, 9, 0, 1, 3, 2},
		{2, 3, 1, 0, 9, 9},
		{1, 2, 3, 9, 0, 9},
		{3, 1, 2, 9, 9, 0},
	})
	if err != nil {
		panic(err)
	}
	return &bgpsim.Config{
		Nodes: []bgpsim.Node{
			{ID: "rr1", RouterID: "10.0.0.1", Reflector: true},
			{ID: "rr2", RouterID: "10.0.0.2", Reflector: true},
			{ID: "rr3", RouterID: "10.0.0.3", Reflector: true},
			{ID: "b1", RouterID: "10.0.1.1", Border: true},
			{ID: "b2", RouterID: "10.0.1.2", Border: true},
			{ID: "b3", RouterID: "10.0.1.3", Border: true},
		},
		Clusters: []bgpsim.Cluster{
			{ID: "1", Reflectors: []bgpsim.NodeID{"rr1"}, Clients: []bgpsim.NodeID{"b1"}},
			{ID: "2", Reflectors: []bgpsim.NodeID{"rr2"}, Clients: []bgpsim.NodeID{"b2"}},
			{ID: "3", Reflectors: []bgpsim.NodeID{"rr3"}, Clients: []bgpsim.NodeID{"b3"}},
		},
		ExternalRoutes: []bgpsim.Route{
			{Prefix: prefix, Egress: "b1", RouterID: "1.1.1.1", NeighborAS: 10, MED: 10, LocalPref: 100, ASPathLen: 2},
			{Prefix: prefix, Egress: "b2", RouterID: "2.2.2.2", NeighborAS: 20, MED: 20, LocalPref: 100, ASPathLen: 2},
			{Prefix: prefix, Egress: "b3", RouterID: "3.3.3.3", NeighborAS: 10, MED: 30, LocalPref: 100, ASPathLen: 2},
		},
		IGP:     igp,
		Options: bgpsim.Options{AlwaysCompareMED: acme},
	}
}

func rrRow(res *bgpsim.Result, round int) []string {
	row := []string{}
	for _, rr := range []bgpsim.NodeID{"rr1", "rr2", "rr3"} {
		best := res.BestRouteAt(round, rr, prefix)
		if best == nil {
			row = append(row, "-")
		} else {
			row = append(row, string(best.Egress))
		}
	}
	return row
}

func printTriangle(title string, acme bool) {
	fmt.Println("==", title, "==")
	res, err := bgpsim.Run(triangleConfig(acme))
	if err != nil {
		fmt.Fprintln(os.Stderr, "simulate:", err)
		os.Exit(1)
	}
	fmt.Println("round  rr1 rr2 rr3")
	for i := range res.Rounds {
		r := rrRow(res, i)
		fmt.Printf("%-6d %-3s %-3s %-3s\n", i, r[0], r[1], r[2])
	}
	if o := res.Oscillation; o != nil {
		fmt.Printf("OSCILLATION first-state=round-%d repeated-at=round-%d min-period=%d\n",
			o.FirstRound, o.RepeatRound, o.Period)
	} else {
		fmt.Printf("STATUS %s\n", res.Status)
	}
	fmt.Printf("incomparability eliminations: %d\n\n", len(res.MEDIncomparableEliminations))
}

func printRouterID() {
	fmt.Println("== IGP tie -> lowest RouterID ==")
	res, err := bgpsim.Run(&bgpsim.Config{
		Nodes: []bgpsim.Node{
			{ID: "rr", RouterID: "10.2.0.1", Reflector: true},
			{ID: "e1", RouterID: "10.2.1.1", Border: true},
			{ID: "e2", RouterID: "10.2.1.2", Border: true},
		},
		Clusters: []bgpsim.Cluster{{
			ID: "9", Reflectors: []bgpsim.NodeID{"rr"}, Clients: []bgpsim.NodeID{"e1", "e2"},
		}},
		ExternalRoutes: []bgpsim.Route{
			{Prefix: "p", Egress: "e1", RouterID: "4.4.4.4", NeighborAS: 1, MED: 50, LocalPref: 100, ASPathLen: 1},
			{Prefix: "p", Egress: "e2", RouterID: "2.2.2.2", NeighborAS: 1, MED: 50, LocalPref: 100, ASPathLen: 1},
		},
		IGP: map[bgpsim.NodeID]map[bgpsim.NodeID]int{
			"rr": {"e1": 5, "e2": 5},
			"e1": {"e2": 5},
			"e2": {"e1": 5},
		},
	})
	if err != nil {
		panic(err)
	}
	best := res.BestRoute("rr", "p")
	fmt.Printf("winner egress=%s router-id=%s stage-tiebreak=RouterID\n\n", best.Egress, best.RouterID)
}

func printClusterLoop() {
	fmt.Println("== dual-homed client cluster-loop filtering ==")
	res, err := bgpsim.Run(&bgpsim.Config{
		Nodes: []bgpsim.Node{
			{ID: "rr1", RouterID: "10.1.0.1", Reflector: true},
			{ID: "rr2", RouterID: "10.1.0.2", Reflector: true},
			{ID: "b", RouterID: "10.1.1.1", Border: true},
			{ID: "c", RouterID: "10.1.2.1"},
		},
		Clusters: []bgpsim.Cluster{
			{ID: "1", Reflectors: []bgpsim.NodeID{"rr1"}, Clients: []bgpsim.NodeID{"b", "c"}},
			{ID: "2", Reflectors: []bgpsim.NodeID{"rr2"}, Clients: []bgpsim.NodeID{"c"}},
		},
		ExternalRoutes: []bgpsim.Route{
			{Prefix: "p", Egress: "b", RouterID: "9.9.9.9", NeighborAS: 65000, LocalPref: 100, ASPathLen: 1},
		},
		IGP: map[bgpsim.NodeID]map[bgpsim.NodeID]int{
			"rr1": {"b": 1}, "rr2": {"b": 2}, "c": {"b": 5},
		},
	})
	if err != nil {
		panic(err)
	}
	for _, ev := range res.ClusterLoopDiscards {
		fmt.Printf("round %d: %s discards ad from %s containing own cluster; list=%v\n",
			ev.Round, ev.Node, ev.From, ev.ClusterList)
	}
	fmt.Printf("STATUS %s\n\n", res.Status)
}

func main() {
	printTriangle("three-RR MED triangle (default: compare MED inside neighbor AS only)", false)
	printTriangle("same triangle with AlwaysCompareMED", true)
	printClusterLoop()
	printRouterID()
}
