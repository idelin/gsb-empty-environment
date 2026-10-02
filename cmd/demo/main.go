// Demo: leaf underflow on delete triggers borrowing from the left sibling.
package main

import (
	"fmt"

	"bplustree"
)

func main() {
	// Order 4: a node holds at most 4 keys and underflows below 4/2 = 2.
	tree := bplustree.New(4)
	for k := 1; k <= 9; k++ {
		tree.Insert(k, k*10)
	}
	fmt.Println("== insert 1..9 ==")
	fmt.Print(tree)

	// Setup: delete 2 forces a merge, leaving leaves [1 3 4] [5 6] [7 8 9].
	tree.Delete(2)
	fmt.Println("== delete 2 (merge, left sibling now has a surplus) ==")
	fmt.Print(tree)

	// Trigger: delete 5 underflows leaf [5 6] -> [6].
	// The left sibling [1 3 4] has a surplus, so its maximum record (4)
	// is borrowed and the parent separator 5 is rewritten to 4.
	h := tree.Height()
	tree.Delete(5)
	fmt.Println("== delete 5 (borrow 4 from left sibling) ==")
	fmt.Print(tree)

	fmt.Printf("height: %d -> %d (unchanged)\n", h, tree.Height())
	fmt.Printf("keys:   %v\n", tree.Keys())
	if v, ok := tree.Get(4); ok {
		fmt.Printf("Get(4) = %d (borrowed record still reachable)\n", v)
	}
}
