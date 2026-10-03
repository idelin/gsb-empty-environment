package main

import (
	"fmt"
	"log"

	bptree "example.com/bptree"
)

func main() {
	// A tiny 16-frame buffer pool serves a tree of 10000 records.
	pool := bptree.NewBufferPool(16)
	tree, err := bptree.NewBPlusTree(pool)
	if err != nil {
		log.Fatal(err)
	}

	for i := 0; i < 10000; i++ {
		if err := tree.Insert(i, i*10); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("inserted 10000 records into %d pages\n", pool.PagesOnDisk())

	it, err := tree.Scan(2500, 7500)
	if err != nil {
		log.Fatal(err)
	}
	var first, last, count int
	for {
		k, v, ok := it.Next()
		if !ok {
			break
		}
		if count == 0 {
			first = k
		}
		if count < 3 || count >= 4998 {
			fmt.Printf("  key=%d value=%d\n", k, v)
		}
		last = k
		count++
	}
	if err := it.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Scan(2500, 7500): %d records, keys %d..%d\n", count, first, last)
	fmt.Printf("buffer pool: hits=%d misses=%d evictions=%d pinned=%d\n",
		pool.Hits, pool.Misses, pool.Evictions, pool.PinnedCount())
}
