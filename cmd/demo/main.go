// Command demo simulates the full acceptance scenario:
//  1. the config center changes the DB password;
//  2. it publishes the new config and revokes all old leases;
//  3. a partitioned slow node misses the update, its old lease expires
//     naturally, and it stops accepting traffic on its own.
package main

import (
	"fmt"
	"time"

	"configlease"
)

const leaseTTL = 1500 * time.Millisecond

func tryServe(node *configlease.Node, query string) {
	out, err := node.Serve(query)
	if err != nil {
		fmt.Printf("  [%s] REJECTED: %v\n", query, err)
		return
	}
	fmt.Printf("  [%s] OK -> %s\n", query, out)
}

func main() {
	center := configlease.NewCenter(configlease.RealClock{}, leaseTTL,
		configlease.Config{Version: 1, DBPassword: "s3cret-v1"})

	fmt.Println("== t=0ms: two nodes bind to config v1 ==")
	fast := configlease.NewNode("fast", center, configlease.RealClock{})
	slow := configlease.NewNode("slow", center, configlease.RealClock{})
	tryServe(fast, "SELECT 1")
	tryServe(slow, "SELECT 1")

	// Background renewer keeps the connected "fast" node bound.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				fast.Renew()
			}
		}
	}()
	// "slow" is partitioned from the center: it never renews again.

	time.Sleep(600 * time.Millisecond)
	fmt.Println("\n== t=600ms: center changes DB password, publishes v2, revokes old leases ==")
	center.UpdateConfig(configlease.Config{DBPassword: "s3cret-v2"})
	fmt.Printf("  old lease of slow node still valid at center? %v\n",
		center.Validate(slow.Lease().ID))

	time.Sleep(400 * time.Millisecond)
	fmt.Println("\n== t=1000ms: fast node re-bound to v2; slow node still inside old lease TTL ==")
	tryServe(fast, "SELECT 1")
	tryServe(slow, "SELECT 1")

	time.Sleep(700 * time.Millisecond)
	fmt.Println("\n== t=1700ms: slow node's old lease expired naturally (TTL 1500ms) ==")
	tryServe(fast, "UPDATE accounts SET balance = balance - 1")
	tryServe(slow, "UPDATE accounts SET balance = balance - 1")

	fmt.Println("\n== slow node reconnects, re-binds to v2, resumes serving ==")
	slow.Renew()
	tryServe(slow, "SELECT 1")
}
