// Command demo runs the acceptance scenario end to end:
// a node's load weight changes 10 -> 80, gossip spreads the update to
// every replica, and a routing query for "zone=us-east" returns the
// healthy node with the highest weight.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"noderouter"
)

func main() {
	cluster := noderouter.NewCluster(rand.New(rand.NewSource(time.Now().UnixNano())))

	type spec struct {
		id      string
		zone    string
		weight  int
		role    noderouter.Role
		healthy bool
	}
	specs := []spec{
		{"alpha", "us-east", 10, noderouter.RoleWorker, true},
		{"bravo", "us-east", 10, noderouter.RoleWorker, true},
		{"charlie", "us-east", 60, noderouter.RoleWorker, false}, // unhealthy
		{"delta", "us-west", 90, noderouter.RoleWorker, true},
		{"echo", "us-east", 30, noderouter.RoleMaster, true},
	}

	lists := map[string]*noderouter.MemberList{}
	var seed []noderouter.NodeState
	for i, sp := range specs {
		ml := noderouter.NewMemberList(sp.id)
		ml.Join(fmt.Sprintf("127.0.0.1:%d", 7000+i),
			noderouter.Metadata{Zone: sp.zone, Weight: sp.weight, Role: sp.role, Healthy: sp.healthy},
			map[string]string{"disk": "ssd", "env": "prod"})
		cluster.Add(ml)
		lists[sp.id] = ml
		self, _ := ml.Self()
		seed = append(seed, self)
	}

	// Introducer pattern: alpha learns the full membership, gossip spreads it.
	lists["alpha"].Merge(seed)
	rounds, _ := cluster.Converge(3, 20)
	fmt.Printf("[bootstrap] membership of %d nodes converged in %d gossip round(s)\n", len(specs), rounds)

	printRoute := func(where string) {
		r := noderouter.NewRouter(lists[where])
		best, err := r.Route("zone=us-east")
		if err != nil {
			fmt.Printf("[route]   node=%s selector=zone=us-east -> %v\n", where, err)
			return
		}
		fmt.Printf("[route]   node=%s selector=zone=us-east -> %s (weight=%d, role=%s, healthy=%t, v=%d)\n",
			where, best.ID, best.Meta.Weight, best.Meta.Role, best.Meta.Healthy, best.Version)
	}

	fmt.Println("\n-- before update: bravo weight=10 --")
	printRoute("alpha")

	// Dynamic metadata update: bravo's load weight 10 -> 80.
	bravo, _ := lists["bravo"].UpdateLocal(func(meta *noderouter.Metadata) {
		meta.Weight = 80
	})
	fmt.Printf("\n[update]  bravo weight 10 -> 80 (version %d)\n", bravo.Version)

	rounds, _ = cluster.Converge(3, 20)
	fmt.Printf("[gossip]  update converged to all replicas in %d round(s)\n", rounds)

	fmt.Println("\n-- after gossip: every replica routes zone=us-east --")
	for _, sp := range specs {
		printRoute(sp.id)
	}

	fmt.Println("\n-- full healthy subset of zone=us-east, weight desc --")
	sub, _ := noderouter.NewRouter(lists["echo"]).Subset("zone=us-east")
	for _, s := range sub {
		fmt.Printf("          %s\n", s)
	}
}
