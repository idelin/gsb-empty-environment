// Command lseqdemo demonstrates the two acceptance boundaries:
//  1. 1000 dense inserts alternating between head and tail, reporting
//     how identifier depth (bit-width) grows;
//  2. three replicas merging the same operation set in random
//     permutations, converging to one strict causal order.
package main

import (
	"fmt"
	"math/rand"
	"sort"

	"lseq"
)

func main() {
	fmt.Println("=== 1. Alternating dense inserts (head/tail), n=1000 ===")
	doc := lseq.NewDocument(1, rand.New(rand.NewSource(42)))
	depthAt := make(map[int]int)
	for i := 0; i < 1000; i++ {
		ch := rune('a' + i%26)
		if i%2 == 0 {
			doc.Insert(0, ch)
		} else {
			doc.Insert(doc.Len(), ch)
		}
		depthAt[i+1] = doc.MaxDepth()
	}
	for _, n := range []int{100, 200, 400, 600, 800, 1000} {
		fmt.Printf("  after %4d inserts: maxDepth=%d\n", n, depthAt[n])
	}
	hist := doc.DepthHistogram()
	depths := make([]int, 0, len(hist))
	for d := range hist {
		depths = append(depths, d)
	}
	sort.Ints(depths)
	fmt.Print("  depth histogram: ")
	for _, d := range depths {
		fmt.Printf("depth%d=%d ", d, hist[d])
	}
	fmt.Println()

	fmt.Println("=== 2. Random-permutation merge across 3 replicas ===")
	// Site A types "hello"; site B concurrently prepends "XY";
	// site C concurrently appends "12"; A then deletes 'e'.
	a := lseq.NewDocument(1, rand.New(rand.NewSource(11)))
	var ops []lseq.Op
	for _, ch := range "hello" {
		ops = append(ops, a.Insert(a.Len(), ch))
	}
	b := lseq.NewDocument(2, rand.New(rand.NewSource(22)))
	c := lseq.NewDocument(3, rand.New(rand.NewSource(33)))
	for _, op := range ops {
		b.Merge(op)
		c.Merge(op)
	}
	for _, ch := range "XY" {
		ops = append(ops, b.Insert(0, ch))
	}
	for _, ch := range "12" {
		ops = append(ops, c.Insert(c.Len(), ch))
	}
	ops = append(ops, a.Delete(1))

	rng := rand.New(rand.NewSource(7))
	results := map[string]int{}
	for trial := 0; trial < 20; trial++ {
		replica := lseq.NewDocument(100+uint64(trial), nil)
		for _, idx := range rng.Perm(len(ops)) {
			replica.Merge(ops[idx])
		}
		results[replica.String()]++
	}
	for text, count := range results {
		fmt.Printf("  converged text %q seen in %d/20 random permutations\n", text, count)
	}
	if len(results) == 1 {
		fmt.Println("  => all replicas converge to the same strict causal order")
	} else {
		fmt.Println("  => DIVERGENCE DETECTED")
	}
}
