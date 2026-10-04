// Demo: 5-node Multi-Paxos cluster with incremental snapshots.
//
// Scenario: node 1 is elected and commits entries; the network partitions
// {1,2} | {3,4,5}; the old leader's new proposal stays uncommitted in the
// minority; node 3 wins Phase 1 in the majority, repairs open slots and
// commits new entries; the partition heals and the minority's dirty log is
// safely overwritten. Finally a checkpoint is archived to disk and restored
// into a fresh state machine.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"paxoslab/paxos"
	"paxoslab/snapshot"
)

func parseOp(v []byte) snapshot.Op {
	s := string(v)
	if strings.HasPrefix(s, "del ") {
		return snapshot.Del(strings.TrimPrefix(s, "del "))
	}
	kv := strings.SplitN(s, "=", 2)
	return snapshot.Put(kv[0], kv[1])
}

func main() {
	dir, err := os.MkdirTemp("", "paxos-demo-*")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)

	net := paxos.NewNetwork()
	machines := map[int]*snapshot.Machine{}
	snaps := map[int]*snapshot.Snapshotter{}
	nodes := map[int]*paxos.Node{}

	for id := 1; id <= 5; id++ {
		m := snapshot.NewMachine()
		arch, err := snapshot.NewFileArchive(filepath.Join(dir, fmt.Sprintf("node-%d", id)))
		if err != nil {
			log.Fatal(err)
		}
		machines[id] = m
		snaps[id] = snapshot.NewSnapshotter(m, arch)
		id := id
		nodes[id] = paxos.NewNode(id, net, 0, func(slot uint64, value []byte) {
			if paxos.IsNoop(value) {
				return
			}
			if err := m.Apply(slot, 1, parseOp(value)); err != nil {
				log.Fatalf("node %d apply slot %d: %v", id, slot, err)
			}
			if slot%3 == 0 { // incremental snapshot every 3 applied entries
				if _, err := snaps[id].CaptureIncremental(); err != nil {
					log.Fatalf("node %d capture: %v", id, err)
				}
			}
		})
	}

	fmt.Println("== 5-node cluster, electing node 1 ==")
	must(nodes[1].Campaign())
	propose(t{nodes[1], "alpha=1"}, t{nodes[1], "beta=2"}, t{nodes[1], "gamma=3"})
	fmt.Println("committed on all nodes: alpha=1 beta=2 gamma=3")

	fmt.Println("\n== partition {1,2} | {3,4,5} ==")
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, err := nodes[1].Propose([]byte("dirty=999")); err != nil {
		fmt.Printf("old leader propose in minority: %v (stays uncommitted)\n", err)
	}

	fmt.Println("\n== majority elects node 3 (Phase 1 repairs open slots) ==")
	must(nodes[3].Campaign())
	propose(t{nodes[3], "delta=4"}, t{nodes[3], "epsilon=5"})
	fmt.Println("new leader committed: delta=4 epsilon=5")

	fmt.Println("\n== heal; minority syncs; dirty entries overwritten ==")
	net.HealAll()
	if _, err := nodes[1].Propose([]byte("stale=0")); err != nil {
		fmt.Printf("stale leader propose after heal: %v\n", err)
	}
	must(nodes[1].SyncFrom(3))
	must(nodes[2].SyncFrom(3))

	fmt.Print("\napplied log per node:")
	for id := 1; id <= 5; id++ {
		fmt.Printf("\n  node %d: %s", id, formatLog(nodes[id].AppliedValues()))
	}
	fmt.Println()

	fmt.Println("\n== checkpoint node 3 and restore into a fresh machine ==")
	cp, err := snaps[3].Checkpoint()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("checkpoint archived at index %d (%d keys)\n", cp.LastIndex, len(cp.Data))

	arch, err := snapshot.NewFileArchive(filepath.Join(dir, "node-3"))
	if err != nil {
		log.Fatal(err)
	}
	fresh := snapshot.NewMachine()
	if err := snapshot.Restore(arch, fresh); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("restored machine: lastIndex=%d state=%v\n", fresh.LastIndex(), fresh.Keys())
	fmt.Printf("archive dir: %s\n", filepath.Join(dir, "node-3"))
}

type t = struct {
	n *paxos.Node
	v string
}

func propose(ops ...t) {
	for _, op := range ops {
		if _, err := op.n.Propose([]byte(op.v)); err != nil {
			log.Fatalf("propose %q: %v", op.v, err)
		}
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func formatLog(vs [][]byte) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		if paxos.IsNoop(v) {
			parts[i] = "<noop>"
		} else {
			parts[i] = string(v)
		}
	}
	return strings.Join(parts, " | ")
}
