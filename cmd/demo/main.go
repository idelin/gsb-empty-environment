// Demo of B+ tree leaf borrowing during delete rebalancing.
package main

import (
	"fmt"

	"bplustree"
)

func main() {
	tr := bplustree.New(4) // M=4: max 4 keys per leaf, min fill = 2
	for k := 1; k <= 14; k++ {
		tr.Insert(k)
	}

	fmt.Println("=== After inserting 1..14 (order M=4, min fill M/2=2) ===")
	fmt.Print(tr.String())
	fmt.Printf("height=%d size=%d keys=%v\n\n", tr.Height(), tr.Size(), tr.Keys())

	fmt.Println("=== Delete 13: leaf [14] underflows (1 < 2), borrow max key 12 from left sibling ===")
	tr.Delete(13)
	fmt.Print(tr.String())
	fmt.Printf("height=%d size=%d keys=%v\n\n", tr.Height(), tr.Size(), tr.Keys())

	fmt.Println("=== Delete 1, 4, 5: leaf [6] underflows, left sibling at min, borrow min key 7 from right sibling ===")
	tr.Delete(1)
	tr.Delete(4)
	tr.Delete(5)
	fmt.Print(tr.String())
	fmt.Printf("height=%d size=%d keys=%v\n", tr.Height(), tr.Size(), tr.Keys())
}
