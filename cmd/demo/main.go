// Demo of the keyq scheduler: same-key serial order, cross-key
// parallelism, live rescale, and cancellation.
package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"keyq"
)

func main() {
	s := keyq.New(keyq.WithShards(2))

	var mu sync.Mutex
	log := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	}

	task := func(name string, d time.Duration) keyq.TaskFunc {
		return func(ctx context.Context) error {
			log("start  %s", name)
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return ctx.Err()
			}
			log("finish %s", name)
			return nil
		}
	}

	log("shards=%d: submit alpha-0..5 (same key, strictly serial)", s.ShardCount())
	var handles []*keyq.Handle
	for i := 0; i < 6; i++ {
		handles = append(handles, s.Submit("alpha", task(fmt.Sprintf("alpha-%d", i), 150*time.Millisecond)))
	}
	log("submit beta-0..2 (different key, runs in parallel)")
	for i := 0; i < 3; i++ {
		handles = append(handles, s.Submit("beta", task(fmt.Sprintf("beta-%d", i), 220*time.Millisecond)))
	}

	time.Sleep(350 * time.Millisecond)
	s.Rescale(4)
	log("rescaled 2 -> 4 shards mid-flight; alpha keeps its order")

	for i := 6; i < 9; i++ {
		handles = append(handles, s.Submit("alpha", task(fmt.Sprintf("alpha-%d", i), 100*time.Millisecond)))
	}

	cancelled := s.Submit("alpha", task("alpha-X", 10*time.Millisecond))
	time.Sleep(50 * time.Millisecond)
	if cancelled.Cancel() {
		log("cancelled queued task alpha-X; remaining alpha tasks keep order")
	}

	for _, h := range handles {
		if err := h.Wait(); err != nil {
			log("task error: %v", err)
		}
	}
	log("cancelled handle reports: %v", cancelled.Wait())

	s.Rescale(1)
	log("rescaled 4 -> 1 shard; draining via Shutdown")
	if err := s.Shutdown(context.Background()); err != nil {
		log("shutdown: %v", err)
	}
	log("shutdown complete")
}
