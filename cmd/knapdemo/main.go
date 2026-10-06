// Command knapdemo demonstrates that a checkpointed, repeatedly
// resumed branch-and-bound run explores exactly the same nodes as an
// uninterrupted run, and cross-checks the optimum by brute force.
package main

import (
	"bytes"
	"fmt"
	"math/rand"

	"knapbb"
)

func main() {
	r := rand.New(rand.NewSource(42))
	items := make([]knapbb.Item, 10)
	for i := range items {
		items[i] = knapbb.Item{Value: 1 + r.Intn(30), Weight: 1 + r.Intn(12), Bound: 1 + r.Intn(3)}
	}
	capacity := 40
	inst := knapbb.NewInstance(items, capacity)
	fmt.Printf("instance: %d items, capacity %d\n", len(items), capacity)

	// 1. Uninterrupted reference run.
	full := knapbb.NewSolver(inst)
	fullNodes := 0
	full.Trace = func([]int) { fullNodes++ }
	fullRes := full.Run(1 << 30)
	fmt.Printf("uninterrupted: optimal=%d nodes=%d\n", fullRes.BestValue, fullNodes)

	// 2. Same search, budget 7 nodes per session, checkpointed between.
	resumed := knapbb.NewSolver(inst)
	resumedNodes, checkpoints := 0, 0
	resumed.Trace = func([]int) { resumedNodes++ }
	var res knapbb.Result
	for {
		res = resumed.Run(7)
		if res.Done {
			break
		}
		var buf bytes.Buffer
		if err := resumed.Save(&buf); err != nil {
			panic(err)
		}
		var err error
		resumed, err = knapbb.Load(&buf)
		if err != nil {
			panic(err)
		}
		resumed.Trace = func([]int) { resumedNodes++ }
		checkpoints++
	}
	fmt.Printf("resumed (%d checkpoints): optimal=%d nodes=%d bounds=[%d,%d]\n",
		checkpoints, res.BestValue, resumedNodes, res.LowerBound, res.UpperBound)
	if resumedNodes != fullNodes || res.BestValue != fullRes.BestValue {
		panic("MISMATCH: resumed run diverged from uninterrupted run")
	}

	// 3. Independent exhaustive verification.
	best := 0
	counts := make([]int, len(inst.Items))
	var rec func(i, w, v int)
	rec = func(i, w, v int) {
		if w > inst.Capacity {
			return
		}
		if i == len(inst.Items) {
			if v > best {
				best = v
			}
			return
		}
		it := inst.Items[i]
		for c := 0; c <= it.Bound; c++ {
			counts[i] = c
			rec(i+1, w+c*it.Weight, v+c*it.Value)
		}
	}
	rec(0, 0, 0)
	fmt.Printf("exhaustive: optimal=%d\n", best)
	if best != fullRes.BestValue {
		panic("MISMATCH: solver optimum disagrees with exhaustive search")
	}
	fmt.Println("OK: all three agree; optimality claim verified")
}
