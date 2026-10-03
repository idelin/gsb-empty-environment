// Command inspect is an offline B+ tree integrity inspector. It hammers
// a tree with concurrent writes and deletes, runs a full invariant
// sweep, then injects the classic "parent routing key greater than left
// subtree max" corruption and shows the located violation report.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"sync"

	"bplusinspect/bplustree"
)

func hammer(tree *bplustree.Tree, workers, ops int) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < ops; i++ {
				key := rng.Intn(5000)
				mu.Lock()
				if rng.Intn(100) < 55 {
					tree.Insert(key, key*10)
				} else {
					tree.Delete(key)
				}
				mu.Unlock()
			}
		}(int64(w)*104729 + 7)
	}
	wg.Wait()
}

// injectRouteKeyFault corrupts one internal node so that a routing key
// exceeds the maximum key of its left subtree.
func injectRouteKeyFault(n *bplustree.Node) bool {
	if n.IsLeaf {
		return false
	}
	if len(n.Children) >= 2 && len(n.Children[0].Keys) > 0 {
		left := n.Children[0]
		for !left.IsLeaf {
			left = left.Children[len(left.Children)-1]
		}
		n.Keys[0] = left.Keys[len(left.Keys)-1] + 1000
		return true
	}
	for _, c := range n.Children {
		if injectRouteKeyFault(c) {
			return true
		}
	}
	return false
}

func main() {
	tree := bplustree.New(8)

	fmt.Println("== phase 1: 8 goroutines x 5000 random insert/delete ==")
	hammer(tree, 8, 5000)
	fmt.Printf("tree settled: %d live keys\n\n", tree.Size())

	fmt.Println("== phase 2: full invariant sweep on healthy tree ==")
	rep := bplustree.Inspect(tree)
	fmt.Print(rep.String())
	if !rep.OK() {
		os.Exit(1)
	}

	fmt.Println("== phase 3: inject 'routing key > left subtree max' fault ==")
	if !injectRouteKeyFault(tree.Root) {
		fmt.Println("no internal node available for injection")
		os.Exit(1)
	}
	fmt.Println("fault injected at an internal node near the root")

	fmt.Println("\n== phase 4: re-inspect corrupted tree ==")
	rep = bplustree.Inspect(tree)
	fmt.Print(rep.String())
	if rep.OK() {
		fmt.Println("ERROR: inspector missed the injected fault")
		os.Exit(1)
	}
}
