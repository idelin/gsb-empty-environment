package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"latchcrabbing"
)

func main() {
	const (
		preloaded        = 2000
		readerCount      = 10
		writerCount      = 5
		readsPerReader   = 2000
		insertsPerWriter = 400
	)

	tree := latchcrabbing.New(4)
	for i := 0; i < preloaded; i++ {
		tree.Insert(i)
	}

	var wg sync.WaitGroup
	var readErrors int32
	start := make(chan struct{})

	for reader := 0; reader < readerCount; reader++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < readsPerReader; i++ {
				key := (id*131 + i) % preloaded
				if !tree.Contains(key) {
					atomic.AddInt32(&readErrors, 1)
					return
				}
			}
		}(reader)
	}

	for writer := 0; writer < writerCount; writer++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			base := preloaded + id*insertsPerWriter
			for i := 0; i < insertsPerWriter; i++ {
				tree.Insert(base + i)
			}
		}(writer)
	}

	close(start)
	wg.Wait()

	stats := tree.Stats()
	fmt.Printf("readers=%d writers=%d preload=%d inserts_per_writer=%d\n",
		readerCount, writerCount, preloaded, insertsPerWriter)
	fmt.Printf("final_len=%d height=%d read_errors=%d\n",
		tree.Len(), tree.Height(), atomic.LoadInt32(&readErrors))
	fmt.Printf("read_latches=%d write_latches=%d early_write_releases=%d\n",
		stats.ReadLatches, stats.WriteLatches, stats.EarlyWriteReleases)

	if readErrors != 0 {
		os.Exit(1)
	}
}
