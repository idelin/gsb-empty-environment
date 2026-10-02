// Command inspect-demo builds a B+ tree under a heavy concurrent
// insert/delete workload, runs the invariant inspector, then injects a
// "parent routing key greater than left subtree max" fault and inspects
// again to show the pinpointed violation report.
package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"bptree"
)

func main() {
	tr := bptree.New()

	// Phase 1: heavy concurrent writes and deletes.
	const workers = 8
	const opsPerWorker = 20000
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w + 1)))
			base := w * 1_000_000
			for op := 0; op < opsPerWorker; op++ {
				key := base + rng.Intn(50000)
				if rng.Intn(100) < 55 {
					tr.Insert(key, key)
				} else {
					tr.Delete(key)
				}
			}
		}(w)
	}
	wg.Wait()
	fmt.Printf("phase 1: %d workers x %d mixed insert/delete ops in %v\n\n",
		workers, opsPerWorker, time.Since(start).Round(time.Millisecond))

	// Phase 2: inspect the healthy tree.
	fmt.Println("phase 2: inspecting tree after concurrent workload")
	fmt.Print(bptree.Inspect(tr))

	// Phase 3: inject "parent routing key greater than left subtree max".
	victim := findInternal(tr.Root())
	idx := len(victim.Keys()) / 2
	trueMax := subtreeMaxOf(victim.Children()[idx])
	injected := trueMax + 1
	bptree.InjectRoutingKey(victim, idx, injected)
	fmt.Printf("\nphase 3: injected fault - routing key at internal node %p, keys[%d]: %d -> %d (true left-subtree max is %d)\n\n",
		victim, idx, trueMax, injected, trueMax)

	// Phase 4: re-inspect and print the located violation.
	fmt.Println("phase 4: re-inspecting corrupted tree")
	rep := bptree.Inspect(tr)
	fmt.Print(rep)
	if !rep.OK() {
		fmt.Println("fault successfully detected and located")
	}
}

func findInternal(root *bptree.Node) *bptree.Node {
	queue := []*bptree.Node{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.IsLeaf() {
			continue
		}
		if len(n.Keys()) > 0 {
			return n
		}
		queue = append(queue, n.Children()...)
	}
	panic("no internal node")
}

func subtreeMaxOf(n *bptree.Node) int {
	for !n.IsLeaf() {
		ch := n.Children()
		n = ch[len(ch)-1]
	}
	k := n.Keys()
	return k[len(k)-1]
}
