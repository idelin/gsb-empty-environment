// Demo of the batcher: items are submitted concurrently and executed in
// batches flushed by size (4) or by the first item's wait limit (80ms).
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"batcher"
)

func main() {
	exec := func(ctc context.Context, items []int) ([]string, error) {
		fmt.Printf("exec batch %v\n", items)
		time.Sleep(20 * time.Millisecond) // simulate work
		out := make([]string, len(items))
		for i, v := range items {
			out[i] = fmt.Sprintf("result-%d", v)
		}
		return out, nil
	}

	b := batcher.New(4, 80*time.Millisecond, exec, nil) // nil clock = real time
	defer b.Close()

	var wg sync.WaitGroup
	submit := func(ctx context.Context, item int) {
		defer wg.Done()
		res, err := b.Submit(ctx, item)
		if err != nil {
			fmt.Printf("submit %2d -> error: %v\n", item, err)
			return
		}
		fmt.Printf("submit %2d -> %s\n", item, res)
	}

	// First wave: 4 items fill a batch and flush by size.
	for i := 1; i <= 4; i++ {
		wg.Add(1)
		go submit(context.Background(), i)
	}
	// Second wave: 2 items, not enough to fill a batch; the 80ms wait
	// limit of the first item flushes them. Item 6 is cancelled first.
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(3)
	go submit(ctx, 6)
	go submit(context.Background(), 5)
	time.Sleep(10 * time.Millisecond)
	cancel() // item 6 is removed from the open batch
	go submit(context.Background(), 7)
	wg.Wait()
}
