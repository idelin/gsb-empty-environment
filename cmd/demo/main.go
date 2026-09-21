// Demo of the key-ordered, dynamically sharded task scheduler.
//
// It submits ordered tasks for several keys, rescales the shard count in
// the middle of the stream, cancels one pending task, injects one failure,
// and prints the observed execution order.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"keysched/scheduler"
)

func main() {
	s := scheduler.New(
		scheduler.WithShards(2),
		scheduler.WithWorkersPerShard(2),
	)

	var mu sync.Mutex
	var log []string
	record := func(format string, args ...interface{}) {
		mu.Lock()
		log = append(log, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	// Gate holds the head of each key's chain so queues build up before
	// we start resharding.
	gate := make(chan struct{})
	boom := errors.New("injected failure")

	type submitted struct {
		key string
		seq int
		f   *scheduler.Future
	}
	var all []submitted
	var canceled *scheduler.Future

	keys := []string{"order-1", "order-2", "order-3", "order-4"}
	const perKey = 6
	for _, key := range keys {
		for seq := 0; seq < perKey; seq++ {
			key, seq := key, seq
			f, err := s.Submit(key, func(ctx context.Context) error {
				<-gate
				record("execute %s#%d (shard=%d epoch=%d)", key, seq, s.ShardIndex(key), s.Epoch())
				time.Sleep(5 * time.Millisecond) // injected timing
				if key == "order-2" && seq == 3 {
					return boom // injected failure
				}
				return nil
			})
			if err != nil {
				panic(err)
			}
			all = append(all, submitted{key, seq, f})
			if key == "order-3" && seq == 4 {
				canceled = f // will be canceled while still pending
			}
		}
	}

	fmt.Printf("submitted %d tasks on %d keys, shards=%d\n", len(all), len(keys), s.ShardCount())

	fmt.Println("resharding 2 -> 8 (scale up) ...")
	s.Reshard(8)
	fmt.Println("resharding 8 -> 3 (scale down) ...")
	s.Reshard(3)

	fmt.Println("canceling order-3#4 ...")
	if ok := canceled.Cancel(); !ok {
		fmt.Println("  (too late, already running)")
	}

	close(gate) // release all chains; they drain across the new generation

	fmt.Println("\nresults:")
	for _, sub := range all {
		err := sub.f.Wait()
		switch {
		case errors.Is(err, scheduler.ErrCanceled):
			fmt.Printf("  %s#%d -> CANCELED\n", sub.key, sub.seq)
		case errors.Is(err, boom):
			fmt.Printf("  %s#%d -> FAILED: %v\n", sub.key, sub.seq, err)
		case err != nil:
			fmt.Printf("  %s#%d -> ERROR: %v\n", sub.key, sub.seq, err)
		default:
			fmt.Printf("  %s#%d -> ok\n", sub.key, sub.seq)
		}
	}

	fmt.Println("\nexecution trace (as observed):")
	for _, line := range log {
		fmt.Println("  " + line)
	}

	s.Close()
	fmt.Printf("\ndone: shards=%d epoch=%d live-key-queues=%d\n", s.ShardCount(), s.Epoch(), s.KeyCount())
}
