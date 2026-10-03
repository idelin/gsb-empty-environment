// Demo of the Multi-Paxos consensus log with incremental snapshots:
// a 5-node cluster elects a leader, suffers a 2|3 network partition, the
// majority replaces the old leader, repairs the log via Phase 1, and after
// healing safely overwrites the minority's dirty entries. Snapshots are
// archived along the way and a fresh node recovers from them.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"multipaxos/paxos"
)

func main() {
	dir, err := os.MkdirTemp("", "paxos-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	cfg := func(id int) paxos.Config {
		return paxos.Config{
			Lease:       2 * time.Second,
			IncrEvery:   2, // incremental snapshot every 2 applied ops
			ChkEvery:    4, // full checkpoint every 4 applied ops
			SnapshotDir: filepath.Join(dir, fmt.Sprintf("node%d", id)),
			Retain:      2,
		}
	}
	c := paxos.NewCluster(5, cfg)

	fmt.Println("== 5-node cluster, node 1 campaigns ==")
	leader := c.Node(1)
	must(leader.Campaign())
	propose(leader, "region", "cn-north")
	propose(leader, "zone", "a")
	fmt.Printf("node 1 committed %d slots (Phase-1 rounds so far: %d)\n\n",
		leader.CommitIndex(), leader.Prepares())

	fmt.Println("== partition: {1,2} | {3,4,5} ==")
	c.Hub.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, err := leader.Propose(paxos.Op{Kind: paxos.OpSet, Key: "dirty", Value: "x"}); err != nil {
		fmt.Printf("old leader propose through partition: %v (slot stays uncommitted)\n", err)
	}

	fmt.Println("\n== majority elects node 4 ==")
	newLeader := c.Node(4)
	must(newLeader.Campaign())
	propose(newLeader, "zone", "b") // lease valid: no Phase 1
	propose(newLeader, "tier", "gold")
	fmt.Printf("node 4 commitIndex=%d, Phase-1 rounds=%d (lease fast path)\n",
		newLeader.CommitIndex(), newLeader.Prepares())

	fmt.Println("\n== heal; new leader syncs the minority ==")
	c.Hub.Heal()
	if _, err := leader.Propose(paxos.Op{Kind: paxos.OpSet, Key: "stale", Value: "y"}); err != nil {
		fmt.Printf("old leader propose after heal: %v (it stepped down; majority promised a higher ballot)\n", err)
	}
	c.SyncAll(newLeader)
	for _, n := range c.Nodes {
		zone, _ := n.KV().Get("zone")
		_, hasDirty := n.KV().Get("dirty")
		fmt.Printf("node %d: commitIndex=%d zone=%s dirty-applied=%v\n",
			n.ID(), n.CommitIndex(), zone, hasDirty)
	}

	fmt.Println("\n== snapshot archive of node 4 ==")
	files, _ := os.ReadDir(filepath.Join(dir, "node4"))
	for _, f := range files {
		fmt.Println("  " + f.Name())
	}

	fmt.Println("\n== fresh node recovers from the archive ==")
	fresh, err := paxos.NewNode(99, nil, paxos.NewHub(),
		paxos.Config{SnapshotDir: filepath.Join(dir, "node4")})
	must(err)
	must(fresh.Recover())
	zone, _ := fresh.KV().Get("zone")
	tier, _ := fresh.KV().Get("tier")
	fmt.Printf("recovered: commitIndex=%d zone=%s tier=%s\n",
		fresh.CommitIndex(), zone, tier)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func propose(n *paxos.Node, k, v string) {
	slot, err := n.Propose(paxos.Op{Kind: paxos.OpSet, Key: k, Value: v})
	if err != nil {
		panic(err)
	}
	fmt.Printf("  committed slot %d: %s=%s\n", slot, k, v)
}
