// Command demo runs a small link-state routing scenario on the
// discrete-event simulator: initial convergence, a link failure, its
// recovery, and a router failure handled by LSA ageing.
package main

import (
	"fmt"
	"sort"

	sim "example.com/lspsim"
)

func main() {
	s := sim.NewSimulator()
	net := sim.NewNetwork(s)

	routers := make([]*sim.Router, 6)
	for i := range routers {
		r := sim.NewRouter(i)
		r.LSALifetime = 120
		r.SPFDelay = 10
		net.AddRouter(r)
		routers[i] = r
	}

	ring := [][2]int{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 0}}
	links := map[[2]int]*sim.Link{}
	for _, e := range ring {
		links[e] = net.AddLink(routers[e[0]], routers[e[1]], 1, 3, 1)
	}
	net.AddLink(routers[0], routers[3], 1, 3, 1) // chord

	printRoutes := func(label string) {
		fmt.Printf("\n== %s @ tick %d ==\n", label, s.Now())
		for _, r := range routers {
			if r.Down {
				fmt.Printf("R%d: DOWN\n", r.ID)
				continue
			}
			fmt.Printf("R%d:", r.ID)
			dests := make([]int, 0, len(r.Routes()))
			for d := range r.Routes() {
				dests = append(dests, d)
			}
			sort.Ints(dests)
			for _, d := range dests {
				rt := r.Routes()[d]
				fmt.Printf("  R%d via R%d (cost %d)", d, rt.NextHop, rt.Cost)
			}
			fmt.Println()
		}
	}

	for _, r := range routers {
		r.Start()
	}
	s.After(80, func() { printRoutes("initial convergence") })
	s.After(100, func() {
		fmt.Printf("\n-- tick %d: link R1-R2 fails --", s.Now())
		net.SetLinkUp(links[[2]int{1, 2}], false)
	})
	s.After(200, func() { printRoutes("after link R1-R2 failure") })
	s.After(300, func() {
		fmt.Printf("\n-- tick %d: link R1-R2 recovers --", s.Now())
		net.SetLinkUp(links[[2]int{1, 2}], true)
	})
	s.After(400, func() { printRoutes("after recovery") })
	s.After(500, func() {
		fmt.Printf("\n-- tick %d: router R4 goes down --", s.Now())
		net.SetRouterDown(routers[4])
	})
	s.After(700, func() { printRoutes("after R4 down (its LSA expired everywhere)") })

	s.Run(10000)

	fmt.Println("\n== statistics ==")
	fmt.Printf("ran until tick %d, %d events processed\n", s.Now(), s.EventsProcessed)
	for _, r := range routers {
		fmt.Printf("R%d: SPF runs=%d, LSAs originated=%d, stale LSAs dropped=%d\n",
			r.ID, r.SPFRuns, r.Originated, r.DroppedOldLSA)
	}
	for _, l := range net.Links {
		fmt.Printf("link R%d-R%d: packets=%d, max backlog=%d ticks\n",
			l.A.ID, l.B.ID, l.Packets, l.MaxBacklog)
	}
}
