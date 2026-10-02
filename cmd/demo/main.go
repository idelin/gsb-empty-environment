// Command demo builds a B+ tree with 10000 consecutive records and runs
// the range scan Scan(2500, 7500) through a tiny 8-frame buffer pool.
package main

import (
	"fmt"

	"bptree"
)

func main() {
	pool := bptree.NewBufferPool(8) // deliberately tiny: forces page eviction
	tree, err := bptree.NewBPlusTree(pool)
	if err != nil {
		panic(err)
	}

	for k := int64(0); k < 10000; k++ {
		if err := tree.Insert(k, k*10); err != nil {
			panic(err)
		}
	}
	fmt.Printf("inserted 10000 records, pins after load = %d\n", pool.TotalPins())

	it, err := tree.Scan(2500, 7500)
	if err != nil {
		panic(err)
	}
	count := 0
	for k, v, ok := it.Next(); ok; k, v, ok = it.Next() {
		if count < 3 || count >= 4997 {
			fmt.Printf("  key=%d value=%d\n", k, v)
		} else if count == 3 {
			fmt.Println("  ...")
		}
		count++
	}
	if err := it.Err(); err != nil {
		panic(err)
	}

	fmt.Printf("scanned %d records in [2500, 7500)\n", count)
	fmt.Printf("buffer pool: hits=%d misses=%d evictions=%d outstanding-pins=%d\n",
		pool.Hits, pool.Misses, pool.Evictions, pool.TotalPins())
}
